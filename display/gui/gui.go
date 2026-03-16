//go:build gui

package gui

import (
	"context"
	"errors"
	"image"
	"image/color"
	"log/slog"
	"math"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	rdp "gopher-rdp"
	"gopher-rdp/display"
	"gopher-rdp/protocol/pdu"
	"gopher-rdp/protocol/rdpsnd"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/audio"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
)

type keyEntry struct {
	scancode uint16
	extended bool
}

// Ebiten key → RDP scancode mapping
var keyMap = map[ebiten.Key]keyEntry{
	ebiten.KeyEscape:       {0x01, false},
	ebiten.KeyDigit1:       {0x02, false},
	ebiten.KeyDigit2:       {0x03, false},
	ebiten.KeyDigit3:       {0x04, false},
	ebiten.KeyDigit4:       {0x05, false},
	ebiten.KeyDigit5:       {0x06, false},
	ebiten.KeyDigit6:       {0x07, false},
	ebiten.KeyDigit7:       {0x08, false},
	ebiten.KeyDigit8:       {0x09, false},
	ebiten.KeyDigit9:       {0x0A, false},
	ebiten.KeyDigit0:       {0x0B, false},
	ebiten.KeyMinus:        {0x0C, false},
	ebiten.KeyEqual:        {0x0D, false},
	ebiten.KeyBackspace:    {0x0E, false},
	ebiten.KeyTab:          {0x0F, false},
	ebiten.KeyQ:            {0x10, false},
	ebiten.KeyW:            {0x11, false},
	ebiten.KeyE:            {0x12, false},
	ebiten.KeyR:            {0x13, false},
	ebiten.KeyT:            {0x14, false},
	ebiten.KeyY:            {0x15, false},
	ebiten.KeyU:            {0x16, false},
	ebiten.KeyI:            {0x17, false},
	ebiten.KeyO:            {0x18, false},
	ebiten.KeyP:            {0x19, false},
	ebiten.KeyBracketLeft:  {0x1A, false},
	ebiten.KeyBracketRight: {0x1B, false},
	ebiten.KeyEnter:        {0x1C, false},
	ebiten.KeyControlLeft:  {0x1D, false},
	ebiten.KeyA:            {0x1E, false},
	ebiten.KeyS:            {0x1F, false},
	ebiten.KeyD:            {0x20, false},
	ebiten.KeyF:            {0x21, false},
	ebiten.KeyG:            {0x22, false},
	ebiten.KeyH:            {0x23, false},
	ebiten.KeyJ:            {0x24, false},
	ebiten.KeyK:            {0x25, false},
	ebiten.KeyL:            {0x26, false},
	ebiten.KeySemicolon:    {0x27, false},
	ebiten.KeyQuote:        {0x28, false},
	ebiten.KeyBackquote:    {0x29, false},
	ebiten.KeyShiftLeft:    {0x2A, false},
	ebiten.KeyBackslash:    {0x2B, false},
	ebiten.KeyZ:            {0x2C, false},
	ebiten.KeyX:            {0x2D, false},
	ebiten.KeyC:            {0x2E, false},
	ebiten.KeyV:            {0x2F, false},
	ebiten.KeyB:            {0x30, false},
	ebiten.KeyN:            {0x31, false},
	ebiten.KeyM:            {0x32, false},
	ebiten.KeyComma:        {0x33, false},
	ebiten.KeyPeriod:       {0x34, false},
	ebiten.KeySlash:        {0x35, false},
	ebiten.KeyShiftRight:   {0x36, false},
	ebiten.KeyNumpadMultiply: {0x37, false},
	ebiten.KeyAltLeft:      {0x38, false},
	ebiten.KeySpace:        {0x39, false},
	ebiten.KeyCapsLock:     {0x3A, false},
	ebiten.KeyF1:           {0x3B, false},
	ebiten.KeyF2:           {0x3C, false},
	ebiten.KeyF3:           {0x3D, false},
	ebiten.KeyF4:           {0x3E, false},
	ebiten.KeyF5:           {0x3F, false},
	ebiten.KeyF6:           {0x40, false},
	ebiten.KeyF7:           {0x41, false},
	ebiten.KeyF8:           {0x42, false},
	ebiten.KeyF9:           {0x43, false},
	ebiten.KeyF10:          {0x44, false},
	ebiten.KeyNumLock:      {0x45, false},
	ebiten.KeyScrollLock:   {0x46, false},
	ebiten.KeyNumpad7:      {0x47, false},
	ebiten.KeyNumpad8:      {0x48, false},
	ebiten.KeyNumpad9:      {0x49, false},
	ebiten.KeyNumpadSubtract: {0x4A, false},
	ebiten.KeyNumpad4:      {0x4B, false},
	ebiten.KeyNumpad5:      {0x4C, false},
	ebiten.KeyNumpad6:      {0x4D, false},
	ebiten.KeyNumpadAdd:    {0x4E, false},
	ebiten.KeyNumpad1:      {0x4F, false},
	ebiten.KeyNumpad2:      {0x50, false},
	ebiten.KeyNumpad3:      {0x51, false},
	ebiten.KeyNumpad0:      {0x52, false},
	ebiten.KeyNumpadDecimal: {0x53, false},
	ebiten.KeyF11:          {0x57, false},
	ebiten.KeyF12:          {0x58, false},

	// Extended keys
	ebiten.KeyNumpadEnter:  {0x1C, true},
	ebiten.KeyControlRight: {0x1D, true},
	ebiten.KeyNumpadDivide: {0x35, true},
	ebiten.KeyPrintScreen:  {0x37, true},
	ebiten.KeyAltRight:     {0x38, true},
	ebiten.KeyHome:         {0x47, true},
	ebiten.KeyArrowUp:      {0x48, true},
	ebiten.KeyPageUp:       {0x49, true},
	ebiten.KeyArrowLeft:    {0x4B, true},
	ebiten.KeyArrowRight:   {0x4D, true},
	ebiten.KeyEnd:          {0x4F, true},
	ebiten.KeyArrowDown:    {0x50, true},
	ebiten.KeyPageDown:     {0x51, true},
	ebiten.KeyInsert:       {0x52, true},
	ebiten.KeyDelete:       {0x53, true},
	ebiten.KeyMetaLeft:     {0x5B, true},
	ebiten.KeyMetaRight:    {0x5C, true},
	ebiten.KeyContextMenu:  {0x5D, true},
}

