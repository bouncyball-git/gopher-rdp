package web

import (
	"encoding/binary"
	"fmt"
	"log/slog"
	"sync"
	"time"

	rdp "gopher-rdp"
	"gopher-rdp/display"
	"gopher-rdp/protocol/audin"
	"gopher-rdp/protocol/disp"
	"gopher-rdp/protocol/rdpsnd"
)

// MonitorRect describes a monitor's position and size in the virtual desktop.
type MonitorRect struct {
	Index         int
	X, Y          int
	Width, Height int
	Primary       bool
}

// Dispatcher fans out RDP client callbacks to per-monitor WebSocket sessions.
// It registers ONE set of callbacks on the client and routes bitmap updates
// to the appropriate monitor session based on intersection with monitor rects.
//
// The Dispatcher manages the RDP client lifecycle: the first browser tab
// to connect triggers the RDP connection. Subsequent tabs that resolve
// auto-detect monitors trigger ResizeMulti to update the topology.
type Dispatcher struct {
	mu       sync.Mutex
	monitors []MonitorRect
	sessions []*monitorSession // indexed by monitor, nil if disconnected
	client   *rdp.Client
	opts     *rdp.Options
	log      *slog.Logger
	kbMode   rdp.KeyboardMode
	done     chan struct{} // closed when RDP session ends

	// Frame batching state (only accessed from callback goroutine — no lock needed).
	inPaint    bool
	frameBatch []bitmapMsg

}

// monitorSession holds per-monitor channels and connection state.
type monitorSession struct {
	monitor  MonitorRect
	conn     *wsConn
	bitmapCh chan []bitmapMsg
	cursorCh chan cursorMsg
	clipCh      chan string
	clipImageCh chan []byte
	audioCh     chan []byte
	h264Ch      chan []byte // pre-built WS frames for H.264 pass-through
	done     chan struct{}
	once     sync.Once
}

func (s *monitorSession) close() {
	s.once.Do(func() { close(s.done) })
}

func (d *Dispatcher) audioInBufMs() int {
	if d.opts.AudioIn != nil {
		return d.opts.AudioIn.BufMs
	}
	return 0
}

// NewDispatcher creates a dispatcher for multi-monitor web viewing.
// Monitors with Width=0 are auto-detect — their resolution is filled in
// when a browser tab connects. The RDP connection is deferred until the
// first browser tab connects.
func NewDispatcher(opts *rdp.Options, monitors []MonitorRect, log *slog.Logger, kbMode rdp.KeyboardMode) *Dispatcher {
	return &Dispatcher{
		monitors: monitors,
		sessions: make([]*monitorSession, len(monitors)),
		opts:     opts,
		log:      log,
		kbMode:   kbMode,
		done:     make(chan struct{}),
	}
}

// Monitors returns the monitor rectangles.
func (d *Dispatcher) Monitors() []MonitorRect {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]MonitorRect, len(d.monitors))
	copy(out, d.monitors)
	return out
}

// Log returns the dispatcher's logger.
func (d *Dispatcher) Log() *slog.Logger {
	return d.log
}

// KBMode returns the keyboard mode.
func (d *Dispatcher) KBMode() rdp.KeyboardMode {
	return d.kbMode
}

// Done returns a channel that is closed when the RDP session ends.
func (d *Dispatcher) Done() <-chan struct{} {
	return d.done
}

// Close closes the RDP client and all monitor sessions, then signals Done.
func (d *Dispatcher) Close() {
	d.mu.Lock()
	client := d.client
	for i, s := range d.sessions {
		if s != nil {
			lockedWriteWSFrame(s.conn, []byte{wsMsgDisconnect})
			s.conn.Close()
			s.close()
			d.sessions[i] = nil
		}
	}
	d.mu.Unlock()
	if client != nil {
		client.Close()
	}
	select {
	case <-d.done:
	default:
		close(d.done)
	}
}

