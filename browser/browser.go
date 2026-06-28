package browser

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/denandz/glorp/modifier"
)

type pendingResponse struct {
	id      string
	request *http.Request
}

type tabCapture struct {
	logger *modifier.Logger
	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	pending map[fetch.RequestID]*pendingResponse

	muProtoUpdate      sync.Mutex
	pendingProtoUpdate map[network.RequestID]string // netReqID -> glorp entry ID

	muWS   sync.Mutex
	wsURLs map[network.RequestID]string // netReqID -> WebSocket URL
}

func (t *tabCapture) eventHandler(ev any) {
	switch ev := ev.(type) {
	case *fetch.EventRequestPaused:
		if ev.ResponseStatusCode > 0 {
			t.handleResponse(ev)
			return
		}
		t.handleRequest(ev)
	case *fetch.EventAuthRequired:
		go func() {
			chromedp.Run(t.ctx, chromedp.ActionFunc(func(c context.Context) error {
				return fetch.ContinueWithAuth(ev.RequestID, &fetch.AuthChallengeResponse{
					Response: "Default",
				}).Do(c)
			}))
		}()
	case *network.EventResponseReceived:
		if ev.Response != nil {
			t.muProtoUpdate.Lock()
			entryID := t.pendingProtoUpdate[ev.RequestID]
			delete(t.pendingProtoUpdate, ev.RequestID)
			t.muProtoUpdate.Unlock()

			if entryID != "" && ev.Response.Protocol != "" {
				t.applyProto(entryID, ev.Response.Protocol)
			}
		}
	case *network.EventWebSocketCreated:
		t.muWS.Lock()
		t.wsURLs[ev.RequestID] = ev.URL
		t.muWS.Unlock()
	case *network.EventWebSocketFrameSent:
		t.handleWebSocketFrame(ev.RequestID, "sent", int(ev.Response.Opcode), ev.Response.PayloadData)
	case *network.EventWebSocketFrameReceived:
		t.handleWebSocketFrame(ev.RequestID, "received", int(ev.Response.Opcode), ev.Response.PayloadData)
	case *network.EventWebSocketFrameError:
		log.Printf("[!] Browser - Websocket Frame Error: %s, %s", ev.RequestID, ev.ErrorMessage)
	case *network.EventWebSocketWillSendHandshakeRequest:
		t.handleWebSocketHandshakeRequest(ev)
	case *network.EventWebSocketHandshakeResponseReceived:
		t.handleWebSocketHandshakeResponse(ev)

	case *network.EventWebSocketClosed:
		t.muWS.Lock()
		delete(t.wsURLs, ev.RequestID)
		t.muWS.Unlock()
	}
}

func newTabCapture(ctx context.Context, cancel context.CancelFunc, logger *modifier.Logger) *tabCapture {
	return &tabCapture{
		logger:             logger,
		ctx:                ctx,
		cancel:             cancel,
		pending:            make(map[fetch.RequestID]*pendingResponse),
		pendingProtoUpdate: make(map[network.RequestID]string),
		wsURLs:             make(map[network.RequestID]string),
	}
}

func (t *tabCapture) generateID() string {
	buf := make([]byte, 8)
	rand.Read(buf)
	return hex.EncodeToString(buf)
}

func (t *tabCapture) enableFetch() error {
	return chromedp.Run(t.ctx, fetch.Enable(), network.Enable())
}

func (t *tabCapture) handleWebSocketFrame(requestID network.RequestID, direction string, opcode int, payload string) {
	t.muWS.Lock()
	url := t.wsURLs[requestID]
	defer t.muWS.Unlock()

	if url == "" {
		return
	}

	id := t.generateID()

	msg := &modifier.WebSocketEntry{
		ID:        id,
		URL:       url,
		Direction: direction,
		Timestamp: time.Now(),
		Opcode:    opcode,
		Payload:   payload,
	}

	t.logger.InjectWebSocketMessage(msg)
}

func (t *tabCapture) handleWebSocketHandshakeRequest(ev *network.EventWebSocketWillSendHandshakeRequest) {
	t.muWS.Lock()
	rawUrl := t.wsURLs[ev.RequestID]
	t.muWS.Unlock()

	if rawUrl == "" {
		return
	}

	id := t.generateID()

	// websocket request always GET per RFC 6455
	req, err := http.NewRequest("GET", rawUrl, nil)
	if err != nil {
		log.Printf("[!] Browser - buildWSRequest: %s\n", err)
		return
	}

	for k, v := range ev.Request.Headers {
		switch val := v.(type) {
		case string:
			req.Header.Set(k, val)
		}
	}

	// need to parse the url back from ws and wss into http/https here
	// so DumpRequestOut doesn't complain in the logger
	if req.URL.Scheme == "wss" {
		req.URL.Scheme = "https"
	} else if req.URL.Scheme == "ws" {
		req.URL.Scheme = "http"
	}

	err = t.logger.InjectRequest(id, req, modifier.SourceBrowser)
	if err != nil {
		log.Printf("[!] Browser - InjectRequest (WS): %s\n", err)
	}

	// tweak the protocol back from http/https back to ws/wss
	entry := t.logger.GetEntry(id)
	entry.Request.URL = rawUrl

	t.muWS.Lock()
	t.pendingProtoUpdate[ev.RequestID] = id
	t.muWS.Unlock()
}