// controlKeys are keys that should always use scancodes even in unicode mode
// (they have no printable character representation).
var controlKeys = map[ebiten.Key]bool{
	ebiten.KeyEscape:       true,
	ebiten.KeyBackspace:    true,
	ebiten.KeyTab:          true,
	ebiten.KeyEnter:        true,
	ebiten.KeyControlLeft:  true,
	ebiten.KeyControlRight: true,
	ebiten.KeyShiftLeft:    true,
	ebiten.KeyShiftRight:   true,
	ebiten.KeyAltLeft:      true,
	ebiten.KeyAltRight:     true,
	ebiten.KeyMetaLeft:     true,
	ebiten.KeyMetaRight:    true,
	ebiten.KeyCapsLock:     true,
	ebiten.KeyNumLock:      true,
	ebiten.KeyScrollLock:   true,
	ebiten.KeyF1:           true,
	ebiten.KeyF2:           true,
	ebiten.KeyF3:           true,
	ebiten.KeyF4:           true,
	ebiten.KeyF5:           true,
	ebiten.KeyF6:           true,
	ebiten.KeyF7:           true,
	ebiten.KeyF8:           true,
	ebiten.KeyF9:           true,
	ebiten.KeyF10:          true,
	ebiten.KeyF11:          true,
	ebiten.KeyF12:          true,
	ebiten.KeyPrintScreen:  true,
	ebiten.KeyInsert:       true,
	ebiten.KeyDelete:       true,
	ebiten.KeyHome:         true,
	ebiten.KeyEnd:          true,
	ebiten.KeyPageUp:       true,
	ebiten.KeyPageDown:     true,
	ebiten.KeyArrowUp:      true,
	ebiten.KeyArrowDown:    true,
	ebiten.KeyArrowLeft:    true,
	ebiten.KeyArrowRight:   true,
	ebiten.KeyContextMenu:  true,
	ebiten.KeyNumpadEnter:  true,
}