func (d *Dispatcher) registerCallbacks() {
	var dropped int

	sendBatch := func(s *monitorSession, batch []bitmapMsg) {
		select {
		case s.bitmapCh <- batch:
		default:
			dropped += len(batch)
			if dropped%100 < len(batch) {
				d.log.Warn("dropped bitmap updates", "count", dropped)
			}
		}
	}

	d.client.OnBeginPaint(func() {
		d.inPaint = true
		d.frameBatch = d.frameBatch[:0]
	})

	d.client.OnEndPaint(func() {
		d.inPaint = false
		if len(d.frameBatch) == 0 {
			return
		}
		d.mu.Lock()
		for i, s := range d.sessions {
			if s == nil {
				continue
			}
			var monBatch []bitmapMsg
			for _, msg := range d.frameBatch {
				clipped, ok := clipBitmapToMonitor(msg, d.monitors[i])
				if ok {
					monBatch = append(monBatch, clipped)
				}
			}
			if len(monBatch) > 0 {
				sendBatch(s, monBatch)
			}
		}
		d.mu.Unlock()
	})

	d.client.OnStridedBitmap(func(x, y, w, h int, data []byte, stride int) {
		if w <= 0 || h <= 0 || w > 32768 || h > 32768 {
			return
		}
		dstStride := w * 4
		if stride < dstStride || len(data) < h*stride {
			return
		}
		dataCopy := make([]byte, dstStride*h)
		for row := range h {
			copy(dataCopy[row*dstStride:row*dstStride+dstStride], data[row*stride:row*stride+dstStride])
		}
		msg := bitmapMsg{
			X: x, Y: y, Width: w, Height: h,
			BitsPerPixel: 32, TopDown: true, Data: dataCopy,
		}
		if d.inPaint {
			d.frameBatch = append(d.frameBatch, msg)
			return
		}
		d.routeSingle(msg)
	})

	d.client.OnBitmap(func(u *rdp.BitmapUpdate) {
		dataCopy := make([]byte, len(u.Data))
		copy(dataCopy, u.Data)
		msg := bitmapMsg{
			X: u.X, Y: u.Y, Width: u.Width, Height: u.Height,
			BitsPerPixel: u.BitsPerPixel, TopDown: u.TopDown, Data: dataCopy,
		}
		if d.inPaint {
			d.frameBatch = append(d.frameBatch, msg)
			return
		}
		d.routeSingle(msg)
	})

	d.client.OnPointer(func(u *rdp.PointerUpdate) {
		msg := cursorMsg{
			Type: u.Type, CacheIndex: u.CacheIndex,
			HotSpotX: u.HotSpotX, HotSpotY: u.HotSpotY,
			Width: u.Width, Height: u.Height, Data: u.Data,
		}
		d.broadcast(func(s *monitorSession) {
			select {
			case s.cursorCh <- msg:
			default:
			}
		})
	})

	d.client.OnClipboardUpdate(func(hasText, hasImage bool) {
		if hasText {
			d.client.RequestClipboard()
		}
		if hasImage {
			d.client.RequestClipboardImage()
		}
	})
	d.client.OnClipboardText(func(text string) {
		d.broadcast(func(s *monitorSession) {
			select {
			case s.clipCh <- text:
			default:
			}
		})
	})
	d.client.OnClipboardImage(func(pngData []byte) {
		d.broadcast(func(s *monitorSession) {
			select {
			case s.clipImageCh <- pngData:
			default:
			}
		})
	})

	d.client.OnAudioData(func(s *rdpsnd.AudioSample) {
		pcmLen := len(s.Data)
		d.log.Debug("audio output to browser", "bytes", pcmLen, "rate", s.Format.SamplesPerSec, "channels", s.Format.Channels, "bps", s.Format.BitsPerSample)
		payloadLen := 9 + pcmLen
		buf := make([]byte, 10+payloadLen)
		buf[10] = wsMsgAudio
		binary.LittleEndian.PutUint16(buf[11:13], s.Format.Channels)
		binary.LittleEndian.PutUint32(buf[13:17], s.Format.SamplesPerSec)
		binary.LittleEndian.PutUint16(buf[17:19], s.Format.BitsPerSample)
		copy(buf[19:], s.Data)
		d.broadcast(func(sess *monitorSession) {
			select {
			case sess.audioCh <- buf:
			default:
			}
		})
	})

	// Audio input callbacks — silence fill is handled by the client layer.
	d.client.OnAudioInputOpen(func(f audin.AudioFormat) {
		var buf [11]byte
		buf[0] = wsMsgAudioInput
		binary.LittleEndian.PutUint16(buf[1:3], f.Channels)
		binary.LittleEndian.PutUint32(buf[3:7], f.SamplesPerSec)
		binary.LittleEndian.PutUint16(buf[7:9], f.BitsPerSample)
		binary.LittleEndian.PutUint16(buf[9:11], uint16(d.audioInBufMs()))
		msg := buf // copy
		d.broadcast(func(s *monitorSession) {
			lockedWriteWSFrame(s.conn, msg[:])
		})
		d.log.Info("audio input open, notified browsers", "channels", f.Channels, "rate", f.SamplesPerSec, "bps", f.BitsPerSample, "bufMs", d.audioInBufMs())
	})
	d.client.OnAudioInputClose(func() {
		var buf [11]byte
		buf[0] = wsMsgAudioInput
		msg := buf // copy
		d.broadcast(func(s *monitorSession) {
			lockedWriteWSFrame(s.conn, msg[:])
		})
		d.log.Info("audio input closed, notified browsers")
	})

	d.client.OnResize(func(newW, newH int) {
		var buf [5]byte
		buf[0] = wsMsgResize
		binary.LittleEndian.PutUint16(buf[1:3], uint16(newW))
		binary.LittleEndian.PutUint16(buf[3:5], uint16(newH))
		msg := buf // copy
		d.broadcast(func(s *monitorSession) {
			lockedWriteWSFrame(s.conn, msg[:])
		})
	})

	// H.264 frames are broadcast to all monitors — the encoded stream
	// can't be clipped, and the destRect coordinates handle positioning.
	// Note: OnH264Frame must be set before Connect() for EGFX caps.
	// The Dispatcher.Connect() method handles this.

	prevDisconnect := d.client.GetOnDisconnect()
	d.client.OnDisconnect(func(err error) {
		d.mu.Lock()
		for _, s := range d.sessions {
			if s != nil {
				lockedWriteWSFrame(s.conn, []byte{wsMsgDisconnect})
				s.conn.Close()
				s.close()
			}
		}
		d.mu.Unlock()
		select {
		case <-d.done:
		default:
			close(d.done)
		}
		if prevDisconnect != nil {
			prevDisconnect(err)
		}
	})
}

