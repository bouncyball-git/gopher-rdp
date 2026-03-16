package web

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// dialWebSocket performs a manual WebSocket handshake to the given URL path
// on a test server, returning the raw connection for frame-level testing.
func dialWebSocket(t *testing.T, serverURL, path string) net.Conn {
	t.Helper()

	addr := strings.TrimPrefix(serverURL, "http://")
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	// Send upgrade request
	key := "dGhlIHNhbXBsZSBub25jZQ==" // fixed test key
	req := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n",
		path, addr, key)
	if _, err := conn.Write([]byte(req)); err != nil {
		conn.Close()
		t.Fatalf("write upgrade: %v", err)
	}

	// Read response
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		conn.Close()
		t.Fatalf("read response: %v", err)
	}
	if resp.StatusCode != 101 {
		conn.Close()
		t.Fatalf("expected 101, got %d", resp.StatusCode)
	}

	// Verify accept key
	h := sha1.New()
	h.Write([]byte(key))
	h.Write([]byte(wsMagicGUID))
	wantAccept := base64.StdEncoding.EncodeToString(h.Sum(nil))
	gotAccept := resp.Header.Get("Sec-WebSocket-Accept")
	if gotAccept != wantAccept {
		conn.Close()
		t.Fatalf("Sec-WebSocket-Accept = %q, want %q", gotAccept, wantAccept)
	}

	return conn
}

// sendClientFrame sends a masked binary WebSocket frame (as browsers do).
func sendClientFrame(conn net.Conn, payload []byte) error {
	n := len(payload)
	var hdr [14]byte // max: 10 header + 4 mask
	hdr[0] = 0x82 // FIN + binary

	var hdrLen int
	if n < 126 {
		hdr[1] = 0x80 | byte(n) // masked + length
		hdrLen = 2
	} else if n < 65536 {
		hdr[1] = 0x80 | 126
		binary.BigEndian.PutUint16(hdr[2:4], uint16(n))
		hdrLen = 4
	} else {
		hdr[1] = 0x80 | 127
		binary.BigEndian.PutUint64(hdr[2:10], uint64(n))
		hdrLen = 10
	}

	// Mask key (fixed for testing)
	mask := [4]byte{0x12, 0x34, 0x56, 0x78}
	copy(hdr[hdrLen:], mask[:])
	hdrLen += 4

	// Mask payload
	masked := make([]byte, n)
	for i, b := range payload {
		masked[i] = b ^ mask[i%4]
	}

	if _, err := conn.Write(hdr[:hdrLen]); err != nil {
		return err
	}
	_, err := conn.Write(masked)
	return err
}

func TestWebSocketUpgrade(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgradeWebSocket(w, r)
		if err != nil {
			t.Errorf("upgrade failed: %v", err)
			return
		}
		defer conn.Close()

		// Read one frame and echo it back
		buf := make([]byte, 1024)
		payload, err := readWSFrame(conn, buf, slog.New(slog.DiscardHandler))
		if err != nil {
			t.Errorf("readWSFrame: %v", err)
			return
		}
		if err := writeWSFrame(conn, payload); err != nil {
			t.Errorf("writeWSFrame: %v", err)
		}
	}))
	defer server.Close()

	conn := dialWebSocket(t, server.URL, "/")
	defer conn.Close()

	// Send "hello" as a masked binary frame
	if err := sendClientFrame(conn, []byte("hello")); err != nil {
		t.Fatalf("send: %v", err)
	}

	// Read the echoed frame (server sends unmasked)
	var hdr [2]byte
	if _, err := io.ReadFull(conn, hdr[:]); err != nil {
		t.Fatalf("read hdr: %v", err)
	}
	if hdr[0] != 0x82 {
		t.Fatalf("expected opcode 0x82, got 0x%02X", hdr[0])
	}
	pLen := int(hdr[1] & 0x7F)
	if pLen != 5 {
		t.Fatalf("expected payload length 5, got %d", pLen)
	}
	payload := make([]byte, pLen)
	if _, err := io.ReadFull(conn, payload); err != nil {
		t.Fatalf("read payload: %v", err)
	}
	if string(payload) != "hello" {
		t.Fatalf("got %q, want %q", payload, "hello")
	}
}

func TestWebSocketLargeFrame(t *testing.T) {
	// Test with a payload > 125 bytes (triggers 16-bit length encoding)
	testData := make([]byte, 300)
	for i := range testData {
		testData[i] = byte(i)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgradeWebSocket(w, r)
		if err != nil {
			return
		}
		defer conn.Close()

		buf := make([]byte, 1024)
		payload, err := readWSFrame(conn, buf, slog.New(slog.DiscardHandler))
		if err != nil {
			t.Errorf("readWSFrame: %v", err)
			return
		}
		writeWSFrame(conn, payload)
	}))
	defer server.Close()

	conn := dialWebSocket(t, server.URL, "/")
	defer conn.Close()

	if err := sendClientFrame(conn, testData); err != nil {
		t.Fatalf("send: %v", err)
	}

	// Read response header
	var hdr [4]byte
	if _, err := io.ReadFull(conn, hdr[:]); err != nil {
		t.Fatalf("read hdr: %v", err)
	}
	if hdr[0] != 0x82 {
		t.Fatalf("expected opcode 0x82, got 0x%02X", hdr[0])
	}
	if hdr[1] != 126 {
		t.Fatalf("expected 16-bit length marker (126), got %d", hdr[1])
	}
	pLen := int(binary.BigEndian.Uint16(hdr[2:4]))
	if pLen != 300 {
		t.Fatalf("expected payload length 300, got %d", pLen)
	}
	payload := make([]byte, pLen)
	if _, err := io.ReadFull(conn, payload); err != nil {
		t.Fatalf("read payload: %v", err)
	}
	for i, b := range payload {
		if b != byte(i) {
			t.Fatalf("mismatch at byte %d: got %d, want %d", i, b, byte(i))
		}
	}
}

