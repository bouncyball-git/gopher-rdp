package web

import (
	"embed"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	rdp "github.com/bouncyball-git/gopher-rdp"
	"github.com/bouncyball-git/gopher-rdp/display"
	"github.com/bouncyball-git/gopher-rdp/protocol/audin"
	"github.com/bouncyball-git/gopher-rdp/protocol/egfx"
	"github.com/bouncyball-git/gopher-rdp/protocol/rdpsnd"
)

//go:embed index.html
var indexHTML embed.FS

// Server→browser message type prefixes.
const (
	wsMsgBitmap     byte = 0x01
	wsMsgCursor     byte = 0x02
	wsMsgDisconnect byte = 0x03
	wsMsgResize     byte = 0x04 // server confirms resize: [0x04][w:u16 LE][h:u16 LE]
	wsMsgClipboard  byte = 0x05 // clipboard text: [0x05][utf8 text...]
	wsMsgAudio      byte = 0x06 // audio PCM: [0x06][channels:u16 LE][rate:u32 LE][bps:u16 LE][pcm data...]
	wsMsgAudioInput      byte = 0x07 // audio input: server→client format notification or client→server PCM
	wsMsgClipboardImage  byte = 0x09 // clipboard image: [0x09][png bytes...]
	wsMsgH264            byte = 0x0A // H.264 frame: [0x0A][surfId:u16][mode:u8][destRect:8][regions...][nalData...]
	wsMsgCameraCtrl      byte = 0x0B // server→browser: camera control [0x0B][subtype][params...]
	wsMsgCameraData      byte = 0x0C // browser→server: camera H.264 [0x0C][nalData...]
)

// Cursor subtypes (second byte after wsMsgCursor).
const (
	cursorNull    byte = 0x00
	cursorDefault byte = 0x01
	cursorShape   byte = 0x02
	cursorCached  byte = 0x03
)

// NewWebHandler returns an http.Handler that serves an HTML viewer page
// at "/" and a WebSocket endpoint at "/ws" for streaming bitmap updates
// and receiving keyboard/mouse input. Uses a pre-connected RDP client.
func NewWebHandler(client *rdp.Client, logger *slog.Logger, width, height int, kbMode rdp.KeyboardMode) http.Handler {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	log := logger.With("component", "WEB")
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		// Redirect to include dimensions if not present
		if r.URL.Query().Get("w") == "" {
			kb := "scancode"
			if kbMode == rdp.KeyboardUnicode {
				kb = "unicode"
			}
			http.Redirect(w, r, fmt.Sprintf("/?w=%d&h=%d&kb=%s", width, height, kb), http.StatusFound)
			return
		}
		data, _ := indexHTML.ReadFile("index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Write(data)
	})

	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		handleWS(w, r, client, log, width, height, kbMode, 0)
	})

	return mux
}

