//go:build gui

package gui

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"time"

	rdp "github.com/bouncyball-git/gopher-rdp"
	"github.com/bouncyball-git/gopher-rdp/display"
	"github.com/bouncyball-git/gopher-rdp/protocol/disp"
	"github.com/bouncyball-git/gopher-rdp/protocol/rdpsnd"
)

// MonitorInfo describes a monitor for the broker.
type MonitorInfo struct {
	X, Y          int
	Width, Height int
	Primary       bool
}

// childProc holds the process and pipes for a child.
type childProc struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	logFile io.Closer       // log file redirected to stderr (nil if stderr)
	writeCh chan []byte      // buffered channel for async writes
	done    chan struct{}
}

// brokerState holds shared mutable state for the broker.
type brokerState struct {
	mu       sync.Mutex
	monitors []MonitorInfo
	children []*childProc
	client   *rdp.Client
	opts     *rdp.Options
	log      *slog.Logger

	// During a resize, suppress bitmap forwarding to unchanged monitors.
	// The server does a full deactivation-reactivation that repaints everything,
	// but unchanged monitors already have correct content on screen.
	resizingIdx int  // monitor being resized (-1 = none)
	resizing    bool // true between ResizeMulti and repaint completes
}

// RunMulti launches one Ebiten child process per monitor and routes RDP
// callbacks between the client and child processes.
func RunMulti(client *rdp.Client, opts *rdp.Options, monitors []MonitorInfo) error {
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	logger = logger.With("component", "GUI")

	state := &brokerState{
		monitors:    monitors,
		children:    make([]*childProc, len(monitors)),
		client:      client,
		opts:        opts,
		log:         logger,
		resizingIdx: -1,
	}

	// Spawn child processes.
	for i, mon := range monitors {
		child, err := spawnChild(i, mon, opts.KeyboardMode, logger)
		if err != nil {
			// Clean up already-spawned children.
			for j := 0; j < i; j++ {
				state.children[j].stdin.Close()
				state.children[j].cmd.Process.Kill()
			}
			return err
		}
		state.children[i] = child
	}

	// Start per-child writer goroutines.
	for i := range state.children {
		go state.runChildWriter(i)
	}

	// Register RDP callbacks.
	state.registerBrokerCallbacks()

	// Start per-child input reader goroutines.
	var wg sync.WaitGroup
	for i := range state.children {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			state.runInputReader(idx)
		}(i)
	}

	// Wait for all children to exit.
	wg.Wait()
	logger.Info("all child windows closed, shutting down")
	client.Close()
	return nil
}

func spawnChild(idx int, mon MonitorInfo, kbMode rdp.KeyboardMode, log *slog.Logger) (*childProc, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe)
	// Pass log level to child so it can match the parent's log format.
	logLevel := "off"
	for _, probe := range []struct {
		level slog.Level
		name  string
	}{
		{slog.Level(-8), "trace"}, // rdp.LevelTrace
		{slog.LevelDebug, "debug"},
		{slog.LevelInfo, "info"},
		{slog.LevelWarn, "warn"},
		{slog.LevelError, "error"},
	} {
		if log.Enabled(context.Background(), probe.level) {
			logLevel = probe.name
			break
		}
	}
	cmd.Env = append(os.Environ(), "GOPHER_RDP_CHILD=1",
		"GOPHER_RDP_LOG_LEVEL="+logLevel)
	// If a log file is configured, direct child stderr there too;
	// otherwise child logs go to the parent's stderr.
	var logFile *os.File
	if lf := os.Getenv("GOPHER_RDP_LOG_FILE"); lf != "" {
		if f, err := os.OpenFile(lf, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
			logFile = f
			cmd.Stderr = f
		} else {
			cmd.Stderr = os.Stderr
		}
	} else {
		cmd.Stderr = os.Stderr
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		stdin.Close()
		return nil, err
	}

	// Send MsgInit.
	initPayload := EncodeInit(uint8(idx), int32(mon.X), int32(mon.Y),
		uint16(mon.Width), uint16(mon.Height), uint8(kbMode), mon.Primary)
	if err := WriteMsg(stdin, MsgInit, initPayload); err != nil {
		cmd.Process.Kill()
		return nil, err
	}

	log.Info("spawned child", "monitor", idx, "pid", cmd.Process.Pid,
		"width", mon.Width, "height", mon.Height)

	child := &childProc{
		cmd:     cmd,
		stdin:   stdin,
		stdout:  stdout,
		logFile: logFile,
		writeCh: make(chan []byte, 512),
		done:    make(chan struct{}),
	}
	return child, nil
}

