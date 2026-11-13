package proxy

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/denandz/glorp/modifier"
)

// isWebSocketUpgrade reports whether the headers describe a WebSocket
// upgrade (request or 101 response headers).
func isWebSocketUpgrade(h http.Header) bool {
	connUpgrade := false
	for _, v := range h["Connection"] {
		for _, s := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(s), "upgrade") {
				connUpgrade = true
			}
		}
	}
	if !connUpgrade {
		return false
	}
	for _, v := range h["Upgrade"] {
		for _, s := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(s), "websocket") {
				return true
			}
		}
	}
	return false
}

// wsDisplayURL renders a request URL with a ws/wss scheme, like browser
// capture does for websocket handshakes.
func wsDisplayURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	var scheme string
	switch u.Scheme {
	case "http":
		scheme = "ws"
	case "https":
		scheme = "wss"
	default:
		return u.String()
	}
	clone := *u
	clone.Scheme = scheme
	return clone.String()
}

// wsFrameCodec sits between goproxy's websocket relay and the upstream
// connection. goproxy pipes raw bytes both ways; this codec decodes the
// RFC 6455 frames in each direction, logs every frame to the logger, and
// passes the bytes on:
//
// - Write (client -> upstream): client frames are masked; they are
//   decoded, logged, and re-encoded unmasked for the upstream
// - Read (upstream -> client): frames are decoded, logged, and the
//   original bytes forwarded to the client untouched
type wsFrameCodec struct {
	rw     io.ReadWriter
	logger *modifier.Logger
	url    string

	writeParser frameParser // buffered client -> upstream bytes
	readParser  frameParser // buffered upstream -> client bytes
	out         []byte      // upstream frame bytes pending delivery to the client
	in          [32 * 1024]byte
}

func newWSFrameCodec(rw io.ReadWriter, logger *modifier.Logger, u *url.URL) *wsFrameCodec {
	return &wsFrameCodec{rw: rw, logger: logger, url: wsDisplayURL(u)}
}

// frameParser incrementally parses websocket frames from a byte stream.
type frameParser struct {
	buf []byte
}

type wsFrame struct {
	firstByte byte // original first byte (FIN/RSV/opcode), preserved for re-encoding
	opcode    int
	masked    bool   // whether the frame carried a masking key on the wire
	payload   []byte // unmasked payload, owned copy
	raw       []byte // original wire bytes of the frame (including mask bits/key)
}

func (p *frameParser) push(b []byte) {
	p.buf = append(p.buf, b...)
}

// next returns the next complete frame in the buffer, or nil if the
// buffer does not yet hold a full frame.
func (p *frameParser) next() *wsFrame {
	b := p.buf
	if len(b) < 2 {
		return nil
	}
	opcode := int(b[0] & 0x0f)
	l := int(b[1] & 0x7f)
	off := 2
	switch l {
	case 126:
		if len(b) < 4 {
			return nil
		}
		l = int(binary.BigEndian.Uint16(b[2:4]))
		off = 4
	case 127:
		if len(b) < 10 {
			return nil
		}
		v := binary.BigEndian.Uint64(b[2:10])
		if v > 0x7fffffff {
			// implausibly large. refuse rather than allocate
			return nil
		}
		l = int(v)
		off = 10
	}
	masked := b[1]&0x80 != 0
	maskOff := off
	if masked {
		if len(b) < off+4 {
			return nil
		}
		maskOff = off + 4
	}
	if l < 0 || len(b) < maskOff+l {
		return nil
	}

	payload := make([]byte, l)
	copy(payload, b[maskOff:maskOff+l])
	if masked {
		key := b[off : off+4]
		for i := range payload {
			payload[i] ^= key[i%4]
		}
	}
	raw := make([]byte, maskOff+l)
	copy(raw, b[:maskOff+l])

	p.buf = b[maskOff+l:]
	return &wsFrame{firstByte: b[0], opcode: opcode, masked: masked, payload: payload, raw: raw}
}

// encodeFrame builds a websocket frame from a first byte (FIN/RSV/opcode)
// and payload. If masked, a fresh random masking key is applied - the
// proxy is a client to the upstream, and client-to-server frames MUST be
// masked (RFC 6455 section 5.1); strict servers close the connection
// otherwise.
func encodeFrame(masked bool, firstByte byte, payload []byte) []byte {
	l := len(payload)
	hdr := make([]byte, 2)
	switch {
	case l < 126:
		hdr[1] = byte(l)
	case l < 65536:
		hdr = append(hdr, 0, 0)
		hdr[1] = 126
		binary.BigEndian.PutUint16(hdr[2:], uint16(l))
	default:
		hdr = append(hdr, make([]byte, 8)...)
		hdr[1] = 127
		binary.BigEndian.PutUint64(hdr[2:], uint64(l))
	}
	hdr[0] = firstByte
	if !masked {
		return append(hdr, payload...)
	}
	key := make([]byte, 4)
	rand.Read(key)
	hdr[1] |= 0x80
	m := make([]byte, l)
	for i := range m {
		m[i] = payload[i] ^ key[i%4]
	}
	out := append(hdr, key...)
	return append(out, m...)
}

// Write receives raw client bytes (masked frames), logs each complete
// frame, and forwards masked frames to the upstream (the proxy is a
// client on this leg, so frames must stay masked per RFC 6455).
func (c *wsFrameCodec) Write(p []byte) (int, error) {
	c.writeParser.push(p)
	for {
		f := c.writeParser.next()
		if f == nil {
			break
		}
		c.logFrame("sent", f)
		if _, err := c.rw.Write(encodeFrame(true, f.firstByte, f.payload)); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// Read pulls bytes from the upstream, logs each complete frame, and
// returns the original frame bytes for the client.
func (c *wsFrameCodec) Read(p []byte) (int, error) {
	for len(c.out) == 0 {
		n, err := c.rw.Read(c.in[:])
		if n > 0 {
			c.readParser.push(c.in[:n])
			for {
				f := c.readParser.next()
				if f == nil {
					break
				}
				c.logFrame("received", f)
				c.out = append(c.out, f.raw...)
			}
		}
		if err != nil {
			if len(c.out) > 0 {
				break // deliver buffered frames before surfacing the error
			}
			return 0, err
		}
	}
	n := copy(p, c.out)
	c.out = c.out[n:]
	return n, nil
}

// Close closes the upstream connection.
func (c *wsFrameCodec) Close() error {
	if cl, ok := c.rw.(io.Closer); ok {
		return cl.Close()
	}
	return nil
}

// logFrame records one frame as a WebSocketEntry, matching the format
// used by browser (CDP) capture.
func (c *wsFrameCodec) logFrame(direction string, f *wsFrame) {
	c.logger.InjectWebSocketMessage(&modifier.WebSocketEntry{
		ID:        modifier.GenerateID(),
		URL:       c.url,
		Direction: direction,
		Timestamp: time.Now(),
		Opcode:    f.opcode,
		Payload:   base64.StdEncoding.EncodeToString(f.payload),
	})
}