// NewAutoWebHandler returns an http.Handler that defers the RDP connection
// until the browser reports its physical resolution via the WebSocket URL.
// The browser detects screen.availWidth * devicePixelRatio and passes it
// as ?w=X&h=Y on the WebSocket connection. The first WebSocket connection
// triggers the RDP connection at those dimensions.
// The returned channel is closed when the RDP session ends.
func NewAutoWebHandler(opts *rdp.Options) (http.Handler, <-chan struct{}) {
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	log := logger.With("component", "WEB")

	mux := http.NewServeMux()
	sessionDone := make(chan struct{})
	var sessionOnce sync.Once

	var (
		client    *rdp.Client
		clientW   int
		clientH   int
		clientMu  sync.Mutex
		clientErr error
	)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		// Redirect to include keyboard mode param if not present
		if r.URL.Query().Get("kb") == "" {
			kb := "scancode"
			if opts.KeyboardMode == rdp.KeyboardUnicode {
				kb = "unicode"
			}
			abuf := 0
			if opts.AudioOut != nil {
				abuf = opts.AudioOut.BufMs
			}
			url := fmt.Sprintf("/?kb=%s&abuf=%d", kb, abuf)
			if opts.NoBilinear {
				url += "&nearest=1"
			}
			if opts.NoDPR {
				url += "&nodpr=1"
			}
			http.Redirect(w, r, url, http.StatusFound)
			return
		}
		data, _ := indexHTML.ReadFile("index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Write(data)
	})

	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		// Read browser resolution from WS URL params
		wStr := r.URL.Query().Get("w")
		hStr := r.URL.Query().Get("h")
		bw, _ := strconv.Atoi(wStr)
		bh, _ := strconv.Atoi(hStr)
		if bw <= 0 || bh <= 0 {
			bw, bh = int(opts.Width), int(opts.Height)
		}

		// Optional DPI scaling (MS-RDPBCGR Client Core Data desktopScaleFactor /
		// deviceScaleFactor). When ?scale=N is present, override opts so the
		// initial RDP connection negotiates the requested DPI. Validation against
		// the allowed value list is performed by rdp.NewClient().
		scaleStr := r.URL.Query().Get("scale")
		devScaleStr := r.URL.Query().Get("devscale")

		clientMu.Lock()
		if client == nil && clientErr == nil {
			// First connection — connect RDP at browser resolution
			opts.Width = uint16(bw)
			opts.Height = uint16(bh)
			if scaleStr != "" {
				if v, err := strconv.Atoi(scaleStr); err == nil && v > 0 {
					opts.DesktopScaleFactor = uint32(v)
				}
			}
			if devScaleStr != "" {
				if v, err := strconv.Atoi(devScaleStr); err == nil && v > 0 {
					opts.DeviceScaleFactor = uint32(v)
				}
			}
			log.Debug("browser resolution, connecting RDP", "width", bw, "height", bh,
				"desktopScale", opts.DesktopScaleFactor, "deviceScale", opts.DeviceScaleFactor)
			c, err := rdp.NewClient(opts)
			if err != nil {
				clientErr = err
				clientMu.Unlock()
				log.Error("failed to create RDP client", "error", err)
				http.Error(w, "RDP client error", http.StatusInternalServerError)
				return
			}
			// Enable H.264 pass-through if the browser supports WebCodecs
			// and NoAVC is not set. Must register before Connect() so the
			// EGFX handler advertises AVC in CAPS_ADVERTISE.
			if r.URL.Query().Get("avc") == "1" && !opts.NoAVC {
				c.OnH264Frame(func(*egfx.H264Frame) {})
			}
			if err := c.Connect(); err != nil {
				clientErr = err
				clientMu.Unlock()
				log.Error("RDP connection failed", "error", err)
				http.Error(w, "RDP connection failed", http.StatusBadGateway)
				return
			}
			client = c
			clientW = bw
			clientH = bh
			log.Info("RDP connected", "width", bw, "height", bh)
			go func() {
				<-c.Done()
				sessionOnce.Do(func() { close(sessionDone) })
			}()
		}
		if clientErr != nil {
			clientMu.Unlock()
			http.Error(w, "RDP connection failed", http.StatusBadGateway)
			return
		}
		c, cw, ch := client, clientW, clientH
		clientMu.Unlock()

		aiBuf := 0
		if opts.AudioIn != nil {
			aiBuf = opts.AudioIn.BufMs
		}
		handleWS(w, r, c, log, cw, ch, opts.KeyboardMode, aiBuf)
	})

	return mux, sessionDone
}

// bitmapMsg is a bitmap update with its own copy of the pixel data,
// safe to send across goroutines (the original Data may reference
// the client's reusable decompression buffer).
type bitmapMsg struct {
	X, Y          int
	Width, Height int
	BitsPerPixel  int
	TopDown       bool
	Data          []byte
}

// cursorMsg is a cursor update safe to send across goroutines.
type cursorMsg struct {
	Type       byte
	CacheIndex uint16
	HotSpotX   uint16
	HotSpotY   uint16
	Width      uint16
	Height     uint16
	Data       []byte // owned RGBA copy (PointerShape only)
}