// runChildWriter drains the write channel and writes to the child's stdin pipe.
func (s *brokerState) runChildWriter(idx int) {
	child := s.children[idx]
	for {
		select {
		case data, ok := <-child.writeCh:
			if !ok {
				return
			}
			if _, err := child.stdin.Write(data); err != nil {
				s.log.Debug("child write error", "monitor", idx, "error", err)
				return
			}
		case <-child.done:
			return
		}
	}
}

// sendToChild queues a message for async delivery to a child.
func sendToChild(child *childProc, msgType byte, payload []byte) {
	totalLen := 5 + len(payload) // [len:u32][type:u8][payload]
	buf := make([]byte, totalLen)
	buf[0] = byte(1 + len(payload))
	buf[1] = byte((1 + len(payload)) >> 8)
	buf[2] = byte((1 + len(payload)) >> 16)
	buf[3] = byte((1 + len(payload)) >> 24)
	buf[4] = msgType
	copy(buf[5:], payload)
	select {
	case child.writeCh <- buf:
	default:
		// Drop if channel full — back-pressure.
	}
}

// sendToAllChildren broadcasts a message to all children.
func (s *brokerState) sendToAllChildren(msgType byte, payload []byte) {
	for _, child := range s.children {
		sendToChild(child, msgType, payload)
	}
}

// runInputReader reads input messages from a child's stdout and routes them
// to the RDP client with coordinate translation.
func (s *brokerState) runInputReader(idx int) {
	child := s.children[idx]
	defer func() {
		close(child.done)
		child.stdin.Close()
		child.cmd.Wait()
		if child.logFile != nil {
			child.logFile.Close()
		}
		s.log.Info("child exited", "monitor", idx)
	}()

	buf := make([]byte, 256)
	for {
		msgType, payload, err := ReadMsg(child.stdout, buf)
		if err != nil {
			return
		}

		// Read current monitor offset under lock.
		s.mu.Lock()
		monX, monY := s.monitors[idx].X, s.monitors[idx].Y
		s.mu.Unlock()

		switch msgType {
		case MsgKeyboard:
			if len(payload) < 4 {
				continue
			}
			scancode, flags := DecodeKeyboard(payload)
			pressed := flags&0x8000 == 0
			if err := s.client.SendKeyboard(scancode, pressed); err != nil {
				s.log.Debug("SendKeyboard error", "error", err)
			}
		case MsgMouse:
			if len(payload) < 6 {
				continue
			}
			x, y, buttons := DecodeMouse(payload)
			// Translate to virtual desktop coords.
			absX := int(x) + monX
			absY := int(y) + monY
			if err := s.client.SendMouse(absX, absY, buttons); err != nil {
				s.log.Debug("SendMouse error", "error", err)
			}
		case MsgWheel:
			if len(payload) < 7 {
				continue
			}
			x, y, delta, horiz := DecodeWheel(payload)
			absX := int(x) + monX
			absY := int(y) + monY
			if err := s.client.SendMouseWheel(absX, absY, int(delta), horiz); err != nil {
				s.log.Debug("SendMouseWheel error", "error", err)
			}
		case MsgChildResize:
			if len(payload) < 4 {
				continue
			}
			w, h := DecodeResize(payload)
			s.resizeMonitor(idx, int(w), int(h))
		case MsgChildClipboard:
			text := string(payload)
			if err := s.client.SetClipboard(text); err != nil {
				s.log.Debug("SetClipboard error", "error", err)
			}
		case MsgUnicodeInput:
			if len(payload) < 4 {
				continue
			}
			codepoint, flags := DecodeUnicode(payload)
			pressed := flags&0x8000 == 0
			if err := s.client.SendUnicode(codepoint, pressed); err != nil {
				s.log.Debug("SendUnicode error", "error", err)
			}
		case MsgChildDisconnect:
			s.log.Info("child requested disconnect", "monitor", idx)
			s.sendToAllChildren(MsgDisconnect, nil)
			for _, c := range s.children {
				c.stdin.Close()
			}
			s.client.Close()
			return
		}
	}
}