// game implements ebiten.Game for the RDP viewer.
type game struct {
	client       *rdp.Client
	log          *slog.Logger
	keyboardMode rdp.KeyboardMode
	screen       *ebiten.Image
	width        int
	height       int
	rgbaBuf      []byte
	fbBuf        []byte // reusable buffer for framebuffer→screen blit (top-down RGBA)
	needsRedraw         bool // framebuffer was modified — blit to screen on next Draw
	mu                  sync.Mutex
	inPaint             bool // true while inside EGFX frame flush (mu held by beginPaint)
	stridedRectsInFrame int  // per-rect strided callbacks fired in this frame
	egfxActive          bool // true once any EGFX strided callback has fired
	lastMouseX   int
	lastMouseY   int

	// Custom cursor overlay
	cursorImg    *ebiten.Image
	cursorHotX   int
	cursorHotY   int
	cursorHidden bool

	// Dynamic resize (debounced)
	resizeTimer *time.Timer
	pendingW    int
	pendingH    int
	lastOutW    float64 // previous LayoutF outsideWidth
	lastOutH    float64 // previous LayoutF outsideHeight

	// Clipboard bridging
	lastClipText string

	// Disconnect detection
	disconnected atomic.Bool
	terminated   atomic.Bool // server-initiated clean disconnect (logoff) — exit app

	// Hotkey state
	initialW int // RDP resolution at startup (for fullscreen restore)
	initialH int
}

