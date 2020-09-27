package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"glorp/modifier"
	"log"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/google/martian"
	"github.com/google/martian/fifo"
	"github.com/google/martian/mitm"
)

// Config - struct that holds the proxy config
type Config struct {
	Port uint   // port to listen on, default 8080
	Addr string // ip address to listen on, default 0.0.0.0

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

// StartProxy - Starts the martian proxy and sets up the modifier. Nil config will set some reasonable defaults
func StartProxy(logger *modifier.Logger, config *Config) *martian.Proxy {
	if config == nil {
		config = new(Config)
	}

	config.checkConfig()

	p := martian.NewProxy()

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
	}
	p.SetRoundTripper(tr)

	var x509c *x509.Certificate
	var priv interface{}
	var err error

	if config.Cert != "" && config.Key != "" {
		tlsc, err := tls.LoadX509KeyPair(config.Cert, config.Key)
		if err != nil {
			log.Fatal(err)
		}
		priv = tlsc.PrivateKey

		x509c, err = x509.ParseCertificate(tlsc.Certificate[0])
		if err != nil {
			log.Fatal(err)
		}
	} else {
		x509c, priv, err = mitm.NewAuthority("martian.proxy", "Martian Authority", 30*24*time.Hour)
		if err != nil {
			log.Fatal(err)
		}
	}

	if x509c != nil && priv != nil {
		mc, err := mitm.NewConfig(x509c, priv)
		if err != nil {
			log.Fatal(err)
		}

		mc.SkipTLSVerify(true)

		p.SetMITM(mc)
	}

	topg := fifo.NewGroup()

	topg.AddRequestModifier(logger)
	topg.AddResponseModifier(logger)

	p.SetRequestModifier(topg)
	p.SetResponseModifier(topg)

	l, err := net.Listen("tcp", config.Addr+":"+strconv.FormatInt(int64(config.Port), 10))
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("martian: starting proxy on %s\n", l.Addr().String())

	go p.Serve(l)

	return p
}
