// gopher-rdp is a pure Go RDP client.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"syscall"
	"time"

	rdp "github.com/bouncyball-git/gopher-rdp"
	"github.com/bouncyball-git/gopher-rdp/display/web"
)

const version = "0.9.22"


// optionalString implements flag.Value as a boolean-style flag that also
// accepts an optional string value. Passing -log-file sets it to the default
// "gopher-rdp.log"; passing -log-file=name.log sets it to "name.log".
type optionalString struct {
	val string
	set bool
}

func (o *optionalString) IsBoolFlag() bool { return true }
func (o *optionalString) String() string   { return o.val }
func (o *optionalString) Set(s string) error {
	o.set = true
	o.val = s
	return nil
}

// audioFlag implements flag.Value as a boolean-style flag that also
// accepts comma-separated suboptions. Passing -audio-out sets defaults;
// passing -audio-out=stereo,hirate,15ms parses tokens.
//
// Tokens: stereo/mono, hirate/lorate, Nms, pcm, all-digits = exact Hz.
type audioFlag struct {
	cfg    rdp.AudioConfig
	set    bool
	remote bool // "remote" token: audio plays on server
}

func (a *audioFlag) IsBoolFlag() bool { return true }
func (a *audioFlag) String() string   { return "" }
func (a *audioFlag) Set(s string) error {
	a.set = true
	if s == "true" {
		return nil // keep defaults
	}
	if s == "remote" {
		a.remote = true
		return nil
	}
	for _, tok := range strings.Split(s, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		switch {
		case tok == "stereo":
			a.cfg.Stereo = true
		case tok == "mono":
			a.cfg.Stereo = false
		case tok == "hirate":
			a.cfg.MinRate = 44100
		case tok == "lorate":
			a.cfg.MinRate = 0
		case tok == "pcm":
			a.cfg.PCMOnly = true
		case strings.HasSuffix(tok, "ms"):
			n, err := strconv.Atoi(tok[:len(tok)-2])
			if err != nil || n < 0 {
				return fmt.Errorf("invalid buffer size: %q", tok)
			}
			a.cfg.BufMs = n
		default:
			// All-digits: exact sample rate in Hz
			n, err := strconv.Atoi(tok)
			if err != nil {
				return fmt.Errorf("unknown audio option: %q", tok)
			}
			a.cfg.MinRate = uint32(n)
		}
	}
	return nil
}

func (a *audioFlag) config() *rdp.AudioConfig {
	if !a.set || a.remote {
		return nil
	}
	c := a.cfg // copy
	return &c
}

// displayFlag parses -display N[,P] where N is display count and P is primary
// index (default 0). All display resolutions are auto-detected from the browser.
type displayFlag struct {
	count   int
	primary int
	set     bool
}

func (d *displayFlag) String() string { return "" }
func (d *displayFlag) Set(s string) error {
	d.set = true
	d.primary = 0
	// Parse "N,P" or "N"
	if idx := strings.IndexByte(s, ','); idx >= 0 {
		p, err := strconv.Atoi(s[idx+1:])
		if err != nil || p < 0 {
			return fmt.Errorf("display primary index: %q", s[idx+1:])
		}
		d.primary = p
		s = s[:idx]
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return fmt.Errorf("display format: N[,P] (e.g. 3 or 3,1)")
	}
	if d.primary >= n {
		return fmt.Errorf("primary index %d exceeds display count %d", d.primary, n)
	}
	d.count = n
	return nil
}

// positionMonitors auto-positions monitors left-to-right. Monitors with
// Width=0 (auto-detect) are skipped during positioning — they get positioned
// dynamically when their browser tab connects.
func positionMonitors(monitors []rdp.MonitorConfig) {
	xPos := 0
	for i := range monitors {
		monitors[i].X = xPos
		monitors[i].Y = 0
		xPos += monitors[i].Width // Width=0 for auto-detect → no advance
	}
}

// monitorBounds returns the bounding box of all monitors with known resolutions.
// Returns default 1600x900 if no monitors have known resolutions.
func monitorBounds(monitors []rdp.MonitorConfig) (int, int) {
	var totalW, maxH int
	for _, m := range monitors {
		if r := m.X + m.Width; r > totalW {
			totalW = r
		}
		if b := m.Y + m.Height; b > maxH {
			maxH = b
		}
	}
	if totalW == 0 {
		totalW = 1600
	}
	if maxH == 0 {
		maxH = 900
	}
	return totalW, maxH
}

// driveFlag collects -drive flags.
// Format: "name:/local/path", "name:/local/path:ro",
// or on Windows: "name:X:\path", "name:X:\path:ro".
type driveFlag []rdp.DriveRedirect

func (d *driveFlag) String() string { return "" }
func (d *driveFlag) Set(s string) error {
	// Split on first ":" to get the drive letter name.
	idx := strings.IndexByte(s, ':')
	if idx <= 0 || idx >= len(s)-1 {
		return fmt.Errorf("drive format: name:/path or name:/path:ro")
	}
	name := s[:idx]
	rest := s[idx+1:]

	// Handle Windows paths like "X:\foo" — rejoin the drive letter colon.
	if len(rest) >= 2 && rest[1] == ':' && ((rest[0] >= 'A' && rest[0] <= 'Z') || (rest[0] >= 'a' && rest[0] <= 'z')) {
		// rest = "X:\foo" or "X:\foo:ro"
	}

	// Check for trailing ":ro"
	var readOnly bool
	if strings.HasSuffix(rest, ":ro") {
		readOnly = true
		rest = rest[:len(rest)-3]
	}

	if rest == "" {
		return fmt.Errorf("drive format: name:/path or name:/path:ro")
	}

	dr := rdp.DriveRedirect{Name: name, LocalPath: rest, ReadOnly: readOnly}
	*d = append(*d, dr)
	return nil
}

// serialFlag collects -serial flags. Format: "name:/dev/ttyUSB0"
type serialFlag []rdp.SerialRedirect

func (sf *serialFlag) String() string { return "" }
func (sf *serialFlag) Set(s string) error {
	idx := strings.IndexByte(s, ':')
	if idx <= 0 || idx >= len(s)-1 {
		return fmt.Errorf("serial format: name:/dev/path (e.g. COM3:/dev/ttyUSB0)")
	}
	name := s[:idx]
	path := s[idx+1:]
	if path == "" {
		return fmt.Errorf("serial format: name:/dev/path")
	}
	*sf = append(*sf, rdp.SerialRedirect{Name: name, Path: path})
	return nil
}