func handleWS(w http.ResponseWriter, r *http.Request, client *rdp.Client, log *slog.Logger, width, height int, kbMode rdp.KeyboardMode, audioInputBufMs int) {
	conn, err := upgradeWebSocket(w, r)
	if err != nil {
		log.Error("WebSocket upgrade failed", "error", err)
		return
	}
	defer conn.Close()
	log.Info("WebSocket connected", "remote", r.RemoteAddr)

	// Request full screen redraw so the new viewer gets the current desktop.
	if err := client.RefreshRect(0, 0, width, height); err != nil {
		log.Debug("RefreshRect failed", "error", err)
	}

	// Channel for bitmap update batches. Each entry is a frame's worth of
	// rects (or a single out-of-frame rect). Batching per frame prevents
	// channel overflow during heavy repaints (resize, initial desktop).
	avcMode := r.URL.Query().Get("avc") == "1"

	bitmapCh := make(chan []bitmapMsg, 512)
	cursorCh := make(chan cursorMsg, 64)
	clipCh := make(chan string, 4)
	clipImageCh := make(chan []byte, 4)
	audioCh := make(chan []byte, 32) // pre-built WS frames with 10-byte header room
	h264Ch := make(chan []byte, 64)  // pre-built WS frames with 10-byte header room
	var closeOnce sync.Once
	done := make(chan struct{})

	// Frame-level batching: accumulate bitmap rects during an EGFX frame
	// flush and send them as a single channel entry at EndPaint.
	var frameBatch []bitmapMsg
	var h264Batch [][]byte // pre-built WS frames for H.264, batched during paint
	var inPaint bool
	var dropped int

	sendBatch := func(batch []bitmapMsg) {
		select {
		case bitmapCh <- batch:
		default:
			dropped += len(batch)
			if dropped%100 < len(batch) {
				log.Warn("dropped bitmap updates", "count", dropped)
			}
		}
	}

	client.OnBeginPaint(func() {
		inPaint = true
		frameBatch = frameBatch[:0]
		h264Batch = h264Batch[:0]
	})
	client.OnEndPaint(func() {
		inPaint = false
		if len(frameBatch) > 0 {
			// Send the batch — make a copy of the slice header so the
			// backing array can be reused for the next frame.
			batch := make([]bitmapMsg, len(frameBatch))
			copy(batch, frameBatch)
			sendBatch(batch)
		}
		for _, buf := range h264Batch {
			select {
			case h264Ch <- buf:
			default:
				log.Debug("H.264 frame dropped, channel full")
			}
		}
	})

	// Strided callback for GFX pipeline updates — extract contiguous copy
	// directly from surface data, skipping the intermediate callbackBuf.
	client.OnStridedBitmap(func(x, y, w, h int, data []byte, stride int) {
		dstStride := w * 4
		dataCopy := make([]byte, dstStride*h)
		for row := range h {
			copy(dataCopy[row*dstStride:row*dstStride+dstStride], data[row*stride:row*stride+dstStride])
		}
		msg := bitmapMsg{
			X:            x,
			Y:            y,
			Width:        w,
			Height:       h,
			BitsPerPixel: 32,
			TopDown:      true,
			Data:         dataCopy,
		}
		if inPaint {
			frameBatch = append(frameBatch, msg)
		} else {
			sendBatch([]bitmapMsg{msg})
		}
	})

	// Legacy callback for non-GFX bitmap updates (orders, bitmap cache).
	client.OnBitmap(func(u *rdp.BitmapUpdate) {
		dataCopy := make([]byte, len(u.Data))
		copy(dataCopy, u.Data)
		msg := bitmapMsg{
			X:            u.X,
			Y:            u.Y,
			Width:        u.Width,
			Height:       u.Height,
			BitsPerPixel: u.BitsPerPixel,
			TopDown:      u.TopDown,
			Data:         dataCopy,
		}
		if inPaint {
			frameBatch = append(frameBatch, msg)
		} else {
			sendBatch([]bitmapMsg{msg})
		}
	})

	// Register pointer callback — Data is already an owned copy from client.go.
	client.OnPointer(func(u *rdp.PointerUpdate) {
		msg := cursorMsg{
			Type:       u.Type,
			CacheIndex: u.CacheIndex,
			HotSpotX:   u.HotSpotX,
			HotSpotY:   u.HotSpotY,
			Width:      u.Width,
			Height:     u.Height,
			Data:       u.Data,
		}
		select {
		case cursorCh <- msg:
		default:
			// cursor updates are less frequent; dropping is acceptable
		}
	})

	// Register clipboard callbacks — bridge remote↔browser clipboard.
	client.OnClipboardUpdate(func(hasText, hasImage bool) {
		if hasText {
			client.RequestClipboard()
		}
		if hasImage {
			client.RequestClipboardImage()
		}
	})
	client.OnClipboardText(func(text string) {
		select {
		case clipCh <- text:
		default:
		}
	})
	client.OnClipboardImage(func(pngData []byte) {
		select {
		case clipImageCh <- pngData:
		default:
		}
	})

	// Register audio callback — build WS frame directly to avoid double-copy.
	// Layout: [10 WS hdr room][0x06][channels:u16][rate:u32][bps:u16][pcm...]
	client.OnAudioData(func(s *rdpsnd.AudioSample) {
		pcmLen := len(s.Data)
		log.Debug("audio output to browser", "bytes", pcmLen, "rate", s.Format.SamplesPerSec, "channels", s.Format.Channels, "bps", s.Format.BitsPerSample)
		payloadLen := 9 + pcmLen
		buf := make([]byte, 10+payloadLen)
		buf[10] = wsMsgAudio
		binary.LittleEndian.PutUint16(buf[11:13], s.Format.Channels)
		binary.LittleEndian.PutUint32(buf[13:17], s.Format.SamplesPerSec)
		binary.LittleEndian.PutUint16(buf[17:19], s.Format.BitsPerSample)
		copy(buf[19:], s.Data)
		select {
		case audioCh <- buf:
		default:
			log.Debug("audio output dropped, channel full")
		}
	})

	// Register audio input callbacks — forward format negotiation to browser.
	// Silence fill is handled by the client layer to keep the DVC alive.

	sendAudioInputFormat := func(f audin.AudioFormat) {
		var buf [11]byte
		buf[0] = wsMsgAudioInput
		binary.LittleEndian.PutUint16(buf[1:3], f.Channels)
		binary.LittleEndian.PutUint32(buf[3:7], f.SamplesPerSec)
		binary.LittleEndian.PutUint16(buf[7:9], f.BitsPerSample)
		binary.LittleEndian.PutUint16(buf[9:11], uint16(audioInputBufMs))
		lockedWriteWSFrame(conn, buf[:])
		log.Info("audio input open, notified browser", "channels", f.Channels, "rate", f.SamplesPerSec, "bps", f.BitsPerSample, "bufMs", audioInputBufMs)
	}
	client.OnAudioInputOpen(func(f audin.AudioFormat) {
		sendAudioInputFormat(f)
	})
	client.OnAudioInputClose(func() {
		var buf [11]byte
		buf[0] = wsMsgAudioInput
		lockedWriteWSFrame(conn, buf[:])
		log.Info("audio input closed, notified browser")
	})

	// If audio input is already recording (server opened it before browser
	// connected), send the format now so the browser can start mic capture.
	if f, ok := client.AudioInputFormat(); ok {
		sendAudioInputFormat(f)
	}

	// Register H.264 pass-through callback when browser supports WebCodecs.
	if avcMode {
		client.OnH264Frame(func(f *egfx.H264Frame) {
			// Build WS frame: [10 hdr room][0x0A][surfId:u16][mode:u8][left:u16][top:u16][right:u16][bottom:u16]
			//   [numRegions:u16][N × {left:u16,top:u16,right:u16,bottom:u16,qp:u8,quality:u8}][nalData...]
			hdrLen := 1 + 2 + 1 + 8 + 2 + len(f.Regions)*10
			payloadLen := hdrLen + len(f.NALData)
			buf := make([]byte, 10+payloadLen)
			off := 10
			buf[off] = wsMsgH264
			binary.LittleEndian.PutUint16(buf[off+1:], f.SurfaceID)
			buf[off+3] = f.CodecMode
			binary.LittleEndian.PutUint16(buf[off+4:], uint16(f.Left))
			binary.LittleEndian.PutUint16(buf[off+6:], uint16(f.Top))
			binary.LittleEndian.PutUint16(buf[off+8:], uint16(f.Right))
			binary.LittleEndian.PutUint16(buf[off+10:], uint16(f.Bottom))
			binary.LittleEndian.PutUint16(buf[off+12:], uint16(len(f.Regions)))
			roff := off + 14
			for _, reg := range f.Regions {
				binary.LittleEndian.PutUint16(buf[roff:], reg.Left)
				binary.LittleEndian.PutUint16(buf[roff+2:], reg.Top)
				binary.LittleEndian.PutUint16(buf[roff+4:], reg.Right)
				binary.LittleEndian.PutUint16(buf[roff+6:], reg.Bottom)
				buf[roff+8] = reg.QPVal
				buf[roff+9] = reg.QualityVal
				roff += 10
			}
			copy(buf[roff:], f.NALData)
			if inPaint {
				h264Batch = append(h264Batch, buf)
			} else {
				select {
				case h264Ch <- buf:
				default:
					log.Debug("H.264 frame dropped, channel full")
				}
			}
		})
	}

	// Register RDPECAM webcam callbacks — notify browser to start/stop capture.
	if cam := client.CameraHandler(); cam != nil {
		cam.OnStartCapture(func(width, height, fps int) {
			// Send camera start: [0x0B][0x01][w:u16][h:u16][fps:u8]
			var buf [8]byte
			buf[0] = wsMsgCameraCtrl
			buf[1] = 0x01 // start
			binary.LittleEndian.PutUint16(buf[2:4], uint16(width))
			binary.LittleEndian.PutUint16(buf[4:6], uint16(height))
			binary.LittleEndian.PutUint16(buf[6:8], uint16(fps))
			lockedWriteWSFrame(conn, buf[:])
			log.Info("camera start sent to browser", "width", width, "height", height, "fps", fps)
		})
		cam.OnStopCapture(func() {
			// Send camera stop: [0x0B][0x02]
			lockedWriteWSFrame(conn, []byte{wsMsgCameraCtrl, 0x02})
			log.Info("camera stop sent to browser")
		})
	}

	// Register resize callback — notify browser of confirmed server resize.
	client.OnResize(func(newW, newH int) {
		var buf [5]byte
		buf[0] = wsMsgResize
		binary.LittleEndian.PutUint16(buf[1:3], uint16(newW))
		binary.LittleEndian.PutUint16(buf[3:5], uint16(newH))
		lockedWriteWSFrame(conn, buf[:])
		// Request full redraw at new resolution
		if err := client.RefreshRect(0, 0, newW, newH); err != nil {
			log.Debug("RefreshRect after resize failed", "error", err)
		}
	})

	// Reusable frame buffer for batched bitmap messages.
	// Grows as needed; initial size covers one full-screen rect.
	initPixels := width * height
	if width > 0 && initPixels/width != height {
		initPixels = 0 // overflow
	}
	wsBuf := make([]byte, 10+1+8+initPixels*4)

	// Disconnect watcher: when RDP connection ends, notify browser and tear down.
	go func() {
		select {
		case <-client.Done():
			log.Info("RDP disconnected, notifying browser")
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			lockedWriteWSFrame(conn, []byte{wsMsgDisconnect})
			conn.Close()
			closeOnce.Do(func() { close(done) })
		case <-done:
		}
	}()

	// Audio send loop: dedicated goroutine so audio is never blocked behind
	// bitmaps. Uses locked writes to serialize with the bitmap send loop.
	go func() {
		defer closeOnce.Do(func() { close(done) })
		for {
			select {
			case abuf := <-audioCh:
				if err := lockedWriteWSFrameDirect(conn, abuf, len(abuf)-10); err != nil {
					log.Error("WebSocket audio write error", "error", err)
					return
				}
			case <-done:
				return
			}
		}
	}()

	// H.264 send loop: dedicated goroutine for low-latency H.264 delivery.
	go func() {
		defer closeOnce.Do(func() { close(done) })
		for {
			select {
			case buf := <-h264Ch:
				if err := lockedWriteWSFrameDirect(conn, buf, len(buf)-10); err != nil {
					log.Error("WebSocket H.264 write error", "error", err)
					return
				}
			case <-done:
				return
			}
		}
	}()

	// Send loop: bitmap/cursor/clipboard → WebSocket
	go func() {
		defer closeOnce.Do(func() { close(done) })
		for {
			select {
			case batch, ok := <-bitmapCh:
				if !ok {
					return
				}
				// Pack all rects from this frame batch into a single WS message
				// so the browser paints them atomically in one onmessage handler.
				// Format: [0x01][x:u16][y:u16][w:u16][h:u16][rgba...]... (repeated)
				payloadLen := 0
				for _, u := range batch {
					if u.Width <= 0 || u.Height <= 0 || u.Width > 32768 || u.Height > 32768 {
						continue
					}
					payloadLen += 1 + 8 + u.Width*u.Height*4
				}
				totalNeeded := 10 + payloadLen
				if cap(wsBuf) < totalNeeded {
					wsBuf = make([]byte, totalNeeded)
				}
				fb := wsBuf[:totalNeeded]
				off := 10 // skip WS header room
				for _, u := range batch {
					if u.Width <= 0 || u.Height <= 0 || u.Width > 32768 || u.Height > 32768 {
						continue
					}
					needed := u.Width * u.Height * 4
					fb[off] = wsMsgBitmap
					binary.LittleEndian.PutUint16(fb[off+1:off+3], uint16(u.X))
					binary.LittleEndian.PutUint16(fb[off+3:off+5], uint16(u.Y))
					binary.LittleEndian.PutUint16(fb[off+5:off+7], uint16(u.Width))
					binary.LittleEndian.PutUint16(fb[off+7:off+9], uint16(u.Height))
					pixDst := fb[off+9 : off+9+needed]
					if u.BitsPerPixel == 32 && u.TopDown && len(u.Data) >= needed {
						copy(pixDst, u.Data[:needed])
					} else if len(u.Data) >= u.Width*u.Height*(u.BitsPerPixel/8) {
						display.ConvertToRGBA(pixDst, u.Data, u.Width, u.Height, u.BitsPerPixel, u.TopDown)
					}
					off += 9 + needed
				}
				if err := lockedWriteWSFrameDirect(conn, fb, payloadLen); err != nil {
					log.Error("WebSocket write error", "error", err)
					return
				}

			case cm := <-cursorCh:
				if err := lockedSendCursorMsg(conn, &cm); err != nil {
					log.Error("WebSocket cursor write error", "error", err)
					return
				}

			case text := <-clipCh:
				msg := make([]byte, 1+len(text))
				msg[0] = wsMsgClipboard
				copy(msg[1:], text)
				if err := lockedWriteWSFrame(conn, msg); err != nil {
					log.Error("WebSocket clipboard write error", "error", err)
					return
				}

			case pngData := <-clipImageCh:
				msg := make([]byte, 1+len(pngData))
				msg[0] = wsMsgClipboardImage
				copy(msg[1:], pngData)
				if err := lockedWriteWSFrame(conn, msg); err != nil {
					log.Error("WebSocket clipboard image write error", "error", err)
					return
				}

			case <-done:
				return
			}
		}
	}()

	// Recv loop: WebSocket → keyboard/mouse input
	readBuf := make([]byte, 65536) // large enough for mic PCM chunks
	for {
		payload, err := readWSFrame(conn, readBuf, log)
		if err != nil {
			closeOnce.Do(func() { close(done) })
			log.Info("WebSocket disconnected, closing RDP session")
			client.Close()
			return
		}

		if len(payload) < 1 {
			continue
		}

		// Stop forwarding input after RDP disconnect
		select {
		case <-client.Done():
			continue
		default:
		}

		switch payload[0] {
		case 0x01: // Keyboard: [0x01][scancode:u16 LE][flags:u16 LE]
			if len(payload) < 5 {
				continue
			}
			scancode := binary.LittleEndian.Uint16(payload[1:3])
			flags := binary.LittleEndian.Uint16(payload[3:5])
			pressed := flags&0x8000 == 0 // KBDFLAG_RELEASE = 0x8000
			if err := client.SendKeyboard(scancode, pressed); err != nil {
				log.Debug("SendKeyboard error", "error", err)
			}

		case 0x02: // Mouse: [0x02][x:u16 LE][y:u16 LE][buttons:u16 LE]
			if len(payload) < 7 {
				continue
			}
			x := int(binary.LittleEndian.Uint16(payload[1:3]))
			y := int(binary.LittleEndian.Uint16(payload[3:5]))
			buttons := binary.LittleEndian.Uint16(payload[5:7])
			if err := client.SendMouse(x, y, buttons); err != nil {
				log.Debug("SendMouse error", "error", err)
			}

		case 0x03: // Wheel: [0x03][x:u16 LE][y:u16 LE][delta:i16 LE][horiz:u8]
			if len(payload) < 8 {
				continue
			}
			x := int(binary.LittleEndian.Uint16(payload[1:3]))
			y := int(binary.LittleEndian.Uint16(payload[3:5]))
			delta := int(int16(binary.LittleEndian.Uint16(payload[5:7])))
			horizontal := payload[7] != 0
			if err := client.SendMouseWheel(x, y, delta, horizontal); err != nil {
				log.Debug("SendMouseWheel error", "error", err)
			}

		case 0x04: // Resize: [0x04][w:u16 LE][h:u16 LE]
			if len(payload) < 5 {
				continue
			}
			newW := int(binary.LittleEndian.Uint16(payload[1:3]))
			newH := int(binary.LittleEndian.Uint16(payload[3:5]))
			log.Info("resize request", "width", newW, "height", newH)
			if err := client.Resize(newW, newH); err != nil {
				log.Error("Resize failed", "error", err)
			}

		case 0x05: // Clipboard text: [0x05][utf8 text...]
			if len(payload) < 2 {
				continue
			}
			text := string(payload[1:])
			if err := client.SetClipboard(text); err != nil {
				log.Error("SetClipboard failed", "error", err)
			}

		case 0x09: // Clipboard image: [0x09][png bytes...]
			if len(payload) < 2 {
				continue
			}
			if err := client.SetClipboardImage(payload[1:]); err != nil {
				log.Error("SetClipboardImage failed", "error", err)
			}

		case 0x06: // Unicode keyboard: [0x06][codepoint:u16 LE][flags:u16 LE]
			if len(payload) < 5 {
				continue
			}
			codepoint := binary.LittleEndian.Uint16(payload[1:3])
			flags := binary.LittleEndian.Uint16(payload[3:5])
			pressed := flags&0x8000 == 0
			if err := client.SendUnicode(codepoint, pressed); err != nil {
				log.Debug("SendUnicode error", "error", err)
			}

		case 0x07: // Audio input PCM: [0x07][srcRate:u32 LE][srcCh:u16 LE][pcm S16LE...]
			const micHdr = 1 + 4 + 2 // type + rate + channels
			if len(payload) < micHdr+2 {
				continue
			}
			srcRate := binary.LittleEndian.Uint32(payload[1:5])
			srcCh := binary.LittleEndian.Uint16(payload[5:7])
			pcm := payload[micHdr:]
			log.Debug("mic data from browser", "bytes", len(pcm), "srcRate", srcRate, "srcCh", srcCh)

			// Resample to server's active format if needed
			if f, ok := client.AudioInputFormat(); ok {
				if srcRate != f.SamplesPerSec || srcCh != f.Channels || 16 != f.BitsPerSample {
					pcm = ResamplePCM(pcm,
						PCMFormat{Rate: srcRate, Channels: srcCh, Bits: 16},
						PCMFormat{Rate: f.SamplesPerSec, Channels: f.Channels, Bits: f.BitsPerSample},
					)
					if pcm == nil {
						continue
					}
				}
			}

			if err := client.SendAudioInput(pcm); err != nil {
				log.Debug("SendAudioInput error", "error", err)
			}

		case 0x0C: // Camera H.264 data: [0x0C][nalData...]
			if len(payload) < 2 {
				continue
			}
			client.SendCameraSample(payload[1:])

		case 0x0D: // JS log: [0x0D][utf8 text...]
			if len(payload) > 1 {
				log.Info("JS: " + string(payload[1:]))
			}

		default:
			log.Warn("unknown input type", "type", fmt.Sprintf("0x%02X", payload[0]))
		}
	}
}

