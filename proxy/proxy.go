package proxy

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/denandz/glorp/modifier"
	"github.com/elazarl/goproxy"
)

// Config - struct that holds the proxy config
type Config struct {
	Port  uint   // port to listen on, default 8080
	Addr  string // ip address to listen on, default 0.0.0.0
	Proxy string // downstream proxy to use, optional.

	Cert string // CA certificate
	Key  string // key
}

func (config *Config) checkConfig() {
	if config.Port == 0 {
		config.Port = 8080
	}

	if config.Addr == "" {
		config.Addr = "0.0.0.0"
	}

}

// certCache - implements goproxy.CertStorage, caching MITM certificates per host
type certCache struct {
	mu    sync.Mutex
	certs map[string]*tls.Certificate
}

func newCertCache() *certCache {
	return &certCache{certs: make(map[string]*tls.Certificate)}
}

func (c *certCache) Fetch(hostname string, gen func() (*tls.Certificate, error)) (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if cert, ok := c.certs[hostname]; ok {
		return cert, nil
	}

	cert, err := gen()
	if err != nil {
		return nil, err
	}

	c.certs[hostname] = cert
	return cert, nil
}

// newAuthority generates a self-signed CA certificate and key, used for MITM
// when no CA is provided in the config
func newAuthority(commonName string, ttl time.Duration) (*tls.Certificate, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}

	template := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName, Organization: []string{"glorp.proxy"}},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(ttl),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}

	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}

// StartProxy - Starts the goproxy proxy and sets up the modifier. Nil config will set some reasonable defaults
func StartProxy(logger *modifier.Logger, config *Config) *goproxy.ProxyHttpServer {
	if config == nil {
		config = new(Config)
	}

	config.checkConfig()

	p := goproxy.NewProxyHttpServer()

	// do not cannonicalize headers per usual net/http behaviour
	p.PreventCanonicalization = true

	// route goproxy's internal messages (ctx.Warnf & co) through the
	// global logger so they land in the Log page instead of raw stderr
	p.Logger = log.Default()

	// allow clients to speak HTTP/2 (extended CONNECT / h2 MITM), alongside HTTP/1.1
	p.AllowHTTP2 = true

	tr := &http.Transport{
		Dial: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).Dial,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
		DisableCompression: true,
		// attempt HTTP/2 (with automatic fallback to HTTP/1.1) on upstream connections
		ForceAttemptHTTP2: true,
	}
	p.Tr = tr

	if config.Proxy != "" {
		proxyURL, err := url.Parse(config.Proxy)
		if err != nil {
			log.Printf("goproxy: error parsing upstream proxy URL: %v, skipping proxy\n", err)
		} else if proxyURL.Scheme == "socks5" {
			log.Printf("goproxy: socks5 upstream proxies are not supported, skipping proxy\n")
		} else {
			// route CONNECT tunnels and plain HTTP requests through the upstream proxy
			if dial := p.NewConnectDialToProxy(config.Proxy); dial != nil {
				p.ConnectDial = dial
			}
			p.Tr.Proxy = http.ProxyURL(proxyURL)
		}
	}

	var ca *tls.Certificate
	var err error

	if config.Cert != "" && config.Key != "" {
		tlsc, err := tls.LoadX509KeyPair(config.Cert, config.Key)
		if err != nil {
			log.Fatal(err)
		}
		ca = &tlsc
	} else {
		ca, err = newAuthority("glorp.proxy", 30*24*time.Hour)
		if err != nil {
			log.Fatal(err)
		}
	}

	// MITM all CONNECT tunnels, signing per-host certificates with our CA
	p.CertStore = newCertCache()
	p.OnRequest().HandleConnect(goproxy.FuncHttpsHandler(func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
		return &goproxy.ConnectAction{
			Action:    goproxy.ConnectMitm,
			TLSConfig: goproxy.TLSConfigFromCA(ca),
		}, host
	}))

	p.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		// assign the entry ID here and stash it in the context so the
		// response handler logs the same ID for the same request/response pair
		id := modifier.GenerateID()
		ctx.UserData = id
		if err := logger.ModifyRequest(id, req); err != nil {
			log.Printf("goproxy: error logging request: %v\n", err)
		}
		return req, nil
	})

	p.OnResponse().DoFunc(func(res *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
		id, _ := ctx.UserData.(string)
		if id == "" {
			id = modifier.GenerateID()
		}
		if err := logger.ModifyResponse(id, res); err != nil {
			log.Printf("goproxy: error logging response: %v\n", err)
		}
		return res
	})

	l, err := net.Listen("tcp", config.Addr+":"+strconv.FormatInt(int64(config.Port), 10))
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("goproxy: starting proxy on %s\n", l.Addr().String())

	go http.Serve(l, p)

	return p
}