// parallelFlag collects -parallel flags. Format: "name:/dev/lp0"
type parallelFlag []rdp.ParallelRedirect

func (pf *parallelFlag) String() string { return "" }
func (pf *parallelFlag) Set(s string) error {
	idx := strings.IndexByte(s, ':')
	if idx <= 0 || idx >= len(s)-1 {
		return fmt.Errorf("parallel format: name:/dev/path (e.g. LPT1:/dev/lp0)")
	}
	name := s[:idx]
	path := s[idx+1:]
	if path == "" {
		return fmt.Errorf("parallel format: name:/dev/path")
	}
	*pf = append(*pf, rdp.ParallelRedirect{Name: name, Path: path})
	return nil
}

// printerFlag collects -printer flags.
// Format: "name:/output/dir[:driver=X][:ipp=URL][:default]"
// or IPP-only: "name:ipp=URL[:driver=X][:default]"
type printerFlag []rdp.PrinterRedirect

func (pf *printerFlag) String() string { return "" }
func (pf *printerFlag) Set(s string) error {
	idx := strings.IndexByte(s, ':')
	if idx <= 0 || idx >= len(s)-1 {
		return fmt.Errorf("printer format: name:/output/dir[:driver=X][:ipp=URL][:default]")
	}
	name := s[:idx]
	rest := s[idx+1:]

	var driverName string
	var ippURL string
	var isDefault bool

	// Extract ipp=URL first — URLs contain colons that break suffix parsing.
	// Look for ":ipp=" or leading "ipp=" in rest.
	if strings.HasPrefix(rest, "ipp=") {
		// IPP-only: "name:ipp=URL[:driver=X][:default]"
		ippVal := rest[4:]
		rest = ""
		// Strip trailing :default and :driver=X from the URL
		for {
			if strings.HasSuffix(ippVal, ":default") {
				isDefault = true
				ippVal = ippVal[:len(ippVal)-8]
			} else if i := strings.LastIndex(ippVal, ":driver="); i >= 0 && !strings.Contains(ippVal[i+8:], ":") {
				driverName = ippVal[i+8:]
				ippVal = ippVal[:i]
			} else {
				break
			}
		}
		ippURL = ippVal
	} else if i := strings.Index(rest, ":ipp="); i >= 0 {
		// Mixed: "name:/output/dir:ipp=URL[:driver=X][:default]"
		ippVal := rest[i+5:]
		rest = rest[:i]
		// Strip trailing :default and :driver=X from the URL
		for {
			if strings.HasSuffix(ippVal, ":default") {
				isDefault = true
				ippVal = ippVal[:len(ippVal)-8]
			} else if j := strings.LastIndex(ippVal, ":driver="); j >= 0 && !strings.Contains(ippVal[j+8:], ":") {
				driverName = ippVal[j+8:]
				ippVal = ippVal[:j]
			} else {
				break
			}
		}
		ippURL = ippVal
		// Also parse :driver= and :default that appear before :ipp=
		for {
			last := strings.LastIndexByte(rest, ':')
			if last < 0 {
				break
			}
			suffix := rest[last+1:]
			if strings.HasPrefix(suffix, "driver=") {
				driverName = suffix[7:]
				rest = rest[:last]
			} else if suffix == "default" {
				isDefault = true
				rest = rest[:last]
			} else {
				break
			}
		}
	} else {
		// No IPP — parse suffixes from the right: :driver=X, :default
		for {
			last := strings.LastIndexByte(rest, ':')
			if last < 0 {
				break
			}
			suffix := rest[last+1:]
			if strings.HasPrefix(suffix, "driver=") {
				driverName = suffix[7:]
				rest = rest[:last]
			} else if suffix == "default" {
				isDefault = true
				rest = rest[:last]
			} else {
				break
			}
		}
	}

	// rest is the output dir, or empty for IPP-only
	if rest == "" && ippURL == "" {
		return fmt.Errorf("printer format: name:/output/dir[:driver=X][:ipp=URL][:default]")
	}

	*pf = append(*pf, rdp.PrinterRedirect{
		Name:       name,
		DriverName: driverName,
		OutputDir:  rest,
		IPPURL:     ippURL,
		IsDefault:  isDefault,
	})
	return nil
}

// usbFlag collects -usb flags. Format: "auto", "vid:pid" (hex), or "bus,addr" (decimal).
type usbFlag []rdp.USBRedirect

func (uf *usbFlag) String() string { return "" }
func (uf *usbFlag) Set(s string) error {
	// Auto mode: redirect all devices (HID and hubs excluded)
	if s == "auto" || s == "*" {
		*uf = append(*uf, rdp.USBRedirect{})
		return nil
	}
	// Try vid:pid format first (hex values)
	if idx := strings.IndexByte(s, ':'); idx > 0 {
		vid, err := strconv.ParseUint(s[:idx], 16, 16)
		if err != nil {
			return fmt.Errorf("usb vid:pid - invalid VID %q (use hex, e.g. 1234:5678)", s[:idx])
		}
		pid, err := strconv.ParseUint(s[idx+1:], 16, 16)
		if err != nil {
			return fmt.Errorf("usb vid:pid - invalid PID %q (use hex, e.g. 1234:5678)", s[idx+1:])
		}
		*uf = append(*uf, rdp.USBRedirect{VID: uint16(vid), PID: uint16(pid)})
		return nil
	}
	// Try bus,addr format (decimal values)
	if idx := strings.IndexByte(s, ','); idx > 0 {
		bus, err := strconv.ParseUint(s[:idx], 10, 8)
		if err != nil {
			return fmt.Errorf("usb bus,addr - invalid bus %q (use decimal, e.g. 1,5)", s[:idx])
		}
		addr, err := strconv.ParseUint(s[idx+1:], 10, 8)
		if err != nil {
			return fmt.Errorf("usb bus,addr - invalid addr %q (use decimal, e.g. 1,5)", s[idx+1:])
		}
		*uf = append(*uf, rdp.USBRedirect{BusNum: uint8(bus), DevAddr: uint8(addr)})
		return nil
	}
	return fmt.Errorf("usb format: auto, vid:pid (hex, e.g. 1234:5678), or bus,addr (decimal, e.g. 1,5)")
}

// FilterHandler implements slog.Handler with component filtering and
// custom "[GOPHER-RDP LEVEL] msg key=val" output format.
type FilterHandler struct {
	w          io.Writer
	components map[string]struct{} // nil = allow all
	level      slog.Level
	preAttrs   []slog.Attr // attrs added via WithAttrs
}

