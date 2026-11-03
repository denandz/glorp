package proxy

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/denandz/glorp/modifier"
)

func startTestLogger(t *testing.T) *modifier.Logger {
	t.Helper()
	proxychan := make(chan modifier.Notification, 64)
	sitemapchan := make(chan modifier.Notification, 64)
	wschan := make(chan modifier.Notification, 64)
	l := modifier.NewLogger(nil, proxychan, sitemapchan, wschan)
	go func() {
		for range proxychan {
		}
	}()
	go func() {
		for range sitemapchan {
		}
	}()
	go func() {
		for range wschan {
		}
	}()
	return l
}

func TestProxySmoke(t *testing.T) {
	logger := startTestLogger(t)

	p := StartProxy(logger, &Config{Addr: "127.0.0.1", Port: 18473})
	if p == nil {
		t.Fatal("StartProxy returned nil")
	}

	// plain http backend
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "plain-hello")
	}))
	defer plain.Close()

	// tls backend with h2 support
	tlsSrv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "tls-hello %s", r.Proto)
	}))
	tlsSrv.TLS = &tls.Config{NextProtos: []string{"h2", "http/1.1"}}
	tlsSrv.StartTLS()
	defer tlsSrv.Close()

	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(mustURL("http://127.0.0.1:18473")),
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			ForceAttemptHTTP2: true,
		},
	}

	get := func(url string) (int, string, string) {
		resp, err := client.Get(url)
		if err != nil {
			t.Fatalf("GET %s: %v", url, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b), resp.Proto
	}

	code, body, _ := get(plain.URL)
	if code != 200 || body != "plain-hello" {
		t.Fatalf("plain: got %d %q", code, body)
	}

	code, body, proto := get(tlsSrv.URL)
	t.Logf("tls via MITM: status=%d proto=%s body=%q", code, proto, body)
	if code != 200 {
		t.Fatalf("tls: got %d %q", code, body)
	}
	if proto != "HTTP/2.0" {
		t.Fatalf("expected HTTP/2 through MITM, got %s", proto)
	}

	// give the notification channels a moment, then check entries were recorded
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(logger.GetEntries()) >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	entries := logger.GetEntries()
	t.Logf("recorded %d entries", len(entries))
	if len(entries) < 2 {
		t.Fatalf("expected >=2 entries, got %d", len(entries))
	}
	for id, e := range entries {
		t.Logf("entry %s: %s %s -> %d (%s)", id, e.Request.Method, e.Request.URL, e.Response.Status, e.Request.HTTPVersion)
		if e.Response == nil || e.Request == nil {
			t.Fatalf("entry %s incomplete", id)
		}
	}
}

func TestProxyUpstream(t *testing.T) {
	// upstream proxy
	upLogger := startTestLogger(t)
	StartProxy(upLogger, &Config{Addr: "127.0.0.1", Port: 18474})

	// downstream proxy pointing at the upstream
	downLogger := startTestLogger(t)
	StartProxy(downLogger, &Config{Addr: "127.0.0.1", Port: 18475, Proxy: "http://127.0.0.1:18474"})

	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "chained-hello")
	}))
	defer plain.Close()

	tlsSrv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "chained-tls-hello")
	}))
	tlsSrv.TLS = &tls.Config{NextProtos: []string{"h2", "http/1.1"}}
	tlsSrv.StartTLS()
	defer tlsSrv.Close()

	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(mustURL("http://127.0.0.1:18475")),
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	for _, u := range []string{plain.URL, tlsSrv.URL} {
		resp, err := client.Get(u)
		if err != nil {
			t.Fatalf("GET %s: %v", u, err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || len(b) == 0 {
			t.Fatalf("GET %s: got %d %q", u, resp.StatusCode, b)
		}
		t.Logf("chained %s: %d %q", u, resp.StatusCode, b)
	}
}

func mustURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}