// Run launches the graphical desktop viewer using Ebiten.
func Run(client *rdp.Client, opts *rdp.Options, width, height int) error {
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	logger = logger.With("component", "GUI")

	screen := ebiten.NewImage(width, height)
	screen.Fill(color.NRGBA{0, 0, 0, 0xFF}) // opaque black — prevents desktop bleed-through
	g := &game{
		client:       client,
		log:          logger,
		keyboardMode: opts.KeyboardMode,
		screen:       screen,
		width:        width,
		height:       height,
		rgbaBuf:      make([]byte, width*height*4),
		initialW:     width,
		initialH:     height,
	}

	// Frame-level locking: during an EGFX frame flush, beginPaint holds
	// the mutex so Ebiten's Draw() cannot render partial state.
	client.OnBeginPaint(func() {
		g.mu.Lock()
		g.inPaint = true
		g.stridedRectsInFrame = 0
	})
	client.OnEndPaint(func() {
		// Still holding g.mu from beginPaint — no race with Draw().
		//
		// EGFX path: per-rect strided callbacks already wrote each dirty rect
		// directly from the surface buffer to g.screen (and to the framebuffer).
		// A full framebuffer readback here would be redundant and risks
		// overwriting correct per-rect data with stale framebuffer content
		// (the framebuffer is bottom-up; the extra round-trip conversion can
		// introduce ghost artifacts during fast SurfaceToSurface blits).
		//
		// Legacy path (no strided rects AND EGFX not active): the framebuffer
		// is the only source of truth, so we must blit it to g.screen.
		// When EGFX is active, the strided callbacks own the screen image;
		// a full framebuffer blit here would overwrite correct EGFX content
		// (e.g. when a fast-path pointer update triggers endPaint with no
		// bitmap data, stridedRectsInFrame is 0 but the screen is valid).
		if g.stridedRectsInFrame == 0 && !g.egfxActive {
			w, h := g.client.FramebufferDims()
			if w <= 0 || h <= 0 {
				g.inPaint = false
				g.mu.Unlock()
				return
			}
			need := w * h * 4
			if len(g.fbBuf) < need {
				g.fbBuf = make([]byte, need)
			}
			if rw, rh := g.client.FramebufferWriteTopDown(g.fbBuf); rw > 0 && rh > 0 {
				// Framebuffer may have resized (reconnect-based resize) before
				// OnResize fires. Recreate the screen image to match.
				if rw != g.width || rh != g.height {
					g.width = rw
					g.height = rh
					g.screen = ebiten.NewImage(rw, rh)
				}
				g.screen.WritePixels(g.fbBuf[:rw*rh*4])
			}
		}
		g.inPaint = false
		g.mu.Unlock()
	})

	// Strided callback for GFX pipeline updates (32bpp top-down RGBA with stride).
	// Reads directly from the surface buffer — no intermediate callbackBuf copy.
	client.OnStridedBitmap(func(x, y, w, h int, data []byte, stride int) {
		if !g.inPaint {
			g.mu.Lock()
		}
		if x+w > g.width {
			w = g.width - x
		}
		if y+h > g.height {
			h = g.height - y
		}
		if w <= 0 || h <= 0 {
			if !g.inPaint {
				g.mu.Unlock()
			}
			return
		}

		needed := w * h * 4
		if len(g.rgbaBuf) < needed {
			g.rgbaBuf = make([]byte, needed)
		}
		rgba := g.rgbaBuf[:needed]
		dstStride := w * 4
		for row := range h {
			copy(rgba[row*dstStride:row*dstStride+dstStride], data[row*stride:row*stride+dstStride])
		}
		// Force fully opaque when alpha=0.
		for i := 3; i < needed; i += 4 {
			if rgba[i] == 0 {
				rgba[i] = 0xFF
			}
		}
		sub := g.screen.SubImage(image.Rect(x, y, x+w, y+h)).(*ebiten.Image)
		sub.WritePixels(rgba)
		g.stridedRectsInFrame++
		g.egfxActive = true
		if !g.inPaint {
			g.mu.Unlock()
		}
	})

	// Legacy callback for non-GFX bitmap updates (orders, bitmap cache — various bpp).
	// The framebuffer is the source of truth — just mark dirty; Draw() blits to screen.
	client.OnBitmap(func(u *rdp.BitmapUpdate) {
		if !g.inPaint {
			g.mu.Lock()
		}
		g.needsRedraw = true
		if !g.inPaint {
			g.mu.Unlock()
		}
	})

	// Chain disconnect/reconnect callbacks — preserve any callbacks set by CLI.
	prevDisconnect := client.GetOnDisconnect()
	client.OnDisconnect(func(err error) {
		g.log.LogAttrs(context.Background(), slog.LevelInfo, "RDP disconnected", slog.Any("error", err))
		g.disconnected.Store(true)
		if errors.Is(err, rdp.ErrDisconnected) {
			g.terminated.Store(true)
		}
		if prevDisconnect != nil {
			prevDisconnect(err)
		}
	})
	prevReconnecting := client.GetOnReconnecting()
	client.OnReconnecting(func() {
		g.log.LogAttrs(context.Background(), slog.LevelInfo, "RDP reconnecting")
		g.disconnected.Store(true)
		g.egfxActive = false // EGFX channel torn down; reset until first strided callback
		if prevReconnecting != nil {
			prevReconnecting()
		}
	})
	prevReconnected := client.GetOnReconnected()
	client.OnReconnected(func() {
		g.log.LogAttrs(context.Background(), slog.LevelInfo, "RDP reconnected")
		g.disconnected.Store(false)
		if prevReconnected != nil {
			prevReconnected()
		}
	})

	client.OnResize(func(newW, newH int) {
		if !g.inPaint {
			g.mu.Lock()
		}
		g.width = newW
		g.height = newH
		s := ebiten.NewImage(newW, newH)
		s.Fill(color.NRGBA{0, 0, 0, 0xFF})
		g.screen = s
		if !g.inPaint {
			g.mu.Unlock()
		}
		g.log.LogAttrs(context.Background(), slog.LevelInfo, "resized", slog.Int("width", newW), slog.Int("height", newH))
		// Request full redraw at new resolution
		if err := client.RefreshRect(0, 0, newW, newH); err != nil {
			g.log.LogAttrs(context.Background(), slog.LevelError, "RefreshRect after resize failed", slog.Any("error", err))
		}
	})

	client.OnPointer(func(u *rdp.PointerUpdate) {
		if !g.inPaint {
			g.mu.Lock()
		}
		defer func() {
			if !g.inPaint {
				g.mu.Unlock()
			}
		}()
		switch u.Type {
		case rdp.PointerNull:
			g.cursorHidden = true
			g.cursorImg = nil
		case rdp.PointerDefault:
			g.cursorHidden = false
			g.cursorImg = nil
		case rdp.PointerShape:
			w, h := int(u.Width), int(u.Height)
			if w > 0 && h > 0 && len(u.Data) >= w*h*4 {
				img := ebiten.NewImage(w, h)
				img.WritePixels(u.Data)
				g.cursorImg = img
				g.cursorHotX = int(u.HotSpotX)
				g.cursorHotY = int(u.HotSpotY)
				g.cursorHidden = false
			}
		}
	})

	// Clipboard: bridge remote ↔ local clipboard via OS tools.
	client.OnClipboardUpdate(func(hasText, hasImage bool) {
		g.log.LogAttrs(context.Background(), slog.LevelInfo, "clipboard update from remote",
			slog.Bool("hasText", hasText), slog.Bool("hasImage", hasImage))
		if hasText {
			client.RequestClipboard()
		}
		if hasImage {
			client.RequestClipboardImage()
		}
	})
	client.OnClipboardText(func(text string) {
		g.log.LogAttrs(context.Background(), slog.LevelInfo, "clipboard text from remote", slog.Int("chars", len(text)))
		if err := display.WriteClipboard(text); err != nil {
			g.log.LogAttrs(context.Background(), slog.LevelError, "WriteClipboard failed", slog.Any("error", err))
		}
		g.mu.Lock()
		g.lastClipText = text
		g.mu.Unlock()
	})
	client.OnClipboardImage(func(pngData []byte) {
		g.log.LogAttrs(context.Background(), slog.LevelInfo, "clipboard image from remote", slog.Int("bytes", len(pngData)))
		if err := display.WriteClipboardImage(pngData); err != nil {
			g.log.LogAttrs(context.Background(), slog.LevelError, "WriteClipboardImage failed", slog.Any("error", err))
		}
	})

	// Audio playback via Ebiten — defer context creation until the first
	// sample arrives so we can match the server's sample rate exactly,
	// avoiding resampling entirely.
	stream := newPCMStream()
	var audioPlayer *audio.Player
	var audioOnce sync.Once
	client.OnAudioData(func(s *rdpsnd.AudioSample) {
		audioOnce.Do(func() {
			rate := int(s.Format.SamplesPerSec)
			if rate <= 0 {
				rate = 44100
			}
			g.log.LogAttrs(context.Background(), slog.LevelInfo, "audio: creating context", slog.Int("rate", rate))
			audioCtx := audio.NewContext(rate)
			p, err := audioCtx.NewPlayer(stream)
			if err != nil {
				g.log.LogAttrs(context.Background(), slog.LevelError, "audio player init failed", slog.Any("error", err))
				return
			}
			p.SetBufferSize(100 * time.Millisecond)
			p.Play()
			audioPlayer = p
		})
		stream.WriteRaw(s)
	})
	client.OnAudioClose(func() {
		if audioPlayer != nil {
			audioPlayer.Pause()
		}
	})

	// Set window size in logical pixels so physical pixels match the RDP resolution.
	// On HiDPI displays (scale > 1.0), the logical size must be smaller.
	// Use math.Round to avoid truncation losing a pixel (e.g. 1600/1.000625 → 1599).
	scale := ebiten.Monitor().DeviceScaleFactor()
	logW := int(math.Round(float64(width) / scale))
	logH := int(math.Round(float64(height) / scale))
	ebiten.SetWindowSize(logW, logH)
	ebiten.SetWindowTitle("gopher-rdp")
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	return ebiten.RunGame(g)
}