// NewFilterHandler creates a FilterHandler. If comps is "all" or empty, all
// components are allowed; otherwise only the comma-separated names pass.
func NewFilterHandler(w io.Writer, comps string, level slog.Level) *FilterHandler {
	var m map[string]struct{}
	comps = strings.ToUpper(strings.TrimSpace(comps))
	if comps != "" && comps != "ALL" {
		m = make(map[string]struct{})
		for _, c := range strings.Split(comps, ",") {
			c = strings.TrimSpace(c)
			if c != "" {
				m[c] = struct{}{}
			}
		}
	}
	return &FilterHandler{w: w, components: m, level: level}
}

func (h *FilterHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level
}

func levelName(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "ERROR"
	case l >= slog.LevelWarn:
		return "WARN"
	case l >= slog.LevelInfo:
		return "INFO"
	case l >= slog.LevelDebug:
		return "DEBUG"
	default:
		return "TRACE"
	}
}

func (h *FilterHandler) Handle(_ context.Context, r slog.Record) error {
	if r.Level < h.level {
		return nil
	}

	// Collect all attrs (pre-attrs from WithAttrs + record attrs)
	allAttrs := make([]slog.Attr, 0, len(h.preAttrs)+r.NumAttrs())
	allAttrs = append(allAttrs, h.preAttrs...)
	r.Attrs(func(a slog.Attr) bool {
		allAttrs = append(allAttrs, a)
		return true
	})

	// Extract component, filter, and separate from other attrs.
	var comp string
	var extras []slog.Attr
	for _, a := range allAttrs {
		if a.Key == "component" {
			comp = a.Value.String()
		} else {
			extras = append(extras, a)
		}
	}
	if h.components != nil {
		if comp == "" {
			return nil
		}
		if _, ok := h.components[comp]; !ok {
			return nil
		}
	}

	// Format: 2006-01-02 15:04:05 [GOPHER-RDP COMP LEVEL] msg, key=val, key=val
	var b strings.Builder
	b.WriteString(r.Time.Format("2006-01-02 15:04:05"))
	b.WriteString(" [GOPHER-RDP ")
	if comp != "" {
		b.WriteString(comp)
		b.WriteByte(' ')
	}
	b.WriteString(levelName(r.Level))
	b.WriteString("] ")
	b.WriteString(r.Message)
	for _, a := range extras {
		b.WriteString(", ")
		b.WriteString(a.Key)
		b.WriteByte('=')
		b.WriteString(a.Value.String())
	}
	b.WriteByte('\n')
	_, err := io.WriteString(h.w, b.String())
	return err
}

func (h *FilterHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	combined := make([]slog.Attr, len(h.preAttrs), len(h.preAttrs)+len(attrs))
	copy(combined, h.preAttrs)
	combined = append(combined, attrs...)
	return &FilterHandler{
		w:          h.w,
		components: h.components,
		level:      h.level,
		preAttrs:   combined,
	}
}

func (h *FilterHandler) WithGroup(name string) slog.Handler {
	// Groups not used in this codebase; pass through as-is
	return h
}

func parseLogLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "all", "trace":
		return rdp.LevelTrace
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default: // "info" or anything else
		return slog.LevelInfo
	}
}

// isBoolStyleFlag returns true for flags that use IsBoolFlag (need -key=value format).
var isBoolStyleFlag = map[string]bool{
	"gui": true, "web": true, "log": true, "log-file": true, "audio-out": true, "audio-in": true, "smartcard": true,
	"auto-reconnect": true,
	"wallpaper": true, "window-drag": true, "menu-animations": true,
	"theming": true, "cursor-shadow": true, "cursor-settings": true,
	"font-smoothing": true, "desktop-composition": true, "disable-visuals": true,
}

// isBareBoolFlag returns true for standard flag.Bool flags.
var isBareBoolFlag = map[string]bool{
	"no-clipboard": true, "admin": true,
	"no-gfx": true, "no-avc": true, "camera": true,
}

// parseConfigFile reads a config file and returns synthetic CLI args.
// Format: key value per line, # comments, blank lines ignored.
func parseConfigFile(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var args []string
	for _, line := range strings.Split(string(data), "\n") {
		// Strip inline comment: find " #" or "\t#"
		if idx := strings.Index(line, " #"); idx >= 0 {
			line = line[:idx]
		}
		if idx := strings.Index(line, "\t#"); idx >= 0 {
			line = line[:idx]
		}

		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}

		// Split on first space/tab → key, value
		var key, value string
		if idx := strings.IndexAny(line, " \t"); idx >= 0 {
			key = line[:idx]
			value = strings.TrimSpace(line[idx+1:])
		} else {
			key = line
		}

		switch {
		case isBoolStyleFlag[key]:
			if value == "" {
				args = append(args, "-"+key)
			} else {
				args = append(args, "-"+key+"="+value)
			}
		case isBareBoolFlag[key]:
			if strings.EqualFold(value, "false") {
				continue
			}
			if value != "" && !strings.EqualFold(value, "true") {
				return nil, fmt.Errorf("flag %q: expected true or false, got %q", key, value)
			}
			args = append(args, "-"+key)
		default:
			args = append(args, "-"+key, value)
		}
	}
	return args, nil
}

