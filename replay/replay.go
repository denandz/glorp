package replay

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"os"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/imroc/req/v3"
)

// Protocol constants for the Protocol field.
const (
	ProtoHTTP1 = "HTTP/1.1"
	ProtoHTTP2 = "HTTP/2"
	ProtoHTTP3 = "HTTP/3"
	ProtoAuto  = "Auto"
)

// Protocols is the ordered list of selectable protocol options.
var Protocols = []string{ProtoAuto, ProtoHTTP1, ProtoHTTP2, ProtoHTTP3}

// Fingerprint constants for the Fingerprint field.
// "Impersonate" options spoof both TLS and HTTP/2 frame characteristics.
// "TLS:" options spoof only the TLS ClientHello.
const (
	FingerprintNone         = ""
	FingerprintChrome       = "Impersonate: Chrome"
	FingerprintFirefox      = "Impersonate: Firefox"
	FingerprintSafari       = "Impersonate: Safari"
	FingerprintTLSChrome    = "TLS: Chrome"
	FingerprintTLSFirefox   = "TLS: Firefox"
	FingerprintTLSEdge      = "TLS: Edge"
	FingerprintTLSSafari    = "TLS: Safari"
	FingerprintTLSIOS       = "TLS: iOS"
	FingerprintTLSAndroid   = "TLS: Android"
	FingerprintTLSRandomized = "TLS: Randomized"
)

// Fingerprints is the ordered list of selectable fingerprint options shown in the UI.
// The empty string entry represents "no fingerprint spoofing".
var Fingerprints = []string{
	FingerprintNone,
	FingerprintChrome,
	FingerprintFirefox,
	FingerprintSafari,
	FingerprintTLSChrome,
	FingerprintTLSFirefox,
	FingerprintTLSEdge,
	FingerprintTLSSafari,
	FingerprintTLSIOS,
	FingerprintTLSAndroid,
	FingerprintTLSRandomized,
}

// Request - main struct that holds replay request/response data
type Request struct {
	ID          string        // The ID as displayed in the table
	Host        string        // the destination host
	Port        string        // the destination port
	TLS         bool          // does the destination expect TLS
	Protocol    string        // HTTP protocol version: HTTP/1.1, HTTP/2, HTTP/3
	Fingerprint string        // TLS/HTTP fingerprint impersonation (see Fingerprints slice)
	ProxyURL    string        // downstream proxy URL to use when sending (e.g. http://127.0.0.1:8080)
	RawRequest  []byte // the request to send
	RawResponse []byte        // raw HTTP response bytes
	ResponseTime string       // the time it took to receive the response

	ExternalFile *os.File          `json:"-"` // external file currently used to update the request
	Watcher      *fsnotify.Watcher `json:"-"` // watcher for external file updates
}