// hotkeySet contains keys intercepted as local hotkeys (not forwarded to remote).
var hotkeySet = map[ebiten.Key]bool{
	ebiten.KeyF11: true,
	ebiten.KeyF12: true,
}

func (g *game) Update() error {
	if g.terminated.Load() {
		return ebiten.Termination
	}
	if g.disconnected.Load() {
		// Show disconnect overlay; F12 or window close exits.
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
			if err := g.client.Resize(g.initialW, g.initialH); err != nil {
				g.log.LogAttrs(context.Background(), slog.LevelError, "Resize failed", slog.Any("error", err))
			}
			g.log.LogAttrs(context.Background(), slog.LevelInfo, "hotkey: exit fullscreen",
				slog.Int("width", g.initialW), slog.Int("height", g.initialH))
		} else {
			// Enter fullscreen → resize RDP to monitor's physical resolution.
			scale := ebiten.Monitor().DeviceScaleFactor()
			monW, monH := ebiten.Monitor().Size()
			physW := int(math.Round(float64(monW) * scale))
			physH := int(math.Round(float64(monH) * scale))
			ebiten.SetFullscreen(true)
			if err := g.client.Resize(physW, physH); err != nil {
				g.log.LogAttrs(context.Background(), slog.LevelError, "Resize failed", slog.Any("error", err))
			}
			g.log.LogAttrs(context.Background(), slog.LevelInfo, "hotkey: enter fullscreen",
				slog.Int("width", physW), slog.Int("height", physH))
		}
		// Cancel any pending LayoutF debounce and reset tracking so it
		// doesn't fire a conflicting resize.
		if g.resizeTimer != nil {
			g.resizeTimer.Stop()
		}
		g.lastOutW = 0
		g.lastOutH = 0
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyF12) {
		g.log.LogAttrs(context.Background(), slog.LevelInfo, "hotkey: disconnect")
		g.client.Close()
		return ebiten.Termination
	}

	// Ctrl+Shift+D: dump framebuffer + ebiten screen to PPM files
	if inpututil.IsKeyJustPressed(ebiten.KeyD) &&
		(ebiten.IsKeyPressed(ebiten.KeyControlLeft) || ebiten.IsKeyPressed(ebiten.KeyControlRight)) &&
		(ebiten.IsKeyPressed(ebiten.KeyShiftLeft) || ebiten.IsKeyPressed(ebiten.KeyShiftRight)) {
		if fname, err := g.client.DumpFramebuffer(); err != nil {
			g.log.LogAttrs(context.Background(), slog.LevelError, "framebuffer dump failed", slog.Any("error", err))
		} else {
			g.log.LogAttrs(context.Background(), slog.LevelInfo, "framebuffer dumped", slog.String("file", fname))
		}
		g.dumpScreen()
		for _, f := range g.client.DumpEGFXSurfaces() {
			g.log.LogAttrs(context.Background(), slog.LevelInfo, "surface dumped", slog.String("file", f))
		}
	}

	// Ctrl+Shift+R: refresh screen from framebuffer (diagnostic)
	if inpututil.IsKeyJustPressed(ebiten.KeyR) &&
		(ebiten.IsKeyPressed(ebiten.KeyControlLeft) || ebiten.IsKeyPressed(ebiten.KeyControlRight)) &&
		(ebiten.IsKeyPressed(ebiten.KeyShiftLeft) || ebiten.IsKeyPressed(ebiten.KeyShiftRight)) {
		g.refreshScreenFromFramebuffer()
	}

	// Ctrl+V: read local clipboard and send to remote before the key event.
	// Try image first; fall through to text if no image is available.
	if inpututil.IsKeyJustPressed(ebiten.KeyV) &&
		(ebiten.IsKeyPressed(ebiten.KeyControlLeft) || ebiten.IsKeyPressed(ebiten.KeyControlRight)) {
		sentImage := false
		if imgData, err := display.ReadClipboardImage(); err == nil && len(imgData) > 0 {
			if err := g.client.SetClipboardImage(imgData); err != nil {
				g.log.LogAttrs(context.Background(), slog.LevelError, "SetClipboardImage failed", slog.Any("error", err))
			} else {
				sentImage = true
			}
		}
		if !sentImage {
			if text, err := display.ReadClipboard(); err == nil && text != "" {
				if err := g.client.SetClipboard(text); err != nil {
					g.log.LogAttrs(context.Background(), slog.LevelError, "SetClipboard failed", slog.Any("error", err))
				}
			}
		}
	}

	// Unicode mode: capture typed characters and send as unicode events.
	// Printable characters go through SendUnicode; control/navigation keys
	// still use scancodes via the keyMap loop below.
	if g.keyboardMode == rdp.KeyboardUnicode {
		for _, r := range ebiten.AppendInputChars(nil) {
			if err := g.client.SendUnicode(uint16(r), true); err != nil {
				g.log.LogAttrs(context.Background(), slog.LevelDebug, "SendUnicode press error", slog.Any("error", err))
			}
			if err := g.client.SendUnicode(uint16(r), false); err != nil {
				g.log.LogAttrs(context.Background(), slog.LevelDebug, "SendUnicode release error", slog.Any("error", err))
			}
		}
	}

	// Keyboard: detect just-pressed, repeat, and just-released keys.
	// Key repeat: initial delay ~500ms (30 ticks @60TPS), then ~33ms (2 ticks).
	// In unicode mode, skip printable keys (handled above via AppendInputChars).
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
		if dur == 1 || (dur > repeatDelay && (dur-repeatDelay)%repeatInterval == 0) {
			sc := entry.scancode
			if err := g.client.SendKeyboard(sc, true); err != nil {
				g.log.LogAttrs(context.Background(), slog.LevelDebug, "SendKeyboard press error", slog.Any("error", err))
			}
		}
		if inpututil.IsKeyJustReleased(key) {
			sc := entry.scancode
			if err := g.client.SendKeyboard(sc, false); err != nil {
				g.log.LogAttrs(context.Background(), slog.LevelDebug, "SendKeyboard release error", slog.Any("error", err))
			}
		}
	}

	// Mouse position
	mx, my := ebiten.CursorPosition()
	if mx != g.lastMouseX || my != g.lastMouseY {
		g.lastMouseX = mx
		g.lastMouseY = my
		if err := g.client.SendMouse(mx, my, pdu.PtrFlagsMove); err != nil {
			g.log.LogAttrs(context.Background(), slog.LevelDebug, "SendMouse move error", slog.Any("error", err))
		}
	}

	// Mouse buttons
	type mouseBtn struct {
		btn  ebiten.MouseButton
		flag uint16
	}
	buttons := [3]mouseBtn{
		{ebiten.MouseButtonLeft, pdu.PtrFlagsButton1},
		{ebiten.MouseButtonRight, pdu.PtrFlagsButton2},
		{ebiten.MouseButtonMiddle, pdu.PtrFlagsButton3},
	}
	for _, b := range buttons {
		if inpututil.IsMouseButtonJustPressed(b.btn) {
			if err := g.client.SendMouse(mx, my, pdu.PtrFlagsDown|b.flag); err != nil {
				g.log.LogAttrs(context.Background(), slog.LevelDebug, "SendMouse press error", slog.Any("error", err))
			}
		}
		if inpututil.IsMouseButtonJustReleased(b.btn) {
			if err := g.client.SendMouse(mx, my, b.flag); err != nil {
				g.log.LogAttrs(context.Background(), slog.LevelDebug, "SendMouse release error", slog.Any("error", err))
			}
		}
	}

	// Mouse wheel
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
		if err := g.client.SendMouseWheel(mx, my, delta, false); err != nil {
			g.log.LogAttrs(context.Background(), slog.LevelDebug, "SendMouseWheel error", slog.Any("error", err))
		}
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
		if err := g.client.SendMouseWheel(mx, my, delta, true); err != nil {
			g.log.LogAttrs(context.Background(), slog.LevelDebug, "SendMouseWheel horizontal error", slog.Any("error", err))
		}
	}

	return nil
}