func main() {
	// Child process detection: if spawned by the broker for multi-display GUI,
	// run the child Ebiten window and exit.
	runGUIChild()

	// Command line flags
	host := flag.String("host", "", "RDP server hostname or IP (required)")
	port := flag.Int("port", 3389, "RDP server port")
	user := flag.String("user", "", "Username (omit for Windows login screen)")
	pass := flag.String("pass", "", "Password")
	domain := flag.String("domain", "", "Windows domain")
	var displays displayFlag
	flag.Var(&displays, "display", "Display count N[,P], P=primary index (default 0)")
	depth := flag.Int("depth", 32, "Color depth (8, 15, 16, 24, or 32)")
	cookie := flag.String("cookie", "", "Routing cookie")
	wallpaper := flag.Bool("wallpaper", true, "Desktop wallpaper (default true)")
	windowDrag := flag.Bool("window-drag", true, "Show window contents while dragging (default true)")
	menuAnim := flag.Bool("menu-animations", true, "Menu animations (default true)")
	theming := flag.Bool("theming", true, "Visual themes / Aero/Luna (default true)")
	cursorShadow := flag.Bool("cursor-shadow", true, "Cursor shadow (default true)")
	cursorSettings := flag.Bool("cursor-settings", true, "Cursor blink settings (default true)")
	fontSmooth := flag.Bool("font-smoothing", true, "ClearType font smoothing (default true)")
	desktopComp := flag.Bool("desktop-composition", true, "Desktop composition / Aero Glass (default true)")
	disableVisuals := flag.Bool("disable-visuals", false, "Disable all visual effects (default false)")
	noClipboard := flag.Bool("no-clipboard", false, "Disable clipboard redirection")
	var audioOut audioFlag
	audioOut.cfg = rdp.AudioConfig{BufMs: 15, Stereo: true, MinRate: 44100}
	flag.Var(&audioOut, "audio-out", "Audio output: remote, or stereo,mono,hirate,lorate,Nms,pcm,Hz (default stereo,hirate,15ms)")
	var audioIn audioFlag
	audioIn.cfg = rdp.AudioConfig{BufMs: 5}
	flag.Var(&audioIn, "audio-in", "Audio input: stereo,mono,hirate,lorate,Nms,pcm,Hz (default mono,lorate,5ms)")
	var drives driveFlag
	flag.Var(&drives, "drive", "Share local directory as network drive (repeatable): d:/path or d:/path:ro")
	var serials serialFlag
	flag.Var(&serials, "serial", "Redirect serial port (repeatable): name:/dev/path (e.g. COM3:/dev/ttyUSB0)")
	var parallels parallelFlag
	flag.Var(&parallels, "parallel", "Redirect parallel port (repeatable): name:/dev/path (e.g. LPT1:/dev/lp0)")
	var printers printerFlag
	flag.Var(&printers, "printer", "Redirect printer (repeatable): name:/output/dir[:driver=X][:ipp=URL][:default]")
	var smartcardFlag optionalString
	flag.Var(&smartcardFlag, "smartcard", "Enable smartcard redirection (optional: socket path)")
	var usbDevices usbFlag
	flag.Var(&usbDevices, "usb", "Redirect USB device (repeatable): vid:pid (hex) or bus,addr (decimal)")
	camera := flag.Bool("camera", false, "Enable webcam redirection via RDPECAM (web viewer only)")
	admin := flag.Bool("admin", false, "Connect to the console (admin) session")
	heartbeat := flag.Int("heartbeat", 10, "Heartbeat timeout in seconds, 0 to disable (default 10)")
	autoReconnect := flag.Bool("auto-reconnect", true, "Enable automatic reconnection (default true)")
	reconnectAttempts := flag.Int("reconnect-attempts", 0, "Max reconnect attempts (0 = unlimited)")
	desktopScale := flag.Int("desktop-scale", 0, "DPI scale: 100/125/150/175/200/225/250/300/350/400/450/500 (0 to disable)")
	deviceScale := flag.Int("device-scale", 0, "Device scale factor (100, 140, or 180)")
	noGfx := flag.Bool("no-gfx", false, "Disable RDPGFX graphics pipeline (EGFX)")
	noAvc := flag.Bool("no-avc", false, "Disable H.264/AVC codec in EGFX (force RemoteFX/ClearCodec)")
	keyboard := flag.String("keyboard", "scancode", "Keyboard input mode: scancode or unicode")
	var guiFlag optionalString
	flag.Var(&guiFlag, "gui", "Graphical desktop viewer (optional: -gui WxH, e.g. -gui 1920x1080)")
	var webFlag optionalString
	flag.Var(&webFlag, "web", "Start web viewer (optional port, default 8080)")
	var logFlag optionalString
	flag.Var(&logFlag, "log", "Enable logging (optional: comma-separated components, e.g. RDP,EGFX)")
	logLevel := flag.String("log-level", "info", "Minimum log level: all, trace, debug, info, warn, error")
	var logFileName optionalString
	flag.Var(&logFileName, "log-file", "Write logs to file (default gopher-rdp.log)")
	cpuProfile := flag.String("cpu-profile", "", "Write CPU profile to file (e.g. cpu.prof)")
	memProfile := flag.String("mem-profile", "", "Write memory profile to file on exit (e.g. mem.prof)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "gopher-rdp v%s %s/%s\n\n", version, runtime.GOOS, runtime.GOARCH)
		fmt.Fprintf(os.Stderr, "Usage: gopher-rdp [options]\n\n")
		fmt.Fprintf(os.Stderr, "General:\n")
		fmt.Fprintf(os.Stderr, "  -version                 Show version and exit\n")
		fmt.Fprintf(os.Stderr, "  -help, -usage            Show this help message\n")
		fmt.Fprintf(os.Stderr, "  -config file             Load options from config file\n")
		fmt.Fprintf(os.Stderr, "                           Default: gopher-rdp.conf in current directory\n")
		fmt.Fprintf(os.Stderr, "\nConnection:\n")
		fmt.Fprintf(os.Stderr, "  -host string             RDP server hostname or IP (required)\n")
		fmt.Fprintf(os.Stderr, "  -port int                RDP server port (default 3389)\n")
		fmt.Fprintf(os.Stderr, "  -user string             Username (omit for Windows login screen)\n")
		fmt.Fprintf(os.Stderr, "  -pass string             Password (omit for Windows login screen)\n")
		fmt.Fprintf(os.Stderr, "  -domain string           Windows domain\n")
		fmt.Fprintf(os.Stderr, "  -cookie string           Routing cookie\n")
		fmt.Fprintf(os.Stderr, "\nDisplay:\n")
		fmt.Fprintf(os.Stderr, "  -display N[,P]           Display count, P=primary index (default 0, -web or -gui)\n")
		fmt.Fprintf(os.Stderr, "                           Resolution auto-detected from each browser tab\n")
		fmt.Fprintf(os.Stderr, "                           Example: -display 3,1 (3 displays, second is primary)\n")
		fmt.Fprintf(os.Stderr, "  -depth int               Color depth: 8, 15, 16, 24, 32 (default 32)\n")
		fmt.Fprintf(os.Stderr, "  -desktop-scale int       DPI zoom percent, 100-500 (default 0 = off)\n")
		fmt.Fprintf(os.Stderr, "  -device-scale int        Physical DPI tier: 100=standard, 140=high, 180=very high\n")
		fmt.Fprintf(os.Stderr, "                           (used with -desktop-scale for server-side UI scaling)\n")
		fmt.Fprintf(os.Stderr, "  -no-gfx                  Disable RDPGFX graphics pipeline (EGFX)\n")
		fmt.Fprintf(os.Stderr, "  -no-avc                  Disable H.264/AVC codec (force RemoteFX/ClearCodec)\n")
		fmt.Fprintf(os.Stderr, "  -keyboard string         Keyboard input mode: scancode or unicode (default scancode)\n")
		fmt.Fprintf(os.Stderr, "                           scancode = physical keys (remote layout determines chars)\n")
		fmt.Fprintf(os.Stderr, "                           unicode  = typed characters (local layout determines chars)\n")
		fmt.Fprintf(os.Stderr, "  -gui [WxH]               Graphical desktop viewer (default 1600x900)\n")
		fmt.Fprintf(os.Stderr, "  -web [port]              Web viewer (default port 8080)\n")
		fmt.Fprintf(os.Stderr, "\nSession:\n")
		fmt.Fprintf(os.Stderr, "  -admin                   Connect to the console (admin) session\n")
		fmt.Fprintf(os.Stderr, "  -heartbeat int           Heartbeat timeout in seconds, 0 to disable (default 10)\n")
		fmt.Fprintf(os.Stderr, "  -auto-reconnect          Enable automatic reconnection (default true)\n")
		fmt.Fprintf(os.Stderr, "  -reconnect-attempts int  Max reconnect attempts, 0 = unlimited (default 0)\n")
		fmt.Fprintf(os.Stderr, "\nVisuals (all enabled by default; use -flag=false to disable):\n")
		fmt.Fprintf(os.Stderr, "  -wallpaper               Desktop wallpaper\n")
		fmt.Fprintf(os.Stderr, "  -window-drag             Show window contents while dragging\n")
		fmt.Fprintf(os.Stderr, "  -menu-animations         Menu animations\n")
		fmt.Fprintf(os.Stderr, "  -theming                 Visual themes (Aero/Luna)\n")
		fmt.Fprintf(os.Stderr, "  -cursor-shadow           Cursor shadow\n")
		fmt.Fprintf(os.Stderr, "  -cursor-settings         Cursor blink settings\n")
		fmt.Fprintf(os.Stderr, "  -font-smoothing          ClearType font smoothing\n")
		fmt.Fprintf(os.Stderr, "  -desktop-composition     Desktop composition / Aero Glass\n")
		fmt.Fprintf(os.Stderr, "  -disable-visuals         Disable all visual effects\n")
		fmt.Fprintf(os.Stderr, "\nRedirect (all on by default):\n")
		fmt.Fprintf(os.Stderr, "  -no-clipboard            Disable clipboard redirection\n")
		fmt.Fprintf(os.Stderr, "  -audio-out [opts]        Audio output (omit = no audio at all)\n")
		fmt.Fprintf(os.Stderr, "                           opts: remote, or stereo,mono,hirate,lorate,Nms,pcm,Hz\n")
		fmt.Fprintf(os.Stderr, "  -audio-in [opts]         Audio input (default mono,lorate,5ms, -web only)\n")
		fmt.Fprintf(os.Stderr, "                           opts: stereo,mono,hirate,lorate,Nms,pcm,Hz\n")
		fmt.Fprintf(os.Stderr, "  Audio examples:\n")
		fmt.Fprintf(os.Stderr, "                                     (omit -audio-out = no audio at all)\n")
		fmt.Fprintf(os.Stderr, "    -audio-out                       redirect to client: stereo, 44100+ Hz, 15ms\n")
		fmt.Fprintf(os.Stderr, "    -audio-out remote                play audio on server (no redirection)\n")
		fmt.Fprintf(os.Stderr, "    -audio-out mono,lorate,30ms      redirect: mono, any sample rate, 30ms buffer\n")
		fmt.Fprintf(os.Stderr, "    -audio-out pcm,48000             redirect: PCM only (no ADPCM), exact 48000 Hz\n")
		fmt.Fprintf(os.Stderr, "    -audio-in                        defaults: mono, any rate, 5ms buffer\n")
		fmt.Fprintf(os.Stderr, "    -audio-in stereo,hirate,10ms     stereo, 44100+ Hz, 10ms buffer\n")
		fmt.Fprintf(os.Stderr, "    -audio-out -audio-in             output + microphone together\n\n")
		fmt.Fprintf(os.Stderr, "  -drive d:/home/user/dir   Share local directory as drive D: (repeatable)\n")
		fmt.Fprintf(os.Stderr, "  -drive d:/tmp:ro         Share as read-only\n")
		fmt.Fprintf(os.Stderr, "  -drive d:C:\\Users\\me     On Windows, share a local Windows directory\n")
		fmt.Fprintf(os.Stderr, "  -serial name:/dev/path   Redirect serial port (repeatable)\n")
		fmt.Fprintf(os.Stderr, "                           Example: -serial COM3:/dev/ttyUSB0\n")
		fmt.Fprintf(os.Stderr, "  -parallel name:/dev/path Redirect parallel port (repeatable)\n")
		fmt.Fprintf(os.Stderr, "                           Example: -parallel LPT1:/dev/lp0\n")
		fmt.Fprintf(os.Stderr, "  -printer name:/output/dir  Redirect printer, save jobs to directory (repeatable)\n")
		fmt.Fprintf(os.Stderr, "                           Optional: :driver=X (default MS Publisher Imagesetter)\n")
		fmt.Fprintf(os.Stderr, "                           Optional: :ipp=URL (submit jobs via IPP)\n")
		fmt.Fprintf(os.Stderr, "                           Optional: :default (announce as default printer)\n")
		fmt.Fprintf(os.Stderr, "                           IPP-only: -printer MyPrn:ipp=http://127.0.0.1:631/printers/hp\n")
		fmt.Fprintf(os.Stderr, "                           Both:     -printer MyPrn:/tmp/print:ipp=http://127.0.0.1:631/printers/hp\n")
		fmt.Fprintf(os.Stderr, "                           Example:  -printer MyPrn:/tmp/print:driver=HP LaserJet:default\n")
		fmt.Fprintf(os.Stderr, "  -smartcard [socket]      Enable smartcard redirection via pcsclite\n")
		fmt.Fprintf(os.Stderr, "                           Default socket: /var/run/pcscd/pcscd.comm\n")
		fmt.Fprintf(os.Stderr, "  -usb auto                Redirect all USB devices (HID and hubs excluded)\n")
		fmt.Fprintf(os.Stderr, "  -usb vid:pid             Redirect USB device by vendor:product ID (hex, repeatable)\n")
		fmt.Fprintf(os.Stderr, "                           Example: -usb 1234:5678\n")
		fmt.Fprintf(os.Stderr, "  -usb bus,addr            Redirect USB device by bus number and address (decimal)\n")
		fmt.Fprintf(os.Stderr, "                           Example: -usb 1,5 (bus 1, device address 5)\n")
		fmt.Fprintf(os.Stderr, "  -camera                  Enable webcam redirection via RDPECAM (web viewer only)\n")
		fmt.Fprintf(os.Stderr, "\nLogging (disabled by default):\n")
		fmt.Fprintf(os.Stderr, "  -log [COMP,COMP,...]     Enable logging (default: all components, info, stderr)\n")
		fmt.Fprintf(os.Stderr, "  -log-level string        Minimum level (default \"info\"):\n")
		fmt.Fprintf(os.Stderr, "                             all/trace  per-tile/pixel dumps, codec coefficients\n")
		fmt.Fprintf(os.Stderr, "                             debug      protocol detail: PDU types, codec headers, sizes\n")
		fmt.Fprintf(os.Stderr, "                             info       connection lifecycle milestones\n")
		fmt.Fprintf(os.Stderr, "                             warn       unexpected but recoverable issues\n")
		fmt.Fprintf(os.Stderr, "                             error      failures affecting the session\n")
		fmt.Fprintf(os.Stderr, "  -log-file [name.log]     Write logs to file (default gopher-rdp.log)\n")
		fmt.Fprintf(os.Stderr, "\n  Components: RDP, TPKT, NLA, X224, MCS, SEC, LIC, PDU, FASTPATH, POINTER,\n")
		fmt.Fprintf(os.Stderr, "    EGFX, CLEARCODEC, DVC, DISP, CLIPRDR, RDPSND, AUDIN, RDPDR, URBDRC, RDPECAM, BITMAP, GUI, WEB\n")
		fmt.Fprintf(os.Stderr, "\n  Examples:\n")
		fmt.Fprintf(os.Stderr, "    -log                          all components, info level, stderr\n")
		fmt.Fprintf(os.Stderr, "    -log -log-level debug         all components, debug level\n")
		fmt.Fprintf(os.Stderr, "    -log NLA -log-level debug     NLA/CredSSP auth only, debug level\n")
		fmt.Fprintf(os.Stderr, "    -log MCS,PDU                  MCS + PDU, info level\n")
		fmt.Fprintf(os.Stderr, "    -log -log-file                all to gopher-rdp.log\n")
		fmt.Fprintf(os.Stderr, "    -log -log-file session.log    all to custom file\n")
		fmt.Fprintf(os.Stderr, "\nProfiling:\n")
		fmt.Fprintf(os.Stderr, "  -cpu-profile file        Write CPU profile to file\n")
		fmt.Fprintf(os.Stderr, "  -mem-profile file        Write heap profile to file on exit\n")
		fmt.Fprintf(os.Stderr, "                           pprof server always on http://localhost:6060\n")
		fmt.Fprintf(os.Stderr, "\nConfig file format:\n")
		fmt.Fprintf(os.Stderr, "  key value per line (space/tab separated), keys = flag names without -\n")
		fmt.Fprintf(os.Stderr, "  # comments (full-line or inline after whitespace), blank lines ignored\n")
		fmt.Fprintf(os.Stderr, "  Repeatable keys (drive, serial, parallel, printer, usb) accumulate from config + CLI\n")
		fmt.Fprintf(os.Stderr, "  Boolean keys: \"key true\" or bare \"key\" = enabled, \"key false\" = disabled\n")
		fmt.Fprintf(os.Stderr, "  CLI flags override config values for scalar options\n")
		fmt.Fprintf(os.Stderr, "\n  Example (gopher-rdp.conf):\n")
		fmt.Fprintf(os.Stderr, "    host 10.0.0.1\n")
		fmt.Fprintf(os.Stderr, "    user admin\n")
		fmt.Fprintf(os.Stderr, "    depth 32\n")
		fmt.Fprintf(os.Stderr, "    gui 1920x1080\n")
		fmt.Fprintf(os.Stderr, "    wallpaper false\n")
		fmt.Fprintf(os.Stderr, "    desktop-composition false\n")
		fmt.Fprintf(os.Stderr, "    drive share:/home/user/dir\n")
		fmt.Fprintf(os.Stderr, "    drive tmp:/tmp:ro\n")
		fmt.Fprintf(os.Stderr, "    log RDPDR,EGFX\n")
	}

	// --- Pre-scan os.Args for -help/-usage/-config before flag.Parse ---
	var configPath string
	hasCliArgs := false // true if any non-config/help flags present
	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		switch {
		case arg == "-version" || arg == "--version":
			fmt.Printf("gopher-rdp v%s %s/%s\n", version, runtime.GOOS, runtime.GOARCH)
			os.Exit(0)
		case arg == "-help" || arg == "--help" || arg == "-usage" || arg == "--usage":
			flag.Usage()
			os.Exit(0)
		case arg == "-config" && i+1 < len(os.Args):
			configPath = os.Args[i+1]
			os.Args = append(os.Args[:i], os.Args[i+2:]...)
			i-- // re-examine position
		case strings.HasPrefix(arg, "-config="):
			configPath = arg[len("-config="):]
			os.Args = append(os.Args[:i], os.Args[i+1:]...)
			i--
		default:
			hasCliArgs = true
		}
	}
	// If no -config, try gopher-rdp.conf in the executable's directory or
	// current working directory. Config args are prepended so CLI flags override.
	if configPath == "" {
		// Try directory of the executable first, then current working directory
		if exe, err := os.Executable(); err == nil {
			def := filepath.Join(filepath.Dir(exe), "gopher-rdp.conf")
			if _, err := os.Stat(def); err == nil {
				configPath = def
			}
		}
		if configPath == "" {
			if _, err := os.Stat("gopher-rdp.conf"); err == nil {
				configPath = "gopher-rdp.conf"
			}
		}
	}
	// No config file and no CLI flags → tell user what's needed
	if configPath == "" && !hasCliArgs {
		fmt.Fprintf(os.Stderr, "No config file or CLI options specified.\n")
		fmt.Fprintf(os.Stderr, "Place gopher-rdp.conf in the current directory, or use -config file, or pass CLI flags.\n")
		fmt.Fprintf(os.Stderr, "Run with -help for usage.\n")
		os.Exit(1)
	}
	// Parse config and prepend args
	if configPath != "" {
		cfgArgs, err := parseConfigFile(configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "config file error: %v\n", err)
			os.Exit(1)
		}
		// Prepend config args after os.Args[0], before CLI args — CLI wins on scalar flags
		newArgs := make([]string, 0, 1+len(cfgArgs)+len(os.Args)-1)
		newArgs = append(newArgs, os.Args[0])
		newArgs = append(newArgs, cfgArgs...)
		newArgs = append(newArgs, os.Args[1:]...)
		os.Args = newArgs
	}

	// Pre-process os.Args: rewrite "-gui WxH" and "-log COMP" (space-separated)
	// into "-gui=WxH" / "-log=COMP" so flag.Parse sees them as single values.
	// Without this, IsBoolFlag causes the next arg to be treated as positional.
	for i := 1; i < len(os.Args)-1; i++ {
		next := os.Args[i+1]
		if strings.HasPrefix(next, "-") {
			continue
		}
		rewrite := false
		switch os.Args[i] {
		case "-gui":
			rewrite = strings.Contains(next, "x")
		case "-log":
			rewrite = true
		case "-log-file":
			rewrite = true
		case "-audio-out", "-audio-in":
			rewrite = true
		case "-smartcard":
			rewrite = !strings.HasPrefix(next, "-")
		}
		if rewrite {
			os.Args[i] = os.Args[i] + "=" + next
			os.Args = append(os.Args[:i+1], os.Args[i+2:]...)
		}
	}

	flag.Parse()

	// cliLog prints a timestamped CLI message to stderr.
	cliLog := func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		fmt.Fprintf(os.Stderr, "%s [GOPHER-RDP CLI] %s\n", time.Now().Format("2006-01-02 15:04:05"), msg)
	}

	// Start pprof server for profiling (go tool pprof http://localhost:6060/debug/pprof/heap)
	go http.ListenAndServe("localhost:6060", nil)

	// CPU profiling
	if *cpuProfile != "" {
		f, err := os.Create(*cpuProfile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "could not create CPU profile: %v\n", err)
			os.Exit(1)
		}
		pprof.StartCPUProfile(f)
		defer func() {
			pprof.StopCPUProfile()
			f.Close()
			fmt.Fprintf(os.Stderr, "CPU profile written to %s\n", *cpuProfile)
		}()
	}

	// Memory profiling (written on exit)
	if *memProfile != "" {
		defer func() {
			f, err := os.Create(*memProfile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "could not create memory profile: %v\n", err)
				return
			}
			runtime.GC()
			pprof.WriteHeapProfile(f)
			f.Close()
			fmt.Fprintf(os.Stderr, "Memory profile written to %s\n", *memProfile)
		}()
	}

	// Logging: silent by default, enabled with -log
	var logger *slog.Logger
	if !logFlag.set {
		logger = slog.New(slog.DiscardHandler)
	} else {
		var logOutput io.Writer = os.Stderr
		if logFileName.set {
			name := logFileName.val
			if name == "" || name == "true" {
				name = "gopher-rdp.log"
			}
			f, err := os.Create(name)
			if err != nil {
				cliLog("Failed to create %s: %v", name, err)
				os.Exit(1)
			}
			defer f.Close()
			logOutput = f
			// Expose log file path so GUI child processes write there too.
			os.Setenv("GOPHER_RDP_LOG_FILE", name)
		}
		level := parseLogLevel(*logLevel)
		comps := logFlag.val // "" means all, "RDP,EGFX" means filter
		if comps == "" || comps == "true" {
			comps = "all"
		}
		handler := NewFilterHandler(logOutput, comps, level)
		logger = slog.New(handler)
	}

	if *host == "" {
		cliLog("Error: -host is required (set in config file or pass -host)")
		os.Exit(1)
	}

	// -disable-visuals overrides all visual flags to false.
	if *disableVisuals {
		*wallpaper = false
		*windowDrag = false
		*menuAnim = false
		*theming = false
		*cursorShadow = false
		*cursorSettings = false
		*fontSmooth = false
		*desktopComp = false
	}

	// Build options.
	var kbMode rdp.KeyboardMode
	switch strings.ToLower(*keyboard) {
	case "scancode", "":
		kbMode = rdp.KeyboardScancode
	case "unicode":
		kbMode = rdp.KeyboardUnicode
	default:
		cliLog("Error: -keyboard must be 'scancode' or 'unicode'")
		os.Exit(1)
	}

	// Build monitor topology from -display flag.
	var monitors []rdp.MonitorConfig
	if displays.set {
		for i := range displays.count {
			monitors = append(monitors, rdp.MonitorConfig{Primary: i == displays.primary})
		}
	} else {
		monitors = []rdp.MonitorConfig{{Width: 1600, Height: 900, Primary: true}}
	}
	positionMonitors(monitors)
	totalW, maxH := monitorBounds(monitors)

	opts := &rdp.Options{
		Logger:               logger,
		Host:                 *host,
		Port:                 *port,
		Username:             *user,
		Password:             *pass,
		Domain:               *domain,
		Width:                uint16(totalW),
		Height:               uint16(maxH),
		Depth:                uint16(*depth),
		DesktopScaleFactor:   uint32(*desktopScale),
		DeviceScaleFactor:    uint32(*deviceScale),
		Cookie:               *cookie,
		ConsoleSession:       *admin,
		Clipboard:            !*noClipboard,
		AudioOut:             audioOut.config(),
		AudioIn:              audioIn.config(),
		RemoteAudio:          audioOut.remote,
		Drives:               []rdp.DriveRedirect(drives),
		Serials:              []rdp.SerialRedirect(serials),
		Parallels:            []rdp.ParallelRedirect(parallels),
		Printers:             []rdp.PrinterRedirect(printers),
		USBDevices:           []rdp.USBRedirect(usbDevices),
		Wallpaper:            *wallpaper,
		FullWindowDrag:       *windowDrag,
		MenuAnimations:       *menuAnim,
		Theming:              *theming,
		CursorShadow:         *cursorShadow,
		CursorSettings:       *cursorSettings,
		FontSmoothing:        *fontSmooth,
		DesktopComposition:   *desktopComp,
		GFX:                  !*noGfx,
		NoAVC:                *noAvc,
		Camera:               *camera,
		KeyboardMode:         kbMode,
		HeartbeatTimeout:     time.Duration(*heartbeat) * time.Second,
		AutoReconnect:        *autoReconnect,
		MaxReconnectAttempts: *reconnectAttempts,
	}
	// Smartcard redirection
	if smartcardFlag.set {
		sockPath := smartcardFlag.val
		if sockPath == "" || sockPath == "true" {
			sockPath = "/var/run/pcscd/pcscd.comm"
		}
		opts.Smartcard = &rdp.SmartcardRedirect{SocketPath: sockPath}
	}
	// Set multi-monitor topology (skip for single monitor — backward compatible).
	// opts.Monitors uses MS-RDPBCGR coordinates (primary at 0,0).
	if len(monitors) > 1 {
		proto := make([]rdp.MonitorConfig, len(monitors))
		copy(proto, monitors)
		var offX, offY int
		for _, m := range proto {
			if m.Primary {
				offX, offY = m.X, m.Y
				break
			}
		}
		for i := range proto {
			proto[i].X -= offX
			proto[i].Y -= offY
		}
		opts.Monitors = proto
	}
	// Resolve web port: bare -web defaults to 8080.
	webAddr := webFlag.val
	if webFlag.set && (webAddr == "" || webAddr == "true") {
		webAddr = "8080"
	}

	// -display requires -web or -gui mode.
	if displays.set && webAddr == "" && !guiFlag.set {
		cliLog("Error: -display requires -web or -gui mode")
		os.Exit(1)
	}

	// -audio-in is not supported with -gui (microphone input requires the web viewer).
	if audioIn.set && guiFlag.set {
		cliLog("Error: -audio-in is not supported with -gui (use -web for microphone input)")
		os.Exit(1)
	}

	// -gui requires the gui build tag.
	if guiFlag.set && !guiAvailable {
		cliLog("Error: -gui requires a build with GUI support (build with -tags gui)")
		os.Exit(1)
	}

	// Web viewer mode
	if webAddr != "" {
		addr := web.ListenAddr(webAddr)
		if len(monitors) > 1 {
			// Multi-display: Dispatcher manages RDP lifecycle.
			// RDP connects when the first browser tab connects.
			monRects := make([]web.MonitorRect, len(monitors))
			for i, m := range monitors {
				monRects[i] = web.MonitorRect{
					Index: i, X: m.X, Y: m.Y,
					Width: m.Width, Height: m.Height, Primary: m.Primary,
				}
			}
			d := web.NewDispatcher(opts, monRects, logger, kbMode)
			handler := web.NewMultiMonitorHandler(d)
			cliLog("Web viewer at http://localhost:%s (%d displays)", webAddr, len(monitors))
			srv := &http.Server{Addr: addr, Handler: handler}
			go func() {
				<-d.Done()
				srv.Close()
			}()
			if err := srv.ListenAndServe(); err != http.ErrServerClosed {
				cliLog("Web server error: %v", err)
				os.Exit(1)
			}
			return
		}
		// Single display: defer RDP connection until browser reports resolution.
		handler, sessionDone := web.NewAutoWebHandler(opts)
		cliLog("Web viewer at http://localhost:%s", webAddr)
		srv := &http.Server{Addr: addr, Handler: handler}
		go func() {
			<-sessionDone
			srv.Close()
		}()
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			cliLog("Web server error: %v", err)
			os.Exit(1)
		}
		return
	}

	// Override RDP resolution from -gui=WxH if specified.
	guiW, guiH := 1600, 900 // default GUI resolution
	if guiFlag.set {
		if v := guiFlag.val; v != "" && v != "true" {
			parts := strings.SplitN(v, "x", 2)
			if len(parts) == 2 {
				if pw, err := strconv.Atoi(parts[0]); err == nil && pw > 0 {
					guiW = pw
				}
				if ph, err := strconv.Atoi(parts[1]); err == nil && ph > 0 {
					guiH = ph
				}
			}
		}
		// For multi-display GUI, fill auto-detect monitors with the GUI resolution.
		if displays.set && len(monitors) > 1 {
			for i := range monitors {
				if monitors[i].Width == 0 {
					monitors[i].Width = guiW
					monitors[i].Height = guiH
				}
			}
			positionMonitors(monitors)
			tw, mh := monitorBounds(monitors)
			opts.Width = uint16(tw)
			opts.Height = uint16(mh)
			// Apply primary-at-origin shift for opts.Monitors.
			proto := make([]rdp.MonitorConfig, len(monitors))
			copy(proto, monitors)
			var offX, offY int
			for _, m := range proto {
				if m.Primary {
					offX, offY = m.X, m.Y
					break
				}
			}
			for i := range proto {
				proto[i].X -= offX
				proto[i].Y -= offY
			}
			opts.Monitors = proto
		} else {
			opts.Width = uint16(guiW)
			opts.Height = uint16(guiH)
		}
	}

	// Create client
	client, err := rdp.NewClient(opts)
	if err != nil {
		cliLog("Failed to create client: %v", err)
		os.Exit(1)
	}

	// In log-only mode, show bitmap update summary
	if !guiFlag.set {
		client.OnBitmap(func(update *rdp.BitmapUpdate) {
			logger.Debug("bitmap update",
				"component", "BITMAP",
				"width", update.Width,
				"height", update.Height,
				"x", update.X,
				"y", update.Y,
			)
		})
	}

	// Handle disconnection and reconnection
	disconnectCh := make(chan struct{}, 1)
	client.OnDisconnect(func(err error) {
		if err != nil {
			cliLog("Disconnected: %v", err)
		} else {
			cliLog("Disconnected")
		}
		select {
		case disconnectCh <- struct{}{}:
		default:
		}
	})
	client.OnReconnecting(func() {
		cliLog("Disconnected, reconnecting...")
	})
	client.OnReconnected(func() {
		cliLog("Reconnected")
	})

	// Connect
	cliLog("Connecting to %s:%d as %s...", *host, *port, *user)
	if err := client.Connect(); err != nil {
		cliLog("Connection failed: %v", err)
		if (*user == "" || *pass == "") && strings.Contains(strings.ToLower(err.Error()), "credssp") {
			cliLog("Hint: server requires NLA (CredSSP) — provide -user and -pass, or disable NLA on the server")
		}
		os.Exit(1)
	}

	cliLog("Connected")

	// GUI viewer mode
	if guiFlag.set {
		if displays.set && len(monitors) > 1 {
			cliLog("Multi-display GUI: %d monitors", len(monitors))
		}
		if err := runGUI(client, opts, monitors, int(opts.Width), int(opts.Height)); err != nil {
			cliLog("GUI error: %v", err)
			os.Exit(1)
		}
		client.Close()
		return
	}

	// Log-only mode: wait for interrupt or server disconnect
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-sigChan:
		cliLog("Shutting down...")
	case <-disconnectCh:
	}
	client.Close()
}