// SendRequest fires the stored Request at Host:Port (optionally over TLS)
// using the configured Protocol and Fingerprint. ProxyURL, if set, is honoured.
// The raw response is written into RawResponse.
func (r *Request) SendRequest() (int, error) {
	log.Printf("[+] Replay - SendRequest Host: %s Port: %s TLS: %t Protocol: %s Fingerprint: %q\n",
		r.Host, r.Port, r.TLS, r.Protocol, r.Fingerprint)

        httpReq, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(r.RawRequest)))
	if err != nil {
		log.Printf("[!] Replay - Send Request: %s\n", err)
		return 0, nil
	}

	// Reconstruct the full URL
	scheme := "http"
	if r.TLS {
		scheme = "https"
	}

	// Only append the port when it is non-standard
	var targetHost string
	if (scheme == "http" && r.Port == "80") || (scheme == "https" && r.Port == "443") {
		targetHost = r.Host
	} else {
		targetHost = fmt.Sprintf("%s:%s", r.Host, r.Port)
	}

	rawPath := httpReq.RequestURI
	if rawPath == "" {
		rawPath = "/"
	}

	fullURL := fmt.Sprintf("%s://%s%s", scheme, targetHost, rawPath)

	client := req.C().Clone().
		SetTLSClientConfig(&tls.Config{InsecureSkipVerify: true}).
		SetRedirectPolicy(req.NoRedirectPolicy()).
		SetTimeout(30 * time.Second)

	// Apply fingerprint impersonation first so that an explicit protocol
	// choice below can still override the version set by the impersonator.
	switch r.Fingerprint {
	case FingerprintChrome:
		client.ImpersonateChrome()
	case FingerprintFirefox:
		client.ImpersonateFirefox()
	case FingerprintSafari:
		client.ImpersonateSafari()
	case FingerprintTLSChrome:
		client.SetTLSFingerprintChrome()
	case FingerprintTLSFirefox:
		client.SetTLSFingerprintFirefox()
	case FingerprintTLSEdge:
		client.SetTLSFingerprintEdge()
	case FingerprintTLSSafari:
		client.SetTLSFingerprintSafari()
	case FingerprintTLSIOS:
		client.SetTLSFingerprintIOS()
	case FingerprintTLSAndroid:
		client.SetTLSFingerprintAndroid()
	case FingerprintTLSRandomized:
		client.SetTLSFingerprintRandomized()
	}

	// Apply protocol constraint
	switch r.Protocol {
	case ProtoHTTP1:
		client.EnableForceHTTP1()
	case ProtoHTTP2:
		client.EnableForceHTTP2()
	case ProtoHTTP3:
		client.EnableForceHTTP3()
	case ProtoAuto:
		// No constraint; let the library negotiate
	default:
		// Handle raw protocol values from HTTPRequest.Proto (e.g. "HTTP/2.0")
		switch r.Protocol {
		case "HTTP/2.0":
			client.EnableForceHTTP2()
		default:
			// No constraint; let the library negotiate
		}
	}

	// Honour the downstream proxy if one is configured
	if r.ProxyURL != "" {
		client.SetProxyURL(r.ProxyURL)
	}

	reqObj := client.R()

	// Forward headers
	for name, vals := range httpReq.Header {
		for _, v := range vals {
			reqObj.SetHeader(name, v)
		}
	}

	// Forward body
	if httpReq.Body != nil {
		body, err := io.ReadAll(httpReq.Body)
		httpReq.Body.Close()
		// Restore the body so it can be read again (e.g. on re-display)
		httpReq.Body = io.NopCloser(bytes.NewReader(body))
		if err != nil {
			log.Printf("[!] Replay - SendRequest: failed to read body: %s\n", err)
			return 0, err
		}
		if len(body) > 0 {
			reqObj.SetBodyBytes(body)
		}
	}

	start := time.Now()

	resp, err := reqObj.Send(strings.ToUpper(httpReq.Method), fullURL)
	if err != nil {
		if resp == nil || resp.Response == nil {
			log.Printf("[!] Replay - SendRequest: %s\n", err)
			return 0, err
		}
		// NoRedirectPolicy surfaces 3xx as errors; capture the response anyway
		log.Printf("[+] Replay - SendRequest: non-nil error (likely redirect): %s\n", err)
	}

	elapsed := time.Since(start)

	raw, dumpErr := httputil.DumpResponse(resp.Response, true)
	if dumpErr != nil {
		log.Printf("[!] Replay - SendRequest: failed to dump response: %s\n", dumpErr)
		return 0, dumpErr
	}

	r.RawResponse = raw
	r.ResponseTime = elapsed.String()

	log.Printf("[+] Replay - SendRequest - Received: %d bytes in %s\n", len(raw), elapsed)

	return len(raw), nil
}

// Copy - return a deep copy of a replay entry
func (r *Request) Copy() Request {
	replayData := Request{}

	replayData = *r

	replayData.ExternalFile = nil
	replayData.Watcher = nil
	replayData.RawRequest = make([]byte, len(r.RawRequest))
	replayData.RawResponse = make([]byte, len(r.RawResponse))
	copy(replayData.RawRequest, r.RawRequest)
	copy(replayData.RawResponse, r.RawResponse)

	return replayData
}