func (g *game) Draw(screen *ebiten.Image) {
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

// DrawFinalScreen disables all filtering for pixel-perfect output.
func (g *game) DrawFinalScreen(screen ebiten.FinalScreen, offscreen *ebiten.Image, geoM ebiten.GeoM) {
	op := &ebiten.DrawImageOptions{}
	op.GeoM = geoM
	op.Filter = ebiten.FilterNearest
	screen.DrawImage(offscreen, op)
}

func (g *game) Layout(outsideWidth, outsideHeight int) (int, int) {
	return g.width, g.height
}

// LayoutF implements ebiten.LayoutFer for precise HiDPI handling.
// Also detects window resize and triggers RDP session resize with debounce.
func (g *game) LayoutF(outsideWidth, outsideHeight float64) (float64, float64) {
	g.mu.Lock()
	curW, curH := g.width, g.height
	g.mu.Unlock()

	// Short-circuit: if the WM-reported size hasn't changed, skip everything.
	if outsideWidth == g.lastOutW && outsideHeight == g.lastOutH {
		return float64(curW), float64(curH)
	}

	// First call: record the WM size but don't trigger resize — the server
	// already has the correct resolution from MCS Connect. The WM may
	// report ±1 pixel jitter which we don't want to propagate.
	if g.lastOutW == 0 && g.lastOutH == 0 {
		g.lastOutW = outsideWidth
		g.lastOutH = outsideHeight
		return float64(curW), float64(curH)
	}
	g.lastOutW = outsideWidth
	g.lastOutH = outsideHeight

	// Convert logical size to physical pixels
	scale := ebiten.Monitor().DeviceScaleFactor()
	physW := int(math.Round(outsideWidth * scale))
	physH := int(math.Round(outsideHeight * scale))

	// Detect resize — debounce to avoid flooding the server
	if physW > 0 && physH > 0 && (physW != curW || physH != curH) {
		if physW != g.pendingW || physH != g.pendingH {
			g.pendingW = physW
			g.pendingH = physH
			if g.resizeTimer != nil {
				g.resizeTimer.Stop()
			}
			g.resizeTimer = time.AfterFunc(200*time.Millisecond, func() {
				w, h := g.pendingW, g.pendingH
				g.log.LogAttrs(context.Background(), slog.LevelInfo, "window resized, requesting RDP resize", slog.Int("width", w), slog.Int("height", h))
				if err := g.client.Resize(w, h); err != nil {
					g.log.LogAttrs(context.Background(), slog.LevelError, "Resize failed", slog.Any("error", err))
				}
			})
		}
	}

	return float64(curW), float64(curH)
}

// dumpScreen saves the ebiten screen image as a PPM file for debugging.
func (g *game) dumpScreen() {
	if g.screen == nil {
		return
	}
	w, h := g.screen.Bounds().Dx(), g.screen.Bounds().Dy()
	pix := make([]byte, w*h*4)
	g.screen.ReadPixels(pix)
	fname := "screen_" + strconv.Itoa(w) + "x" + strconv.Itoa(h) + ".ppm"
	if err := display.WritePPM(fname, pix, w, h); err != nil {
		g.log.LogAttrs(context.Background(), slog.LevelError, "screen dump failed", slog.Any("error", err))
		return
	}
	g.log.LogAttrs(context.Background(), slog.LevelInfo, "screen dumped", slog.String("file", fname))
}

func (g *game) refreshScreenFromFramebuffer() {
	if g.screen == nil {
		return
	}
	pix, w, h := g.client.FramebufferTopDown()
	if pix == nil {
		return
	}
	sw, sh := g.screen.Bounds().Dx(), g.screen.Bounds().Dy()
	if w != sw || h != sh {
		g.log.LogAttrs(context.Background(), slog.LevelError, "refresh: size mismatch",
			slog.Int("fbW", w), slog.Int("fbH", h), slog.Int("scrW", sw), slog.Int("scrH", sh))
		return
	}
	g.mu.Lock()
	g.screen.WritePixels(pix)
	g.mu.Unlock()
	g.log.LogAttrs(context.Background(), slog.LevelInfo, "screen refreshed from framebuffer",
		slog.Int("w", w), slog.Int("h", h))
}
