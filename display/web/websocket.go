package web

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"
)

const wsMagicGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// wsConn wraps a hijacked net.Conn with the bufio.Reader from the HTTP
// server. Reads go through the buffered reader (so any bytes the HTTP
// server read ahead aren't lost), writes go directly to the connection.
// The write mutex serializes WS frame writes from concurrent goroutines
// (e.g. bitmap send loop + audio send loop).
type wsConn struct {
	net.Conn
	br *bufio.Reader
	wmu sync.Mutex
}

func (c *wsConn) Read(p []byte) (int, error) {
	return c.br.Read(p)
}

// upgradeWebSocket performs the HTTP WebSocket upgrade handshake.
// Returns a *wsConn that wraps the hijacked connection with the
// buffered reader from the HTTP server.
func upgradeWebSocket(w http.ResponseWriter, r *http.Request) (*wsConn, error) {
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		http.Error(w, "missing Sec-WebSocket-Key", http.StatusBadRequest)
		return nil, errors.New("missing Sec-WebSocket-Key")
	}

	// Compute accept key: SHA1(key + magic GUID) → base64
	h := sha1.New()
	h.Write([]byte(key))
	h.Write([]byte(wsMagicGUID))
	acceptKey := base64.StdEncoding.EncodeToString(h.Sum(nil))

	// Hijack the connection first
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "websocket: hijack not supported", http.StatusInternalServerError)
		return nil, errors.New("hijack not supported")
	}
	conn, bufrw, err := hj.Hijack()
	if err != nil {
		return nil, fmt.Errorf("hijack: %w", err)
	}

	// Clear any deadline the HTTP server may have set for header reading.
	conn.SetDeadline(time.Time{})

	// Flush any stale data the HTTP server may have buffered
	if bufrw.Writer.Buffered() > 0 {
		if err := bufrw.Flush(); err != nil {
			conn.Close()
			return nil, fmt.Errorf("flush stale: %w", err)
		}
	}

	// Write upgrade response directly to the raw TCP connection,
	// bypassing bufio.Writer entirely.
	response := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + acceptKey + "\r\n\r\n"
	if _, err := conn.Write([]byte(response)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("write upgrade: %w", err)
	}

	return &wsConn{Conn: conn, br: bufrw.Reader}, nil
}

// writeWSFrameDirect writes a WebSocket binary frame using a caller-provided
// buffer that has 10 bytes of header room before the payload. The buffer
// layout is: [10 bytes reserved][payload...]. The function writes the WS
// header into the reserved space and writes exactly hdr+payload in one call.
// This avoids allocating a frame buffer on every write.
func writeWSFrameDirect(w io.Writer, buf []byte, payloadLen int) error {
	// buf[0:10] is header room, buf[10:10+payloadLen] is payload.
	n := payloadLen
	var hdrLen int
	if n < 126 {
		hdrLen = 2
	} else if n < 65536 {
		hdrLen = 4
	} else {
		hdrLen = 10
	}

	// Write header into the reserved space just before the payload.
	hdrStart := 10 - hdrLen
	buf[hdrStart] = 0x82 // FIN + binary opcode
	switch hdrLen {
	case 2:
		buf[hdrStart+1] = byte(n)
	case 4:
		buf[hdrStart+1] = 126
		binary.BigEndian.PutUint16(buf[hdrStart+2:hdrStart+4], uint16(n))
	case 10:
		buf[hdrStart+1] = 127
		binary.BigEndian.PutUint64(buf[hdrStart+2:hdrStart+10], uint64(n))
	}

	_, err := w.Write(buf[hdrStart : 10+n])
	return err
}

// writeWSFrame writes a WebSocket binary frame. It allocates a temporary
// buffer — use writeWSFrameDirect for the zero-alloc hot path.
func writeWSFrame(w io.Writer, payload []byte) error {
	buf := make([]byte, 10+len(payload))
	copy(buf[10:], payload)
	return writeWSFrameDirect(w, buf, len(payload))
}

// lockedWriteWSFrameDirect is like writeWSFrameDirect but holds the wsConn
// write mutex, making it safe for concurrent use from multiple goroutines.
func lockedWriteWSFrameDirect(c *wsConn, buf []byte, payloadLen int) error {
	c.wmu.Lock()
	err := writeWSFrameDirect(c, buf, payloadLen)
	c.wmu.Unlock()
	return err
}

// lockedWriteWSFrame is like writeWSFrame but holds the wsConn write mutex.
func lockedWriteWSFrame(c *wsConn, payload []byte) error {
	c.wmu.Lock()
	err := writeWSFrame(c, payload)
	c.wmu.Unlock()
	return err
}

// readWSFrame reads a single WebSocket frame, unmasks the payload, and returns
// the payload bytes. Uses buf as scratch space if large enough, otherwise
// allocates. Returns io.EOF on close frame or connection close.
// Handles ping frames by sending pong responses automatically.
func readWSFrame(rw io.ReadWriter, buf []byte, log *slog.Logger) ([]byte, error) {
	for {
		// Read 2-byte header
		var hdr [2]byte
		_, err := io.ReadFull(rw, hdr[:])
		if err != nil {
			return nil, err
		}

		opcode := hdr[0] & 0x0F
		masked := hdr[1]&0x80 != 0
		payloadLen := uint64(hdr[1] & 0x7F)

		// Extended payload length
		switch payloadLen {
		case 126:
			var ext [2]byte
			if _, err := io.ReadFull(rw, ext[:]); err != nil {
				return nil, err
			}
			payloadLen = uint64(binary.BigEndian.Uint16(ext[:]))
		case 127:
			var ext [8]byte
			if _, err := io.ReadFull(rw, ext[:]); err != nil {
				return nil, err
			}
			payloadLen = binary.BigEndian.Uint64(ext[:])
		}

		// Read masking key
		var mask [4]byte
		if masked {
			if _, err := io.ReadFull(rw, mask[:]); err != nil {
				return nil, err
			}
		}

		// Read payload
		const maxWSPayload = 64 * 1024 * 1024 // 64 MB
		if payloadLen > maxWSPayload {
			return nil, fmt.Errorf("WebSocket payload too large: %d bytes", payloadLen)
		}
		pLen := int(payloadLen)
		payload := buf
		if len(payload) < pLen {
			payload = make([]byte, pLen)
		} else {
			payload = payload[:pLen]
		}
		if pLen > 0 {
			if _, err := io.ReadFull(rw, payload); err != nil {
				return nil, err
			}
		}

		// Unmask
		if masked {
			for i := range payload {
				payload[i] ^= mask[i%4]
			}
		}

		switch opcode {
		case 0x08: // Close
			log.Debug("received close frame", "payloadBytes", len(payload))
			return nil, io.EOF
		case 0x09: // Ping → respond with pong
			var pong [2]byte
			pong[0] = 0x8A // FIN + pong
			pong[1] = 0    // no payload
			rw.Write(pong[:])
			continue
		case 0x0A: // Pong — ignore
			continue
		default:
			return payload, nil
		}
	}
}