// lockedSendCursorMsg writes a cursor update WS message under the write mutex.
func lockedSendCursorMsg(conn *wsConn, cm *cursorMsg) error {
	switch cm.Type {
	case rdp.PointerNull:
		return lockedWriteWSFrame(conn, []byte{wsMsgCursor, cursorNull})
	case rdp.PointerDefault:
		return lockedWriteWSFrame(conn, []byte{wsMsgCursor, cursorDefault})
	case rdp.PointerCached:
		var buf [4]byte
		buf[0] = wsMsgCursor
		buf[1] = cursorCached
		binary.LittleEndian.PutUint16(buf[2:4], cm.CacheIndex)
		return lockedWriteWSFrame(conn, buf[:])
	case rdp.PointerShape:
		hdrLen := 2 + 2 + 2 + 2 + 2 + 2
		msg := make([]byte, hdrLen+len(cm.Data))
		msg[0] = wsMsgCursor
		msg[1] = cursorShape
		binary.LittleEndian.PutUint16(msg[2:4], cm.CacheIndex)
		binary.LittleEndian.PutUint16(msg[4:6], cm.HotSpotX)
		binary.LittleEndian.PutUint16(msg[6:8], cm.HotSpotY)
		binary.LittleEndian.PutUint16(msg[8:10], cm.Width)
		binary.LittleEndian.PutUint16(msg[10:12], cm.Height)
		copy(msg[12:], cm.Data)
		return lockedWriteWSFrame(conn, msg)
	}
	return nil
}