// routeSingle routes a single bitmap message (outside of a frame batch)
// to all intersecting monitors.
func (d *Dispatcher) routeSingle(msg bitmapMsg) {
	d.mu.Lock()
	for i, s := range d.sessions {
		if s == nil {
			continue
		}
		clipped, ok := clipBitmapToMonitor(msg, d.monitors[i])
		if ok {
			select {
			case s.bitmapCh <- []bitmapMsg{clipped}:
			default:
			}
		}
	}
	d.mu.Unlock()
}

// broadcast calls fn for every connected session under the lock.
func (d *Dispatcher) broadcast(fn func(*monitorSession)) {
	d.mu.Lock()
	for _, s := range d.sessions {
		if s != nil {
			fn(s)
		}
	}
	d.mu.Unlock()
}

// Attach connects a WebSocket to a monitor slot. If the monitor was
// auto-detect (Width=0), its resolution is set from the provided w/h.
// The first Attach triggers the RDP connection. Subsequent Attach calls
// for auto-detect monitors trigger ResizeMulti to update the topology.
// Blocks until the session ends. Detaches automatically on return.
func (d *Dispatcher) Attach(monitorIndex int, conn *wsConn, w, h int) error {
	if monitorIndex < 0 || monitorIndex >= len(d.monitors) {
		return fmt.Errorf("invalid monitor index %d", monitorIndex)
	}

	d.mu.Lock()

	// Fill in auto-detect resolution.
	mon := &d.monitors[monitorIndex]
	topologyChanged := false
	if mon.Width == 0 && w > 0 && h > 0 {
		mon.Width = w
		mon.Height = h
		topologyChanged = true
		d.log.Info("monitor resolution detected", "monitor", monitorIndex, "width", w, "height", h)
	}

	// Reposition all monitors left-to-right when topology changes.
	if topologyChanged {
		d.repositionMonitors()
	}

	// Connect RDP on first attach.
	if d.client == nil {
		if mon.Width == 0 {
			d.mu.Unlock()
			return fmt.Errorf("monitor %d has no resolution", monitorIndex)
		}
		d.log.Info("first monitor connected, starting RDP", "monitor", monitorIndex)
		client, err := d.createRDPClient()
		if err != nil {
			d.mu.Unlock()
			return err
		}
		d.client = client
		d.registerCallbacks()
		if err := client.Connect(); err != nil {
			d.client = nil
			d.mu.Unlock()
			return fmt.Errorf("RDP connect: %w", err)
		}
		d.log.Info("RDP connected", "width", d.opts.Width, "height", d.opts.Height)
		go func() {
			<-client.Done()
			select {
			case <-d.done:
			default:
				close(d.done)
			}
		}()
	} else if topologyChanged {
		// RDP already connected — update topology via ResizeMulti.
		d.resizeTopology()
	}

	monRect := *mon // copy current state
	s := &monitorSession{
		monitor:  monRect,
		conn:     conn,
		bitmapCh: make(chan []bitmapMsg, 512),
		cursorCh: make(chan cursorMsg, 64),
		clipCh:      make(chan string, 4),
		clipImageCh: make(chan []byte, 4),
		audioCh:     make(chan []byte, 32),
		h264Ch:      make(chan []byte, 64),
		done:     make(chan struct{}),
	}
	old := d.sessions[monitorIndex]
	if old != nil {
		old.conn.Close()
		old.close()
	}
	d.sessions[monitorIndex] = s
	client := d.client
	d.mu.Unlock()

	// Request full redraw for this monitor's area.
	if err := client.RefreshRect(monRect.X, monRect.Y, monRect.Width, monRect.Height); err != nil {
		d.log.Debug("RefreshRect failed", "monitor", monitorIndex, "error", err)
	}

	// If audio input is already recording, send format to this new session.
	if f, ok := client.AudioInputFormat(); ok {
		var buf [11]byte
		buf[0] = wsMsgAudioInput
		binary.LittleEndian.PutUint16(buf[1:3], f.Channels)
		binary.LittleEndian.PutUint32(buf[3:7], f.SamplesPerSec)
		binary.LittleEndian.PutUint16(buf[7:9], f.BitsPerSample)
		binary.LittleEndian.PutUint16(buf[9:11], uint16(d.audioInBufMs()))
		lockedWriteWSFrame(s.conn, buf[:])
	}

	// Run send/recv loops (blocks until done).
	d.runSession(s, monitorIndex)

	// Detach on return.
	d.mu.Lock()
	if d.sessions[monitorIndex] == s {
		d.sessions[monitorIndex] = nil
	}
	d.mu.Unlock()
	return nil
}

