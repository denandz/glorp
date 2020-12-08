package modifier

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"sync"
	"time"

	"github.com/rivo/tview"

	"github.com/google/martian"
)

// Logger maintains request and response log entries.
type Logger struct {
	mu               sync.Mutex
	entries          Entries
	notificationchan chan Notification
	app              *tview.Application
	table            *tview.Table
}

// Notification channel struct. Holds the element ID and an int for request or response
type Notification struct {
	ID        string
	NotifType int // 0 == request, 1 == response, 2 == request and response (used by save/load)
}

// Entries stores all the Entry items
type Entries map[string]*Entry

// Entry is a individual log entry for a request or response.
type Entry struct {
	// ID is the unique ID for the entry.
	ID string `json:"_id"`
	// StartedDateTime is the date and time stamp of the request start (ISO 8601).
	StartedDateTime time.Time `json:"startedDateTime"`
	// Time is the total elapsed time of the request in milliseconds.
	Time int64 `json:"time"`
	// Request contains the detailed information about the request.
	Request *Request `json:"request"`
	// Response contains the detailed information about the response.
	Response *Response `json:"response,omitempty"`
}

// Request holds data about an individual HTTP request.
type Request struct {
	// Method is the request method (GET, POST, ...).
	Method string `json:"method"`
	// URL is the absolute URL of the request (fragments are not included).
	URL string `json:"url"`
	// HTTPVersion is the Request HTTP version (HTTP/1.1).
	HTTPVersion string `json:"httpVersion"`
	// BodySize is the size of the request body (POST data payload) in bytes. Set
	// to -1 if the info is not available.
	BodySize int64 `json:"bodySize"`

	Raw  []byte // the raw body
	Host string
	TLS  bool
}

// Response holds data about an individual HTTP response.
type Response struct {
	// Status is the response status code.
	Status int `json:"status"`
	// StatusText is the response status description.
	StatusText string `json:"statusText"`
	// HTTPVersion is the Response HTTP version (HTTP/1.1).
	HTTPVersion string `json:"httpVersion"`
	// RedirectURL is the target URL from the Location response header.
	RedirectURL string `json:"redirectURL"`
	// Headers stores the response headers
	Headers http.Header `json:"headers"`
	// BodySize is the size of the request body (POST data payload) in bytes. Set
	// to -1 if the info is not available.
	BodySize int64 `json:"bodySize"`

	Raw []byte // the raw body
}

// NewLogger returns a HAR logger. The returned
// logger logs all request post data and response bodies by default.
func NewLogger(app *tview.Application, notifchan chan Notification, table *tview.Table) *Logger {
	l := &Logger{
		entries:          make(map[string]*Entry),
		app:              app,
		table:            table,
		notificationchan: notifchan,
	}
	return l
}

// ModifyRequest logs requests.
func (l *Logger) ModifyRequest(req *http.Request) error {
	ctx := martian.NewContext(req)
	if ctx.SkippingLogging() {
		return nil
	}

	// don't track CONNECTS
	if req.Method == http.MethodConnect {
		return nil
	}

	id := ctx.ID()

	e := l.RecordRequest(id, req)

	l.mu.Lock()
	defer l.mu.Unlock()

	l.notificationchan <- Notification{id, 0}

	return e
}

// RecordRequest logs the HTTP request with the given ID. The ID should be unique
// per request/response pair.
func (l *Logger) RecordRequest(id string, req *http.Request) error {
	hreq, err := NewRequest(req)
	if err != nil {
		return err
	}

	entry := &Entry{
		ID:              id,
		StartedDateTime: time.Now().UTC(),
		Request:         hreq,
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if _, exists := l.entries[id]; exists {
		return fmt.Errorf("Duplicate request ID: %s", id)
	}
	l.entries[id] = entry

	return nil
}

// NewRequest constructs and returns a Request from req. An error
// is returned (and req.Body may be in an intermediate state) if an error is
// returned from req.Body.Read.
func NewRequest(req *http.Request) (*Request, error) {
	r := &Request{
		Method:      req.Method,
		URL:         req.URL.String(),
		HTTPVersion: req.Proto,
		BodySize:    req.ContentLength,
		Host:        req.URL.Host,
	}

	raw, err := httputil.DumpRequestOut(req, true)
	if err != nil {
		return nil, err
	}
	r.Raw = raw

	return r, nil
}

// ModifyResponse logs responses.
func (l *Logger) ModifyResponse(res *http.Response) error {
	ctx := martian.NewContext(res.Request)
	id := ctx.ID()

	e := l.RecordResponse(id, res)

	l.notificationchan <- Notification{id, 1}

	return e
}

// RecordResponse logs an HTTP response, associating it with the previously-logged
// HTTP request with the same ID.
func (l *Logger) RecordResponse(id string, res *http.Response) error {
	hres, err := NewResponse(res)
	if err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if e, ok := l.entries[id]; ok {
		e.Response = hres
		e.Time = time.Since(e.StartedDateTime).Nanoseconds() / 1000000
	}

	return nil
}

// NewResponse constructs and returns a Response from resp.
func NewResponse(res *http.Response) (*Response, error) {
	r := &Response{
		HTTPVersion: res.Proto,
		Status:      res.StatusCode,
		StatusText:  http.StatusText(res.StatusCode),
		BodySize:    res.ContentLength,
		Headers:     res.Header.Clone(),
	}

	raw, err := httputil.DumpResponse(res, true)
	if err != nil {
		return nil, err
	}

	r.Raw = raw

	if res.StatusCode >= 300 && res.StatusCode < 400 {
		r.RedirectURL = res.Header.Get("Location")
	}

	return r, nil
}

// GetEntry - Get a specific entry by ID
func (l *Logger) GetEntry(id string) *Entry {
	var e *Entry

	// should check it exists here
	e = l.entries[id]

	return e
}

// GetEntries - return the Entries map
func (l *Logger) GetEntries() map[string]*Entry {
	return l.entries
}

// AddEntry - manually add an entry
func (l *Logger) AddEntry(e Entry) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if e.ID != "" {
		l.entries[e.ID] = &e
	}
}

// Reset clears the in-memory log of entries.
func (l *Logger) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.entries = make(map[string]*Entry)
}