func (t *tabCapture) handleWebSocketHandshakeResponse(ev *network.EventWebSocketHandshakeResponseReceived) {
	t.muWS.Lock()
	id := t.pendingProtoUpdate[ev.RequestID]
	delete(t.pendingProtoUpdate, ev.RequestID)
	t.muWS.Unlock()

	if id == "" {
		return
	}

	headers := make(http.Header)
	for k, v := range ev.Response.Headers {
		switch val := v.(type) {
		case string:
			headers.Set(k, val)
		}
	}

	resp := &http.Response{
		StatusCode: int(ev.Response.Status),
		Status:     ev.Response.StatusText,
		Header:     headers,
		Body:       http.NoBody,
	}

	err := t.logger.InjectResponse(id, resp)
	if err != nil {
		log.Printf("[!] Browser - InjectResponse (WS): %s\n", err)
	}
}

func (t *tabCapture) applyProto(id, proto string) {
	_, _, display := parseProtocol(proto)
	entry := t.logger.GetEntry(id)
	if entry == nil || entry.Request == nil {
		return
	}

	t.logger.Lock()
	defer t.logger.Unlock()

	entry.Request.HTTPVersion = display
	entry.Response.HTTPVersion = display

	raw := entry.Request.Raw
	idx := bytes.Index(raw, []byte("HTTP/"))
	if idx >= 0 && idx+8 <= len(raw) {
		copy(raw[idx:], []byte(display))
	}

	raw = entry.Response.Raw
	idx = bytes.Index(raw, []byte("HTTP/"))
	if idx >= 0 && idx+8 <= len(raw) {
		copy(raw[idx:], []byte(display))
	}
}

func (t *tabCapture) continueReq(ev *fetch.EventRequestPaused, intercept bool) {
	err := chromedp.Run(t.ctx, chromedp.ActionFunc(func(c context.Context) error {
		return fetch.ContinueRequest(ev.RequestID).WithInterceptResponse(intercept).Do(c)
	}))

	if err != nil {
		log.Printf("[!] Browser - ContinueRequest: %s %s\n", ev.Request.URL, err)
	}
}

func (t *tabCapture) continueResp(ev *fetch.EventRequestPaused) {
	err := chromedp.Run(t.ctx, chromedp.ActionFunc(func(c context.Context) error {
		return fetch.ContinueResponse(ev.RequestID).Do(c)
	}))

	if err != nil {
		log.Printf("[!] Browser - ContinueResponse: %s %s\n", ev.Request.URL, err)
	}
}

func (t *tabCapture) handleRequest(ev *fetch.EventRequestPaused) {
	if ev.Request.URL == "" || ev.ResourceType == network.ResourceTypeDocument && ev.Request.URL == "about:blank" {
		go t.continueReq(ev, false)
		return
	}

	id := t.generateID()

	req, err := buildRequest(ev.Request)
	if err != nil {
		log.Printf("[!] Browser - buildRequest: %s\n", err)
		go t.continueReq(ev, false)
		return
	}

	t.mu.Lock()
	t.pending[ev.RequestID] = &pendingResponse{id: id, request: req}
	t.mu.Unlock()

	err = t.logger.InjectRequest(id, req, modifier.SourceBrowser)

	if err != nil {
		log.Printf("[!] Browser - InjectRequest: %s\n", err)
	}

	go t.continueReq(ev, true)
}

func (t *tabCapture) handleResponse(ev *fetch.EventRequestPaused) {
	t.mu.Lock()
	pend, ok := t.pending[ev.RequestID]
	if ok {
		delete(t.pending, ev.RequestID)
	}
	t.mu.Unlock()

	if !ok {
		go t.continueResp(ev)
		return
	}

	if ev.NetworkID != "" {
		t.muProtoUpdate.Lock()
		t.pendingProtoUpdate[ev.NetworkID] = pend.id
		t.muProtoUpdate.Unlock()
	}

	go func() {
		var body []byte

		// no response bodies for redirect responses
		if ev.ResponseStatusCode < 300 || ev.ResponseStatusCode > 399 {
			err := chromedp.Run(t.ctx, chromedp.ActionFunc(func(c context.Context) error {
				var err error
				body, err = fetch.GetResponseBody(ev.RequestID).Do(c)
				return err
			}))
			if err != nil {
				log.Printf("[!] Browser - GetResponseBody %s: %s\n", ev.Request.URL, err)
			}
		}

		resp, err := buildFetchResponse(ev, body)
		if err != nil {
			log.Printf("[!] Browser - buildFetchResponse: %s\n", err)
			go t.continueResp(ev)
			return
		}

		err = t.logger.InjectResponse(pend.id, resp)
		if err != nil {
			log.Printf("[!] Browser - InjectResponse: %s\n", err)
		}

		go t.continueResp(ev)
	}()
}