// repositionMonitors auto-positions all monitors left-to-right, skipping
// auto-detect monitors (Width=0). Must be called with d.mu held.
func (d *Dispatcher) repositionMonitors() {
	xPos := 0
	for i := range d.monitors {
		d.monitors[i].X = xPos
		d.monitors[i].Y = 0
		xPos += d.monitors[i].Width // Width=0 → no advance
	}
}

// createRDPClient creates the RDP client without connecting.
// Must be called with d.mu held. Caller should register callbacks
// before calling client.Connect().
func (d *Dispatcher) createRDPClient() (*rdp.Client, error) {
	d.updateOpts()
	client, err := rdp.NewClient(d.opts)
	if err != nil {
		return nil, fmt.Errorf("create RDP client: %w", err)
	}
	return client, nil
}

// updateOpts updates d.opts.Width, Height, and Monitors from the current
// monitor topology. Must be called with d.mu held.
//
// In multi-monitor mode, unresolved monitors (Width=0) get a placeholder size
// matching the first resolved monitor so the full topology is always sent to
// the server, preserving the correct primary flag.
func (d *Dispatcher) updateOpts() {
	if len(d.monitors) <= 1 {
		d.opts.Monitors = nil
		for _, m := range d.monitors {
			if m.Width > 0 {
				d.opts.Width = uint16(m.Width)
				d.opts.Height = uint16(m.Height)
			}
		}
		return
	}

	// Default size for unresolved monitors: first resolved, or 1920x1080.
	defW, defH := 1920, 1080
	for _, m := range d.monitors {
		if m.Width > 0 {
			defW, defH = m.Width, m.Height
			break
		}
	}

	// Compute effective positions using placeholder sizes for unresolved.
	type rect struct{ x, y, w, h int }
	rects := make([]rect, len(d.monitors))
	xPos := 0
	for i, m := range d.monitors {
		w, h := m.Width, m.Height
		if w == 0 {
			w, h = defW, defH
		}
		rects[i] = rect{xPos, 0, w, h}
		xPos += w
	}

	// Primary-at-origin shift for MS-RDPBCGR.
	var offX, offY int
	for i, m := range d.monitors {
		if m.Primary {
			offX, offY = rects[i].x, rects[i].y
			break
		}
	}

	configs := make([]rdp.MonitorConfig, len(d.monitors))
	var maxR, maxB int
	for i, m := range d.monitors {
		configs[i] = rdp.MonitorConfig{
			X: rects[i].x - offX, Y: rects[i].y - offY,
			Width: rects[i].w, Height: rects[i].h,
			Primary: m.Primary,
		}
		if r := rects[i].x + rects[i].w; r > maxR {
			maxR = r
		}
		if b := rects[i].y + rects[i].h; b > maxB {
			maxB = b
		}
	}
	d.opts.Monitors = configs
	d.opts.Width = uint16(maxR)
	d.opts.Height = uint16(maxB)
}

