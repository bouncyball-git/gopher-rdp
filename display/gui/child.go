//go:build gui

package gui

import (
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"io"
	"log/slog"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"time"

	rdp "github.com/bouncyball-git/gopher-rdp"
	"github.com/bouncyball-git/gopher-rdp/protocol/rdpsnd"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/audio"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
)

// childGame implements ebiten.Game for a single monitor in multi-display mode.
// It communicates with the broker process via stdin/stdout pipes.
type childGame struct {
	log          *slog.Logger
	keyboardMode rdp.KeyboardMode
	monIdx       int
	screen       *ebiten.Image
	width        int
	height       int
	rgbaBuf      []byte
	mu           sync.Mutex

	lastMouseX int
	lastMouseY int

	// Custom cursor overlay
	cursorImg    *ebiten.Image
	cursorHotX   int
	cursorHotY   int
	cursorHidden bool
	cursorCache  map[uint16]*cursorEntry

	// Dynamic resize (debounced)
	resizeTimer *time.Timer
	pendingW    int
	pendingH    int
	lastOutW    float64
	lastOutH    float64

	// Clipboard bridging
	lastClipText string

	// Pipe output
	outMu  sync.Mutex
	stdout io.Writer

	// Disconnect detection
	disconnected atomic.Bool
	terminated   atomic.Bool

	// Hotkey state
	initialW int // RDP resolution at startup (for fullscreen restore)
	initialH int
}

type cursorEntry struct {
	img  *ebiten.Image
	hotX int
	hotY int
}

// RunChild is the entry point for child processes. Called from main() when
// GOPHER_RDP_CHILD is set. Reads MsgInit from stdin, creates an Ebiten
// window, and runs the game loop.
func RunChild() error {
	stdin := os.Stdin
	stdout := os.Stdout

	// Create logger matching the parent's format.
	var logger *slog.Logger
	if lvl := os.Getenv("GOPHER_RDP_LOG_LEVEL"); lvl != "" && lvl != "off" {
		var level slog.Level
		switch lvl {
		case "trace":
			level = slog.Level(-8) // rdp.LevelTrace
		case "debug":
			level = slog.LevelDebug
		case "info":
			level = slog.LevelInfo
		case "warn":
			level = slog.LevelWarn
		case "error":
			level = slog.LevelError
		default:
			level = slog.LevelInfo
		}
		logger = slog.New(newChildLogHandler(os.Stderr, level))
	} else {
		logger = slog.New(slog.DiscardHandler)
	}

	// Read MsgInit from broker.
	var buf [256]byte
	msgType, payload, err := ReadMsg(stdin, buf[:])
	if err != nil {
		return fmt.Errorf("read init: %w", err)
	}
	if msgType != MsgInit || len(payload) < 14 {
		return fmt.Errorf("expected MsgInit, got type=%d len=%d", msgType, len(payload))
	}

	monIdx, _, _, w, h, kbMode, primary := DecodeInit(payload)
	width, height := int(w), int(h)
	logger = logger.With("monitor", monIdx)
	logger.Info("child starting", "width", width, "height", height, "kbMode", kbMode)

	screen := ebiten.NewImage(width, height)
	screen.Fill(color.NRGBA{0, 0, 0, 0xFF})

	g := &childGame{
		log:          logger,
		keyboardMode: rdp.KeyboardMode(kbMode),
		monIdx:       int(monIdx),
		screen:       screen,
		width:        width,
		height:       height,
		rgbaBuf:      make([]byte, width*height*4),
		cursorCache:  make(map[uint16]*cursorEntry),
		stdout:       stdout,
		initialW:     width,
		initialH:     height,
	}

	// Start recv goroutine to read messages from broker.
	go g.recvLoop(stdin)

	// Set window size in logical pixels for HiDPI.
	scale := ebiten.Monitor().DeviceScaleFactor()
	logW := int(math.Round(float64(width) / scale))
	logH := int(math.Round(float64(height) / scale))
	ebiten.SetWindowSize(logW, logH)
	title := fmt.Sprintf("gopher-rdp [display %d]", monIdx)
	if primary {
		title += " (primary)"
	}
	ebiten.SetWindowTitle(title)
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	return ebiten.RunGame(g)
}