func parseProtocol(proto string) (major, minor int, display string) {
	switch proto {
	case "h2":
		return 2, 0, "HTTP/2.0"
	case "h3":
		return 3, 0, "HTTP/3.0"
	default:
		return 1, 1, "HTTP/1.1"
	}
}

type Capture struct {
	logger      *modifier.Logger
	allocCtx    context.Context
	allocCancel context.CancelFunc
	mainCtx     context.Context
	mainCancel  context.CancelFunc

	mu      sync.Mutex
	targets map[target.ID]bool
}

func NewCapture(logger *modifier.Logger) *Capture {
	return &Capture{logger: logger, targets: make(map[target.ID]bool)}
}

func (c *Capture) ConnectWS(wsURL string) error {
	if !strings.HasPrefix(wsURL, "ws://") && !strings.HasPrefix(wsURL, "wss://") {
		return fmt.Errorf("browser: CDP URL must be a WebSocket URL (ws:// or wss://), got %q", wsURL)
	}
	allocCtx, allocCancel := chromedp.NewRemoteAllocator(context.Background(), wsURL)
	ctx, cancel := chromedp.NewContext(allocCtx)
	c.allocCtx = allocCtx
	c.allocCancel = allocCancel
	c.mainCtx = ctx
	c.mainCancel = cancel
	return nil
}

func (c *Capture) Start() {
	mainTab := newTabCapture(c.mainCtx, c.mainCancel, c.logger)

	chromedp.ListenBrowser(c.mainCtx, func(ev any) {
		switch ev := ev.(type) {
		case *target.EventTargetCreated:
			if ev.TargetInfo.Type == "page" {
				go c.attachToTarget(ev.TargetInfo.TargetID)
			}
		}
	})

	go func() {
		err := chromedp.Run(mainTab.ctx)
		if err != nil {
			log.Printf("[!] Browser - init: %s\n", err)
			return
		}

		cctx := chromedp.FromContext(c.mainCtx)
		if cctx == nil || cctx.Browser == nil {
			return
		}

		// register the new tab prior to SetDiscoveryTargets to avoid duplicate entries
		c.mu.Lock()
		c.targets[cctx.Target.TargetID] = true
		c.mu.Unlock()

		chromedp.ListenTarget(c.mainCtx, mainTab.eventHandler)

		err = mainTab.enableFetch()
		if err != nil {
			log.Printf("[!] Browser - enableFetch: %s\n", err)
			return
		}

		browserExecutor := cdp.WithExecutor(c.mainCtx, cctx.Browser)

		// Calling SetDiscoverTargets(true) emits the EventTargetCreated callback
		// for all existing tabs and pages
		err = target.SetDiscoverTargets(true).Do(browserExecutor)
		if err != nil {
			log.Printf("[!] Browser - SetDiscoverTargets: %s\n", err)
		}

		<-c.mainCtx.Done()
	}()
}

func (c *Capture) attachToTarget(targetID target.ID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.targets[targetID] { // already attached
		return
	}

	c.targets[targetID] = true

	ctx, cancel := chromedp.NewContext(c.allocCtx, chromedp.WithTargetID(targetID))
	tab := newTabCapture(ctx, cancel, c.logger)
	err := chromedp.Run(tab.ctx)
	if err != nil {
		log.Printf("[!] Browser - init target %s: %s\n", targetID, err)
		cancel()
		return
	}

	chromedp.ListenTarget(ctx, tab.eventHandler)
	err = tab.enableFetch()
	if err != nil {
		log.Printf("[!] Browser - enableFetch %s: %s\n", targetID, err)
		cancel()
	}
}

func buildRequest(cdpReq *network.Request) (*http.Request, error) {
	var body io.Reader
	if len(cdpReq.PostDataEntries) > 0 {
		var sb strings.Builder
		for _, entry := range cdpReq.PostDataEntries {
			sb.WriteString(entry.Bytes)
		}
		body = strings.NewReader(sb.String())
	}

	req, err := http.NewRequest(cdpReq.Method, cdpReq.URL, body)
	if err != nil {
		return nil, err
	}

	for k, v := range cdpReq.Headers {
		switch val := v.(type) {
		case string:
			req.Header.Set(k, val)
		case float64:
			req.Header.Set(k, fmt.Sprintf("%v", val))
		}
	}

	if req.Host == "" {
		req.Host = req.URL.Host
	}

	return req, nil
}

func buildFetchResponse(ev *fetch.EventRequestPaused, body []byte) (*http.Response, error) {
	resp := &http.Response{
		StatusCode:    int(ev.ResponseStatusCode),
		Status:        ev.ResponseStatusText,
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Header:        make(http.Header),
	}

	for _, h := range ev.ResponseHeaders {
		resp.Header.Add(h.Name, h.Value)
	}

	return resp, nil
}