// resizeTopology sends a ResizeMulti to the server with the current topology.
// Must be called with d.mu held and d.client != nil.
func (d *Dispatcher) resizeTopology() {
	d.updateOpts()
	if len(d.opts.Monitors) <= 1 {
		// Single monitor — use simple Resize.
		if len(d.opts.Monitors) == 1 {
			m := d.opts.Monitors[0]
			if err := d.client.Resize(m.Width, m.Height); err != nil {
				d.log.Error("Resize failed", "error", err)
			}
		}
		return
	}
	// opts.Monitors already has primary-at-origin shift applied.
	layouts := make([]disp.MonitorLayout, len(d.opts.Monitors))
	for i, m := range d.opts.Monitors {
		layouts[i] = disp.MonitorLayout{
			Left:   int32(m.X),
			Top:    int32(m.Y),
			Width:  uint32(m.Width),
			Height: uint32(m.Height),
		}
		if m.Primary {
			layouts[i].Flags = 0x01
		}
	}
	if err := d.client.ResizeMulti(layouts); err != nil {
		d.log.Error("ResizeMulti failed", "error", err)
	}
}

// runSession runs the send and recv loops for a monitor session.
func (d *Dispatcher) runSession(s *monitorSession, monIdx int) {
	mon := s.monitor

	// Reusable frame buffer for batched bitmap messages.
	monPixels := mon.Width * mon.Height
	if mon.Width > 0 && monPixels/mon.Width != mon.Height {
		monPixels = 0 // overflow
	}
	wsBuf := make([]byte, 10+1+8+monPixels*4)

	// Disconnect watcher.
	go func() {
		select {
		case <-d.done:
			d.log.Info("RDP disconnected, notifying browser", "monitor", monIdx)
			s.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			lockedWriteWSFrame(s.conn, []byte{wsMsgDisconnect})
			s.conn.Close()
			s.close()
		case <-s.done:
		}
	}()

	// Audio send loop.
	go func() {
		defer s.close()
		for {
			select {
			case abuf := <-s.audioCh:
				if err := lockedWriteWSFrameDirect(s.conn, abuf, len(abuf)-10); err != nil {
					d.log.Error("WebSocket audio write error", "monitor", monIdx, "error", err)
					return
				}
			case <-s.done:
				return
			}
		}
	}()

	// H.264 send loop.
	go func() {
		defer s.close()
		for {
			select {
			case buf := <-s.h264Ch:
				if err := lockedWriteWSFrameDirect(s.conn, buf, len(buf)-10); err != nil {
					d.log.Error("WebSocket H.264 write error", "monitor", monIdx, "error", err)
					return
				}
			case <-s.done:
				return
			}
		}
	}()

	// Bitmap/cursor/clipboard send loop.
	go func() {
		defer s.close()
		for {
			select {
			case batch, ok := <-s.bitmapCh:
				if !ok {
					return
				}
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
				off := 10
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
				if err := lockedWriteWSFrameDirect(s.conn, fb, payloadLen); err != nil {
					d.log.Error("WebSocket write error", "monitor", monIdx, "error", err)
					return
				}
			case cm := <-s.cursorCh:
				if err := lockedSendCursorMsg(s.conn, &cm); err != nil {
					d.log.Error("WebSocket cursor write error", "monitor", monIdx, "error", err)
					return
				}
			case text := <-s.clipCh:
				msg := make([]byte, 1+len(text))
				msg[0] = wsMsgClipboard
				copy(msg[1:], text)
				if err := lockedWriteWSFrame(s.conn, msg); err != nil {
					d.log.Error("WebSocket clipboard write error", "monitor", monIdx, "error", err)
					return
				}
			case pngData := <-s.clipImageCh:
				msg := make([]byte, 1+len(pngData))
				msg[0] = wsMsgClipboardImage
				copy(msg[1:], pngData)
				if err := lockedWriteWSFrame(s.conn, msg); err != nil {
					d.log.Error("WebSocket clipboard image write error", "monitor", monIdx, "error", err)
					return
				}
			case <-s.done:
				return
			}
		}
	}()

	// Recv loop: WebSocket → keyboard/mouse input (blocks).
	readBuf := make([]byte, 65536) // large enough for mic PCM chunks
	for {
		payload, err := readWSFrame(s.conn, readBuf, d.log)
		if err != nil {
			s.close()
			d.log.Info("WebSocket disconnected", "monitor", monIdx)
			return
		}
		if len(payload) < 1 {
			continue
		}

		d.mu.Lock()
		client := d.client
		d.mu.Unlock()
		if client == nil {
			continue
		}
		select {
		case <-client.Done():
			continue
		default:
		}

		// Read current monitor offset (may change after resize).
		d.mu.Lock()
		monX, monY := d.monitors[monIdx].X, d.monitors[monIdx].Y
		d.mu.Unlock()

		switch payload[0] {
		case 0x01: // Keyboard scancode
			if len(payload) < 5 {
				continue
			}
			scancode := binary.LittleEndian.Uint16(payload[1:3])
			flags := binary.LittleEndian.Uint16(payload[3:5])
			pressed := flags&0x8000 == 0
			if err := client.SendKeyboard(scancode, pressed); err != nil {
				d.log.Debug("SendKeyboard error", "error", err)
			}
		case 0x02: // Mouse — translate to virtual desktop coords
			if len(payload) < 7 {
				continue
			}
			x := int(binary.LittleEndian.Uint16(payload[1:3])) + monX
			y := int(binary.LittleEndian.Uint16(payload[3:5])) + monY
			buttons := binary.LittleEndian.Uint16(payload[5:7])
			if err := client.SendMouse(x, y, buttons); err != nil {
				d.log.Debug("SendMouse error", "error", err)
			}
		case 0x03: // Wheel — translate to virtual desktop coords
			if len(payload) < 8 {
				continue
			}
			x := int(binary.LittleEndian.Uint16(payload[1:3])) + monX
			y := int(binary.LittleEndian.Uint16(payload[3:5])) + monY
			delta := int(int16(binary.LittleEndian.Uint16(payload[5:7])))
			horizontal := payload[7] != 0
			if err := client.SendMouseWheel(x, y, delta, horizontal); err != nil {
				d.log.Debug("SendMouseWheel error", "error", err)
			}
		case 0x04: // Resize — update this monitor's resolution and resize topology
			if len(payload) < 5 {
				continue
			}
			newW := int(binary.LittleEndian.Uint16(payload[1:3]))
			newH := int(binary.LittleEndian.Uint16(payload[3:5]))
			d.log.Info("monitor resize request", "monitor", monIdx, "width", newW, "height", newH)
			d.mu.Lock()
			d.monitors[monIdx].Width = newW
			d.monitors[monIdx].Height = newH
			d.repositionMonitors()
			d.resizeTopology()
			d.mu.Unlock()
			// Request redraw for this monitor's new area.
			d.mu.Lock()
			m := d.monitors[monIdx]
			d.mu.Unlock()
			if err := client.RefreshRect(m.X, m.Y, m.Width, m.Height); err != nil {
				d.log.Debug("RefreshRect after resize failed", "monitor", monIdx, "error", err)
			}
		case 0x05: // Clipboard
			if len(payload) < 2 {
				continue
			}
			text := string(payload[1:])
			if err := client.SetClipboard(text); err != nil {
				d.log.Error("SetClipboard failed", "error", err)
			}
		case 0x06: // Unicode keyboard
			if len(payload) < 5 {
				continue
			}
			codepoint := binary.LittleEndian.Uint16(payload[1:3])
			flags := binary.LittleEndian.Uint16(payload[3:5])
			pressed := flags&0x8000 == 0
			if err := client.SendUnicode(codepoint, pressed); err != nil {
				d.log.Debug("SendUnicode error", "error", err)
			}
		case 0x07: // Audio input PCM: [0x07][srcRate:u32 LE][srcCh:u16 LE][pcm S16LE...]
			const micHdr = 1 + 4 + 2
			if len(payload) < micHdr+2 {
				continue
			}
			srcRate := binary.LittleEndian.Uint32(payload[1:5])
			srcCh := binary.LittleEndian.Uint16(payload[5:7])
			pcm := payload[micHdr:]

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
				d.log.Debug("SendAudioInput error", "error", err)
			}
		default:
			d.log.Warn("unknown input type", "type", payload[0])
		}
	}
}