// sendMsg writes a message to the broker via stdout.
func (g *childGame) sendMsg(msgType byte, payload []byte) {
	g.outMu.Lock()
	WriteMsg(g.stdout, msgType, payload)
	g.outMu.Unlock()
}

// recvLoop reads messages from the broker via stdin.
func (g *childGame) recvLoop(r io.Reader) {
	buf := make([]byte, 4*1024*1024) // 4MB reusable buffer for bitmaps
	for {
		msgType, payload, err := ReadMsg(r, buf)
		if err != nil {
			g.log.Info("pipe closed", "error", err)
			g.disconnected.Store(true)
			return
		}
		switch msgType {
		case MsgBitmap:
			if len(payload) < 8 {
				continue
			}
			g.handleBitmap(payload)
		case MsgCursor:
			if len(payload) < 1 {
				continue
			}
			g.handleCursor(payload)
		case MsgDisconnect:
			g.log.Info("disconnect received")
			g.disconnected.Store(true)
			g.terminated.Store(true)
			return
		case MsgResize:
			if len(payload) < 4 {
				continue
			}
			w, h := DecodeResize(payload)
			g.handleResize(int(w), int(h))
		case MsgClipboard:
			text := string(payload)
			g.mu.Lock()
			g.lastClipText = text
			g.mu.Unlock()
			if err := writeClipboardSafe(text); err != nil {
				g.log.Debug("WriteClipboard failed", "error", err)
			}
		case MsgAudio:
			if len(payload) < 8 {
				continue
			}
			g.handleAudio(payload)
		}
	}
}