// resizeMonitor updates a monitor's dimensions, repositions all monitors,
// updates RDP topology, and confirms resize to the child.
func (s *brokerState) resizeMonitor(childIdx, newW, newH int) {
	s.mu.Lock()
	s.monitors[childIdx].Width = newW
	s.monitors[childIdx].Height = newH
	s.repositionMonitors()
	s.updateOpts()

	offX, offY := primaryOriginOffset(s.monitors)
	layouts := make([]disp.MonitorLayout, len(s.monitors))
	for i, m := range s.monitors {
		layouts[i] = disp.MonitorLayout{
			Left:   int32(m.X - offX),
			Top:    int32(m.Y - offY),
			Width:  uint32(m.Width),
			Height: uint32(m.Height),
		}
		if m.Primary {
			layouts[i].Flags = 0x01
		}
	}

	// Send resize confirmation to the child.
	sendToChild(s.children[childIdx], MsgResize, EncodeResize(uint16(newW), uint16(newH)))
	// Suppress bitmap forwarding to unchanged monitors until reactivation completes.
	s.resizingIdx = childIdx
	s.resizing = true
	s.mu.Unlock()

	s.log.Info("monitor resized", "monitor", childIdx, "width", newW, "height", newH)

	// Update server topology. The actual resize is asynchronous — the server
	// does a deactivation-reactivation and fires OnResize when ready.
	// RefreshRect is issued from the OnResize callback, not here.
	if err := s.client.ResizeMulti(layouts); err != nil {
		s.log.Error("ResizeMulti failed", "error", err)
	}
}

// repositionMonitors auto-positions all monitors left-to-right.
// Coordinates are kept non-negative for bitmap routing (server sends uint16 coords).
// Must be called with s.mu held.
func (s *brokerState) repositionMonitors() {
	xPos := 0
	for i := range s.monitors {
		s.monitors[i].X = xPos
		s.monitors[i].Y = 0
		xPos += s.monitors[i].Width
	}
}

// primaryOriginOffset returns the X,Y offset of the primary monitor.
// Subtracting this from each monitor's position shifts the primary to (0,0)
// as required by MS-RDPBCGR for protocol messages.
func primaryOriginOffset(monitors []MonitorInfo) (int, int) {
	for _, m := range monitors {
		if m.Primary {
			return m.X, m.Y
		}
	}
	return 0, 0
}

// updateOpts computes the bounding box and updates opts.Width/Height/Monitors.
// Must be called with s.mu held.
func (s *brokerState) updateOpts() {
	offX, offY := primaryOriginOffset(s.monitors)
	var maxR, maxB int
	configs := make([]rdp.MonitorConfig, len(s.monitors))
	for i, m := range s.monitors {
		configs[i] = rdp.MonitorConfig{
			X: m.X - offX, Y: m.Y - offY,
			Width: m.Width, Height: m.Height,
			Primary: m.Primary,
		}
		if r := m.X + m.Width; r > maxR {
			maxR = r
		}
		if b := m.Y + m.Height; b > maxB {
			maxB = b
		}
	}
	if len(configs) > 1 {
		s.opts.Monitors = configs
	}
	if maxR > 0 {
		s.opts.Width = uint16(maxR)
	}
	if maxB > 0 {
		s.opts.Height = uint16(maxB)
	}
}

