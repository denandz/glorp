package replay

import (
	"bytes"
	"crypto/tls"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"time"
)

// Request - main struct that holds replay request/response data
type Request struct {
	ID           string // The ID as displayed in the table
	Host         string // the destination host
	Port         string // the destination port
	TLS          bool   // does the destination expect TLS
	RawRequest   []byte // the raw body
	RawResponse  []byte // the raw body
	ResponseTime string // the time it took to recieve the response
}

// SendRequest - takes a destination host, port and ssl boolean. Fires the request and writes the
// response into an array
func (r *Request) SendRequest() (bool, error) {
	var buf bytes.Buffer
	log.Printf("[+] Replay - SendRequest Host: %s Port: %s TLS:  %t\n", r.Host, r.Port, r.TLS)

	port, err := strconv.Atoi(r.Port)
	if err != nil {
		log.Printf("[!] Replay - Error in port atoi: %s\n", err)
		return false, err
	}

	start := time.Now()
	if !r.TLS {
		buf = sendTCP(r.Host, port, r.RawRequest)
	} else {
		buf = sendTLS(r.Host, port, r.RawRequest)
	}

	if buf.Len() > 0 {
		r.RawResponse = buf.Bytes()
		r.ResponseTime = time.Since(start).String()
	}

	return true, nil
}

// UpdateContentLength - try and update the content length in a raw request to match the body length
func (r *Request) UpdateContentLength() {
	clheader := "\r\nContent-Length: "

	crlf := bytes.Index(r.RawRequest, []byte{0x0d, 0x0a, 0x0d, 0x0a})
	if crlf == -1 {
		return
	}

	bodyLength := strconv.Itoa(len(r.RawRequest) - (crlf + 4))

	// find the first content-length header -- this should probably be case insensitve? Though someone
	// setting a spongebob ConTenT-LenGTh probably doesn't want us messing with their payload
	contentLengthHeader := bytes.Index(r.RawRequest, []byte(clheader))

	if contentLengthHeader == -1 {
		return
	}

	clEOL := bytes.Index(r.RawRequest[contentLengthHeader+len(clheader):], []byte{0x0d, 0x0a})
	if clEOL == -1 {
		return // we have a content length header but no CRLF anywhere after it...
	}

	if clEOL == len(bodyLength) {
		// updated length and OG length use the same number of bytes, overwrite...
		copy(r.RawRequest[contentLengthHeader+len(clheader):contentLengthHeader+len(clheader)+clEOL], []byte(bodyLength))
	} else if clEOL != len(bodyLength) {
		newSlice := make([]byte, 0) //len(r.RawRequest)+len(bodyLength)-clEOL)
		newSlice = append(newSlice, r.RawRequest[:contentLengthHeader+len(clheader)]...)
		newSlice = append(newSlice, []byte(bodyLength)...)
		newSlice = append(newSlice, r.RawRequest[contentLengthHeader+len(clheader)+clEOL:]...)
		r.RawRequest = newSlice
	}
}

func sendTCP(host string, port int, packet []byte) bytes.Buffer {
	var buf bytes.Buffer

	addr := strings.Join([]string{host, strconv.Itoa(port)}, ":")
	conn, err := net.Dial("tcp", addr)

	if err != nil {
		log.Println(err)
		return buf
	}
	defer conn.Close()

	l, err := conn.Write(packet)
	if err != nil {
		log.Println(err)
		return buf
	}

	log.Printf("[+] Replay - sendTCP - Sent: %d\n", l)

	io.Copy(&buf, conn)
	log.Printf("[+] Replay - sendTCP - Received: %d", buf.Len())

	return buf
}

func sendTLS(host string, port int, packet []byte) bytes.Buffer {
	var buf bytes.Buffer

	addr := strings.Join([]string{host, strconv.Itoa(port)}, ":")
	conn, err := tls.Dial("tcp", addr, nil)

	if err != nil {
		log.Println(err)
		return buf
	}

	defer conn.Close()

	l, err := conn.Write(packet)
	if err != nil {
		log.Println(err)
		return buf
	}

	log.Printf("[+] Replay - sendTLS - Sent: %d\n", l)

	io.Copy(&buf, conn)
	log.Printf("[+] Replay - sendTLS - Received: %d\n", buf.Len())

	return buf
}