// NewMultiMonitorHandler returns an http.Handler for multi-monitor web viewing.
// Each monitor is served at /monitor/{N} and connects via /ws?monitor=N.
// The landing page at "/" lists all monitors with links.
func NewMultiMonitorHandler(d *Dispatcher) http.Handler {
	kbMode := d.KBMode()

	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("w") != "" || r.URL.Query().Get("monitor") != "" {
			// Serve index.html for viewer (redirected from /monitor/N)
			data, _ := indexHTML.ReadFile("index.html")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(data)
			return
		}
		// Landing page listing monitors (read live state for auto-detect status).
		monitors := d.Monitors()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!DOCTYPE html><html><head><title>gopher-rdp (multi monitor)</title>
<style>body{background:#1a1a2e;color:#cc7000;font:18px sans-serif;padding:40px;}
a{color:#cc7000;}h1{margin-bottom:20px;}
.disconnect{margin-top:20px;color:#cc4400;}</style></head><body>
<h1>gopher-rdp</h1><ul>`)
		for _, m := range monitors {
			res := fmt.Sprintf("%dx%d", m.Width, m.Height)
			if m.Width == 0 {
				res = "auto-detect"
			}
			label := fmt.Sprintf("Display %d &mdash; %s", m.Index, res)
			if m.Primary {
				label += " (primary)"
			}
			fmt.Fprintf(w, `<li><a href="/monitor/%d" target="_blank">%s</a></li>`, m.Index, label)
		}
		fmt.Fprintf(w, `</ul>
<p class="disconnect"><a href="/disconnect">Disconnect</a></p>
<script>
var proto=location.protocol==="https:"?"wss:":"ws:";
var ws=new WebSocket(proto+"//"+location.host+"/ws/control");
ws.onclose=function(){window.close();};
</script></body></html>`)
	})

	mux.HandleFunc("/monitor/", func(w http.ResponseWriter, r *http.Request) {
		monitors := d.Monitors()
		idxStr := r.URL.Path[len("/monitor/"):]
		idx, err := strconv.Atoi(idxStr)
		if err != nil || idx < 0 || idx >= len(monitors) {
			http.NotFound(w, r)
			return
		}
		m := monitors[idx]
		kb := "scancode"
		if kbMode == rdp.KeyboardUnicode {
			kb = "unicode"
		}
		// For auto-detect monitors (Width=0), redirect without w/h params —
		// index.html will detect from browser viewport.
		pri := "0"
		if m.Primary {
			pri = "1"
		}
		abuf := 0
		if d.opts.AudioOut != nil {
			abuf = d.opts.AudioOut.BufMs
		}
		if m.Width == 0 {
			http.Redirect(w, r, fmt.Sprintf("/?monitor=%d&kb=%s&primary=%s&abuf=%d", idx, kb, pri, abuf), http.StatusFound)
		} else {
			http.Redirect(w, r, fmt.Sprintf("/?w=%d&h=%d&monitor=%d&kb=%s&primary=%s&abuf=%d", m.Width, m.Height, idx, kb, pri, abuf), http.StatusFound)
		}
	})

	mux.HandleFunc("/disconnect", func(w http.ResponseWriter, r *http.Request) {
		d.Close()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!DOCTYPE html><html><head><title>gopher-rdp</title>
<style>body{background:#1a1a2e;color:#cc7000;font:48px sans-serif;display:flex;justify-content:center;align-items:center;height:100vh;}</style>
</head><body>Disconnected</body></html>`)
	})

	mux.HandleFunc("/ws/control", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgradeWebSocket(w, r)
		if err != nil {
			return
		}
		// Block until the landing page tab is closed (WS disconnect).
		buf := make([]byte, 64)
		for {
			if _, err := readWSFrame(conn, buf, d.Log()); err != nil {
				break
			}
		}
		conn.Close()
		d.Close()
	})

	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		monitors := d.Monitors()
		idxStr := r.URL.Query().Get("monitor")
		idx, err := strconv.Atoi(idxStr)
		if err != nil || idx < 0 || idx >= len(monitors) {
			http.Error(w, "invalid monitor index", http.StatusBadRequest)
			return
		}
		// Read resolution from WS URL params (browser-detected).
		bw, _ := strconv.Atoi(r.URL.Query().Get("w"))
		bh, _ := strconv.Atoi(r.URL.Query().Get("h"))
		conn, err := upgradeWebSocket(w, r)
		if err != nil {
			return
		}
		defer conn.Close()
		if err := d.Attach(idx, conn, bw, bh); err != nil {
			// Log is internal; just return — connection already upgraded.
			return
		}
	})

	return mux
}

// ListenAddr converts a port number to a listen address (e.g. "8080" → ":8080").
func ListenAddr(port string) string {
	return ":" + port
}