// registerBrokerCallbacks registers RDP callbacks that route updates to children.
func (s *brokerState) registerBrokerCallbacks() {
	var (
		inPaint    bool
		frameBatch []bitmapRect
	)

	s.client.OnBeginPaint(func() {
		inPaint = true
		frameBatch = frameBatch[:0]
	})

	s.client.OnEndPaint(func() {
		inPaint = false
		if len(frameBatch) > 0 {
			s.routeBitmaps(frameBatch)
		}
	})

	s.client.OnStridedBitmap(func(x, y, w, h int, data []byte, stride int) {
		// Convert strided to contiguous 32bpp RGBA.
		dstStride := w * 4
		dataCopy := make([]byte, dstStride*h)
		for row := range h {
			copy(dataCopy[row*dstStride:row*dstStride+dstStride], data[row*stride:row*stride+dstStride])
		}
		rect := bitmapRect{X: x, Y: y, Width: w, Height: h, Data: dataCopy}
		if inPaint {
			frameBatch = append(frameBatch, rect)
			return
		}
		s.routeBitmaps([]bitmapRect{rect})
	})

	s.client.OnBitmap(func(u *rdp.BitmapUpdate) {
		if u.Width <= 0 || u.Height <= 0 || u.Width > 32768 || u.Height > 32768 {
			return
		}
		// Convert to 32bpp RGBA.
		needed := u.Width * u.Height * 4
		rgba := make([]byte, needed)
		if u.BitsPerPixel == 32 && u.TopDown && len(u.Data) >= needed {
			copy(rgba, u.Data[:needed])
		} else if len(u.Data) >= u.Width*u.Height*(u.BitsPerPixel/8) {
			display.ConvertToRGBA(rgba, u.Data, u.Width, u.Height, u.BitsPerPixel, u.TopDown)
		}
		rect := bitmapRect{X: u.X, Y: u.Y, Width: u.Width, Height: u.Height, Data: rgba}
		if inPaint {
			frameBatch = append(frameBatch, rect)
			return
		}
		s.routeBitmaps([]bitmapRect{rect})
	})

	s.client.OnPointer(func(u *rdp.PointerUpdate) {
		var payload []byte
		switch u.Type {
		case rdp.PointerNull:
			payload = []byte{CursorNull}
		case rdp.PointerDefault:
			payload = []byte{CursorDefault}
		case rdp.PointerShape:
			payload = EncodeCursorShape(u.CacheIndex, u.HotSpotX, u.HotSpotY, u.Width, u.Height, u.Data)
		case rdp.PointerCached:
			payload = EncodeCursorCached(u.CacheIndex)
		}
		s.sendToAllChildren(MsgCursor, payload)
	})

	s.client.OnClipboardUpdate(func(hasText, hasImage bool) {
		if hasText {
			s.client.RequestClipboard()
		}
		if hasImage {
			s.client.RequestClipboardImage()
		}
	})
	s.client.OnClipboardText(func(text string) {
		s.sendToAllChildren(MsgClipboard, []byte(text))
	})

	// Audio: send to primary monitor's child only (avoid duplicate playback).
	s.client.OnAudioData(func(sample *rdpsnd.AudioSample) {
		payload := EncodeAudio(sample.Format.Channels, sample.Format.SamplesPerSec,
			sample.Format.BitsPerSample, sample.Data)
		s.mu.Lock()
		for i, m := range s.monitors {
			if m.Primary {
				sendToChild(s.children[i], MsgAudio, payload)
				break
			}
		}
		s.mu.Unlock()
	})

	// OnResize: server confirmed the resize (deactivation-reactivation complete).
	// The bitmap repaint flood follows over multiple frames. Clear the
	// suppression after a delay to cover the entire flood.
	s.client.OnResize(func(newW, newH int) {
		s.log.Info("server confirmed resize", "width", newW, "height", newH)
		time.AfterFunc(2*time.Second, func() {
			s.mu.Lock()
			s.resizing = false
			s.resizingIdx = -1
			s.mu.Unlock()
		})
	})

	prevDisconnect := s.client.GetOnDisconnect()
	s.client.OnDisconnect(func(err error) {
		s.log.Info("RDP disconnected", "error", err)
		s.sendToAllChildren(MsgDisconnect, nil)
		for _, child := range s.children {
			child.stdin.Close()
		}
		if prevDisconnect != nil {
			prevDisconnect(err)
		}
	})
}