func (g *childGame) handleBitmap(payload []byte) {
	x, y, w, h, rgba := DecodeBitmap(payload)
	ix, iy, iw, ih := int(x), int(y), int(w), int(h)
	needed := iw * ih * 4
	if len(rgba) < needed {
		return
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if ix+iw > g.width {
		iw = g.width - ix
	}
	if iy+ih > g.height {
		ih = g.height - iy
	}
	if iw <= 0 || ih <= 0 {
		return
	}

	// Repack if clipped (stride changed).
	pixNeeded := iw * ih * 4
	if iw != int(w) {
		srcStride := int(w) * 4
		dstStride := iw * 4
		if len(g.rgbaBuf) < pixNeeded {
			g.rgbaBuf = make([]byte, pixNeeded)
		}
		for row := range ih {
			copy(g.rgbaBuf[row*dstStride:row*dstStride+dstStride], rgba[row*srcStride:row*srcStride+dstStride])
		}
		sub := g.screen.SubImage(image.Rect(ix, iy, ix+iw, iy+ih)).(*ebiten.Image)
		sub.WritePixels(g.rgbaBuf[:pixNeeded])
	} else {
		sub := g.screen.SubImage(image.Rect(ix, iy, ix+iw, iy+ih)).(*ebiten.Image)
		sub.WritePixels(rgba[:pixNeeded])
	}
}

func (g *childGame) handleCursor(payload []byte) {
	g.mu.Lock()
	defer g.mu.Unlock()
	subtype := payload[0]
	switch subtype {
	case CursorNull:
		g.cursorHidden = true
		g.cursorImg = nil
	case CursorDefault:
		g.cursorHidden = false
		g.cursorImg = nil
	case CursorShape:
		if len(payload) < 11 {
			return
		}
		cacheIdx := binary.LittleEndian.Uint16(payload[1:3])
		hotX := int(binary.LittleEndian.Uint16(payload[3:5]))
		hotY := int(binary.LittleEndian.Uint16(payload[5:7]))
		w := int(binary.LittleEndian.Uint16(payload[7:9]))
		h := int(binary.LittleEndian.Uint16(payload[9:11]))
		rgba := payload[11:]
		if w > 0 && h > 0 && len(rgba) >= w*h*4 {
			img := ebiten.NewImage(w, h)
			img.WritePixels(rgba[:w*h*4])
			entry := &cursorEntry{img: img, hotX: hotX, hotY: hotY}
			g.cursorCache[cacheIdx] = entry
			g.cursorImg = img
			g.cursorHotX = hotX
			g.cursorHotY = hotY
			g.cursorHidden = false
		}
	case CursorCached:
		if len(payload) < 3 {
			return
		}
		cacheIdx := binary.LittleEndian.Uint16(payload[1:3])
		if entry, ok := g.cursorCache[cacheIdx]; ok {
			g.cursorImg = entry.img
			g.cursorHotX = entry.hotX
			g.cursorHotY = entry.hotY
			g.cursorHidden = false
		}
	}
}

func (g *childGame) handleResize(newW, newH int) {
	g.mu.Lock()
	if g.width == newW && g.height == newH {
		g.mu.Unlock()
		return
	}
	g.width = newW
	g.height = newH
	s := ebiten.NewImage(newW, newH)
	s.Fill(color.NRGBA{0, 0, 0, 0xFF})
	g.screen = s
	g.mu.Unlock()
	g.log.Info("resized", "width", newW, "height", newH)
}

// Audio handling — lazily initialize Ebiten audio context on first sample.
var (
	childAudioOnce   sync.Once
	childAudioStream *pcmStream
	childAudioPlayer *audio.Player
)

func (g *childGame) handleAudio(payload []byte) {
	channels, rate, bps, pcm := DecodeAudio(payload)
	if len(pcm) == 0 {
		return
	}
	childAudioOnce.Do(func() {
		r := int(rate)
		if r <= 0 {
			r = 44100
		}
		g.log.Info("audio: creating context", "rate", r)
		childAudioStream = newPCMStream()
		audioCtx := audio.NewContext(r)
		p, err := audioCtx.NewPlayer(childAudioStream)
		if err != nil {
			g.log.Error("audio player init failed", "error", err)
			return
		}
		p.SetBufferSize(100 * time.Millisecond)
		p.Play()
		childAudioPlayer = p
	})
	if childAudioStream == nil {
		return
	}
	// Write raw PCM, converting format as needed.
	sample := &rdpsnd.AudioSample{
		Format: rdpsnd.AudioFormat{
			Channels:      channels,
			SamplesPerSec: rate,
			BitsPerSample: bps,
		},
		Data: pcm,
	}
	childAudioStream.WriteRaw(sample)
}

// writeClipboardSafe wraps display.WriteClipboard but avoids importing display
// in a way that creates circular deps — we just use the OS tools directly.
func writeClipboardSafe(text string) error {
	// Import-safe: use display package.
	return writeClipboard(text)
}

func (g *childGame) Update() error {
	if g.terminated.Load() {
		return ebiten.Termination
	}
	if g.disconnected.Load() {
		if inpututil.IsKeyJustPressed(ebiten.KeyF12) {
			return ebiten.Termination
		}
		return nil
	}

	// Hotkeys (intercepted, not forwarded to remote)
	if inpututil.IsKeyJustPressed(ebiten.KeyF11) {
		if ebiten.IsFullscreen() {
			// Exit fullscreen → restore initial RDP resolution.
			ebiten.SetFullscreen(false)
			scale := ebiten.Monitor().DeviceScaleFactor()
			logW := int(math.Round(float64(g.initialW) / scale))
			logH := int(math.Round(float64(g.initialH) / scale))
			ebiten.SetWindowSize(logW, logH)
			g.sendMsg(MsgChildResize, EncodeResize(uint16(g.initialW), uint16(g.initialH)))
			g.log.Info("hotkey: exit fullscreen", "width", g.initialW, "height", g.initialH)
		} else {
			// Enter fullscreen → resize RDP to monitor's physical resolution.
			scale := ebiten.Monitor().DeviceScaleFactor()
			monW, monH := ebiten.Monitor().Size()
			physW := int(math.Round(float64(monW) * scale))
			physH := int(math.Round(float64(monH) * scale))
			ebiten.SetFullscreen(true)
			g.sendMsg(MsgChildResize, EncodeResize(uint16(physW), uint16(physH)))
			g.log.Info("hotkey: enter fullscreen", "width", physW, "height", physH)
		}
		// Cancel any pending LayoutF debounce.
		if g.resizeTimer != nil {
			g.resizeTimer.Stop()
		}
		g.lastOutW = 0
		g.lastOutH = 0
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyF12) {
		g.log.Info("hotkey: disconnect")
		g.sendMsg(MsgChildDisconnect, nil)
		return ebiten.Termination
	}

	// Ctrl+V: read local clipboard and send to broker.
	if inpututil.IsKeyJustPressed(ebiten.KeyV) &&
		(ebiten.IsKeyPressed(ebiten.KeyControlLeft) || ebiten.IsKeyPressed(ebiten.KeyControlRight)) {
		if text, err := readClipboard(); err == nil && text != "" {
			g.sendMsg(MsgChildClipboard, []byte(text))
		}
	}

	// Unicode mode: capture typed characters.
	if g.keyboardMode == rdp.KeyboardUnicode {
		for _, r := range ebiten.AppendInputChars(nil) {
			g.sendMsg(MsgUnicodeInput, EncodeUnicode(uint16(r), 0))       // press
			g.sendMsg(MsgUnicodeInput, EncodeUnicode(uint16(r), 0x8000))  // release
		}
	}

	// Keyboard: detect just-pressed, repeat, and just-released keys.
	// Key repeat: initial delay ~500ms (30 ticks @60TPS), then ~33ms (2 ticks).
	const repeatDelay = 30
	const repeatInterval = 2
	for key, entry := range keyMap {
		if hotkeySet[key] {
			continue
		}
		if g.keyboardMode == rdp.KeyboardUnicode && !controlKeys[key] {
			continue
		}
		dur := inpututil.KeyPressDuration(key)
		if dur == 1 {
			// Just pressed
			var flags uint16
			if entry.extended {
				flags |= 0x0100
			}
			g.sendMsg(MsgKeyboard, EncodeKeyboard(entry.scancode, flags))
		} else if dur > repeatDelay && (dur-repeatDelay)%repeatInterval == 0 {
			// Key repeat
			var flags uint16
			if entry.extended {
				flags |= 0x0100
			}
			g.sendMsg(MsgKeyboard, EncodeKeyboard(entry.scancode, flags))
		}
		if inpututil.IsKeyJustReleased(key) {
			flags := uint16(0x8000) // release flag
			if entry.extended {
				flags |= 0x0100
			}
			g.sendMsg(MsgKeyboard, EncodeKeyboard(entry.scancode, flags))
		}
	}

	// Mouse position.
	mx, my := ebiten.CursorPosition()
	if mx != g.lastMouseX || my != g.lastMouseY {
		g.lastMouseX = mx
		g.lastMouseY = my
		g.sendMsg(MsgMouse, EncodeMouse(uint16(mx), uint16(my), 0x0800)) // PtrFlagsMove
	}

	// Mouse buttons.
	type mouseBtn struct {
		btn  ebiten.MouseButton
		flag uint16
	}
	buttons := [3]mouseBtn{
		{ebiten.MouseButtonLeft, 0x1000},   // PtrFlagsButton1
		{ebiten.MouseButtonRight, 0x2000},  // PtrFlagsButton2
		{ebiten.MouseButtonMiddle, 0x4000}, // PtrFlagsButton3
	}
	for _, b := range buttons {
		if inpututil.IsMouseButtonJustPressed(b.btn) {
			g.sendMsg(MsgMouse, EncodeMouse(uint16(mx), uint16(my), 0x8000|b.flag)) // PtrFlagsDown|button
		}
		if inpututil.IsMouseButtonJustReleased(b.btn) {
			g.sendMsg(MsgMouse, EncodeMouse(uint16(mx), uint16(my), b.flag))
		}
	}

	// Mouse wheel.
	wx, wy := ebiten.Wheel()
	if wy != 0 {
		delta := int(wy)
		if delta == 0 {
			if wy > 0 {
				delta = 1
			} else {
				delta = -1
			}
		}
		g.sendMsg(MsgWheel, EncodeWheel(uint16(mx), uint16(my), int16(delta), false))
	}
	if wx != 0 {
		delta := int(wx)
		if delta == 0 {
			if wx > 0 {
				delta = 1
			} else {
				delta = -1
			}
		}
		g.sendMsg(MsgWheel, EncodeWheel(uint16(mx), uint16(my), int16(delta), true))
	}

	return nil
}

func (g *childGame) Draw(screen *ebiten.Image) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.disconnected.Load() {
		drawDigitalOverlay(screen, "Disconnected")
		return
	}

	screen.DrawImage(g.screen, nil)

	if g.cursorImg != nil {
		ebiten.SetCursorMode(ebiten.CursorModeHidden)
		mx, my := ebiten.CursorPosition()
		op := &ebiten.DrawImageOptions{}
		op.GeoM.Translate(float64(mx-g.cursorHotX), float64(my-g.cursorHotY))
		screen.DrawImage(g.cursorImg, op)
	} else if g.cursorHidden {
		ebiten.SetCursorMode(ebiten.CursorModeHidden)
	} else {
		ebiten.SetCursorMode(ebiten.CursorModeVisible)
	}
}

