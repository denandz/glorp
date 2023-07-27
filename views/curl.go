package views

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strings"
)

const (
	hextable = "0123456789abcdef"
)

// take a net.http request and turn it into a curl command
func reqToCurl(req *http.Request, url string) string {
	var curlCmd []string

	curlCmd = append(curlCmd, "curl")
	curlCmd = append(curlCmd, "-X", req.Method)

	for k := range req.Header {
		curlCmd = append(curlCmd, "-H", fmt.Sprintf("$'%s: %s'", k, strings.Join(req.Header[k], " ")))
	}

	curlCmd = append(curlCmd, fmt.Sprintf("$'%s'", hexEscapeString(url)))

	if req.Body != nil {
		var buf bytes.Buffer
		_, err := buf.ReadFrom(req.Body)
		if err != nil {
			log.Printf("[!] Error copy-as-curl ReadFrom body buffer %s\n", err)
			return ""
		}
		// reset body
		req.Body = ioutil.NopCloser(bytes.NewBuffer(buf.Bytes()))

		if len(buf.String()) > 0 {
			bodyEscaped := fmt.Sprintf("$'%s'", hexEscapeString(buf.String()))
			curlCmd = append(curlCmd, "--data-binary", bodyEscaped)
		}
	}

	return strings.Join(curlCmd, " ")
}

// hexEscapeString take an input string and replaces any non alphanumsymbol chars
// with their \x hex code. Specifically doing this with bytes, rather than runes
// single quotes are the exception, these get escaped to play nice with shell
// copy pastes
func hexEscapeString(s string) string {
	bytes := []byte(s)
	var out []byte

	for i := range bytes {
		if !isAsciiAlphaSymbolsSpace(bytes[i]) || bytes[i] == 0x27 {
			dst := make([]byte, 2)
			dst[0] = hextable[bytes[i]>>4]
			dst[1] = hextable[bytes[i]&0x0f]

			out = append(out, "\\x"...)
			out = append(out, dst...)
		} else {
			out = append(out, bytes[i])
		}
	}

	return string(out)
}

// Return true if the byte is an ascii a-z, A-Z, 0-9, symbol, or space
func isAsciiAlphaSymbolsSpace(c byte) bool {
	if ' ' <= c && c <= '~' {
		return true
	}

	return false
}