// bitmapRect holds a 32bpp RGBA bitmap in virtual desktop coordinates.
type bitmapRect struct {
	X, Y          int
	Width, Height int
	Data          []byte // 32bpp RGBA, top-down, Width*Height*4 bytes
}

// routeBitmaps clips bitmap rects to each monitor and sends to the appropriate child.
func (s *brokerState) routeBitmaps(rects []bitmapRect) {
	s.mu.Lock()
	monitors := make([]MonitorInfo, len(s.monitors))
	copy(monitors, s.monitors)
	children := s.children
	resizing := s.resizing
	resizingIdx := s.resizingIdx
	s.mu.Unlock()

	for i, mon := range monitors {
		// During a resize, skip unchanged monitors — they already have
		// correct content. The server repaints everything after
		// deactivation-reactivation, but only the resized monitor needs it.
		if resizing && i != resizingIdx {
			continue
		}
		for _, rect := range rects {
			clipped, ok := clipBitmapToMonitor(rect, mon)
			if !ok {
				continue
			}
			payloadSize := 8 + len(clipped.Data)
			payload := make([]byte, payloadSize)
			EncodeBitmapInto(payload, uint16(clipped.X), uint16(clipped.Y),
				uint16(clipped.Width), uint16(clipped.Height), clipped.Data)
			sendToChild(children[i], MsgBitmap, payload)
		}
	}
}

// clipBitmapToMonitor clips a bitmap rect to a monitor's area and translates
// to monitor-local coordinates. Returns the clipped rect and true if there
// is an intersection, false otherwise.
func clipBitmapToMonitor(rect bitmapRect, mon MonitorInfo) (bitmapRect, bool) {
	ix0 := max(rect.X, mon.X)
	iy0 := max(rect.Y, mon.Y)
	ix1 := min(rect.X+rect.Width, mon.X+mon.Width)
	iy1 := min(rect.Y+rect.Height, mon.Y+mon.Height)
	if ix0 >= ix1 || iy0 >= iy1 {
		return bitmapRect{}, false
	}

	iw := ix1 - ix0
	ih := iy1 - iy0

	// No clipping needed — just translate.
	if ix0 == rect.X && iy0 == rect.Y && iw == rect.Width && ih == rect.Height {
		out := rect
		out.X = rect.X - mon.X
		out.Y = rect.Y - mon.Y
		return out, true
	}

	// Extract sub-rectangle.
	srcStride := rect.Width * 4
	dstStride := iw * 4
	srcXOff := (ix0 - rect.X) * 4
	srcYOff := iy0 - rect.Y

	dataCopy := make([]byte, dstStride*ih)
	for row := range ih {
		srcOff := (srcYOff+row)*srcStride + srcXOff
		dstOff := row * dstStride
		if srcOff+dstStride <= len(rect.Data) {
			copy(dataCopy[dstOff:dstOff+dstStride], rect.Data[srcOff:srcOff+dstStride])
		}
	}

	return bitmapRect{
		X:      ix0 - mon.X,
		Y:      iy0 - mon.Y,
		Width:  iw,
		Height: ih,
		Data:   dataCopy,
	}, true
}