// DrawFinalScreen disables filtering for pixel-perfect output.
func (g *childGame) DrawFinalScreen(screen ebiten.FinalScreen, offscreen *ebiten.Image, geoM ebiten.GeoM) {
	op := &ebiten.DrawImageOptions{}
	op.GeoM = geoM
	op.Filter = ebiten.FilterNearest
	screen.DrawImage(offscreen, op)
}

func (g *childGame) Layout(outsideWidth, outsideHeight int) (int, int) {
	return g.width, g.height
}

// LayoutF implements ebiten.LayoutFer for precise HiDPI handling.
// Detects window resize and sends MsgChildResize to broker (debounced).
func (g *childGame) LayoutF(outsideWidth, outsideHeight float64) (float64, float64) {
	g.mu.Lock()
	curW, curH := g.width, g.height
	g.mu.Unlock()

	if outsideWidth == g.lastOutW && outsideHeight == g.lastOutH {
		return float64(curW), float64(curH)
	}

	// First call: record WM size, don't trigger resize.
	if g.lastOutW == 0 && g.lastOutH == 0 {
		g.lastOutW = outsideWidth
		g.lastOutH = outsideHeight
		return float64(curW), float64(curH)
	}
	g.lastOutW = outsideWidth
	g.lastOutH = outsideHeight

	// Convert logical size to physical pixels.
	scale := ebiten.Monitor().DeviceScaleFactor()
	physW := int(math.Round(outsideWidth * scale))
	physH := int(math.Round(outsideHeight * scale))

	// Detect resize — debounce 200ms.
	if physW > 0 && physH > 0 && (physW != curW || physH != curH) {
		if physW != g.pendingW || physH != g.pendingH {
			g.pendingW = physW
			g.pendingH = physH
			if g.resizeTimer != nil {
				g.resizeTimer.Stop()
			}
			g.resizeTimer = time.AfterFunc(200*time.Millisecond, func() {
				w, h := g.pendingW, g.pendingH
				g.log.LogAttrs(context.Background(), slog.LevelInfo, "window resized, requesting resize",
					slog.Int("width", w), slog.Int("height", h))
				g.sendMsg(MsgChildResize, EncodeResize(uint16(w), uint16(h)))
			})
		}
	}

	return float64(curW), float64(curH)
}

// readClipboard and writeClipboard delegate to the display package.
func readClipboard() (string, error) {
	return readClipboardImpl()
}

func writeClipboard(text string) error {
	return writeClipboardImpl(text)
}