func TestWebSocketWithServeMux(t *testing.T) {
	// Mimics the real setup: ServeMux with "/" and "/ws" handlers
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello"))
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgradeWebSocket(w, r)
		if err != nil {
			t.Errorf("upgrade failed: %v", err)
			return
		}
		defer conn.Close()

		buf := make([]byte, 1024)
		payload, err := readWSFrame(conn, buf, slog.New(slog.DiscardHandler))
		if err != nil {
			t.Errorf("readWSFrame: %v", err)
			return
		}
		writeWSFrame(conn, payload)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	conn := dialWebSocket(t, server.URL, "/ws")
	defer conn.Close()

	// Send "test" as a masked binary frame
	if err := sendClientFrame(conn, []byte("test")); err != nil {
		t.Fatalf("send: %v", err)
	}

	// Read the echoed frame
	var hdr [2]byte
	if _, err := io.ReadFull(conn, hdr[:]); err != nil {
		t.Fatalf("read hdr: %v", err)
	}
	if hdr[0] != 0x82 {
		t.Fatalf("expected opcode 0x82, got 0x%02X", hdr[0])
	}
	pLen := int(hdr[1] & 0x7F)
	payload := make([]byte, pLen)
	if _, err := io.ReadFull(conn, payload); err != nil {
		t.Fatalf("read payload: %v", err)
	}
	if string(payload) != "test" {
		t.Fatalf("got %q, want %q", payload, "test")
	}
}

// TestWebSocketHTTPKeepAlive tests WebSocket upgrade on a connection that
// previously served a regular HTTP request (HTTP keep-alive reuse).
func TestWebSocketHTTPKeepAlive(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("page"))
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgradeWebSocket(w, r)
		if err != nil {
			t.Errorf("upgrade failed: %v", err)
			return
		}
		defer conn.Close()

		buf := make([]byte, 1024)
		payload, err := readWSFrame(conn, buf, slog.New(slog.DiscardHandler))
		if err != nil {
			t.Errorf("readWSFrame: %v", err)
			return
		}
		writeWSFrame(conn, payload)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	// Open a single TCP connection and do TWO requests:
	// 1. GET / (regular HTTP)
	// 2. GET /ws (WebSocket upgrade) on the SAME connection
	addr := strings.TrimPrefix(server.URL, "http://")
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Request 1: regular HTTP GET /
	httpReq := fmt.Sprintf("GET / HTTP/1.1\r\nHost: %s\r\nConnection: keep-alive\r\n\r\n", addr)
	if _, err := conn.Write([]byte(httpReq)); err != nil {
		t.Fatalf("write HTTP req: %v", err)
	}

	// Read HTTP response (consume it fully)
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read HTTP resp: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "page" {
		t.Fatalf("HTTP body = %q, want %q", body, "page")
	}

	// Request 2: WebSocket upgrade on the SAME connection
	key := "dGhlIHNhbXBsZSBub25jZQ=="
	wsReq := fmt.Sprintf("GET /ws HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n",
		addr, key)
	if _, err := conn.Write([]byte(wsReq)); err != nil {
		t.Fatalf("write WS req: %v", err)
	}

	// Read 101 response
	resp2, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read WS resp: %v", err)
	}
	if resp2.StatusCode != 101 {
		t.Fatalf("expected 101, got %d", resp2.StatusCode)
	}

	// Send a WebSocket frame (must use br for reads since it may have buffered data)
	if err := sendClientFrame(conn, []byte("keepalive")); err != nil {
		t.Fatalf("send: %v", err)
	}

	// Read echoed frame through the buffered reader
	var hdr [2]byte
	if _, err := io.ReadFull(br, hdr[:]); err != nil {
		t.Fatalf("read hdr: %v (buffered=%d)", err, br.Buffered())
	}
	if hdr[0] != 0x82 {
		t.Fatalf("expected opcode 0x82, got 0x%02X", hdr[0])
	}
	pLen := int(hdr[1] & 0x7F)
	payload := make([]byte, pLen)
	if _, err := io.ReadFull(br, payload); err != nil {
		t.Fatalf("read payload: %v", err)
	}
	if string(payload) != "keepalive" {
		t.Fatalf("got %q, want %q", payload, "keepalive")
	}
}

func TestWebSocketCloseFrame(t *testing.T) {
	closeSeen := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgradeWebSocket(w, r)
		if err != nil {
			return
		}
		defer conn.Close()

		buf := make([]byte, 1024)
		_, err = readWSFrame(conn, buf, slog.New(slog.DiscardHandler))
		if err == io.EOF {
			close(closeSeen)
			return
		}
		if err != nil {
			t.Errorf("readWSFrame: unexpected error: %v", err)
		}
	}))
	defer server.Close()

	conn := dialWebSocket(t, server.URL, "/")
	defer conn.Close()

	// Send a masked close frame: opcode=0x08, status code 1000
	var frame [8]byte
	frame[0] = 0x88                                  // FIN + close
	frame[1] = 0x82                                  // masked, 2 bytes payload
	mask := [4]byte{0xAA, 0xBB, 0xCC, 0xDD}
	copy(frame[2:6], mask[:])
	// Close code 1000 (normal closure) = 0x03E8, masked
	frame[6] = 0x03 ^ mask[0]
	frame[7] = 0xE8 ^ mask[1]

	if _, err := conn.Write(frame[:]); err != nil {
		t.Fatalf("send close: %v", err)
	}

	<-closeSeen
}