// clipBitmapToMonitor clips a bitmap message to a monitor's rectangle and
// translates coordinates to monitor-local space. Returns the clipped message
// and true if there is an intersection, or false if there is none.
func clipBitmapToMonitor(msg bitmapMsg, mon MonitorRect) (bitmapMsg, bool) {
	// Compute intersection of bitmap rect with monitor rect.
	ix0 := max(msg.X, mon.X)
	iy0 := max(msg.Y, mon.Y)
	ix1 := min(msg.X+msg.Width, mon.X+mon.Width)
	iy1 := min(msg.Y+msg.Height, mon.Y+mon.Height)
	if ix0 >= ix1 || iy0 >= iy1 {
		return bitmapMsg{}, false
	}

	iw := ix1 - ix0
	ih := iy1 - iy0

	// If the bitmap is entirely within the monitor, just translate coords.
	if ix0 == msg.X && iy0 == msg.Y && iw == msg.Width && ih == msg.Height {
		out := msg
		out.X = msg.X - mon.X
		out.Y = msg.Y - mon.Y
		return out, true
	}

	// Extract sub-rectangle pixel data.
	srcBpp := msg.BitsPerPixel
	if srcBpp == 0 {
		srcBpp = 32
	}
	bytesPerPixel := srcBpp / 8
	srcStride := msg.Width * bytesPerPixel
	dstStride := iw * bytesPerPixel

	// Offset into the source bitmap where the intersection starts.
	srcXOff := (ix0 - msg.X) * bytesPerPixel
	srcYOff := iy0 - msg.Y

	dataCopy := make([]byte, dstStride*ih)
	for row := range ih {
		srcRow := srcYOff + row
		srcOff := srcRow*srcStride + srcXOff
		dstOff := row * dstStride
		if srcOff+dstStride <= len(msg.Data) {
			copy(dataCopy[dstOff:dstOff+dstStride], msg.Data[srcOff:srcOff+dstStride])
		}
	}

	return bitmapMsg{
		X:            ix0 - mon.X,
		Y:            iy0 - mon.Y,
		Width:        iw,
		Height:       ih,
		BitsPerPixel: msg.BitsPerPixel,
		TopDown:      msg.TopDown,
		Data:         dataCopy,
	}, true
}
