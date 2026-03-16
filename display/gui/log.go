//go:build gui

package gui

import (
	"context"
	"io"
	"log/slog"
	"strings"
)

// childLogHandler formats log output to match the app's FilterHandler format:
// "2006-01-02 15:04:05 [GOPHER-RDP GUI LEVEL] msg, key=val, key=val"
type childLogHandler struct {
	w        io.Writer
	level    slog.Level
	preAttrs []slog.Attr
}

func newChildLogHandler(w io.Writer, level slog.Level) *childLogHandler {
	return &childLogHandler{w: w, level: level}
}

func (h *childLogHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level
}

func (h *childLogHandler) Handle(_ context.Context, r slog.Record) error {
	if r.Level < h.level {
		return nil
	}

	allAttrs := make([]slog.Attr, 0, len(h.preAttrs)+r.NumAttrs())
	allAttrs = append(allAttrs, h.preAttrs...)
	r.Attrs(func(a slog.Attr) bool {
		allAttrs = append(allAttrs, a)
		return true
	})

	var b strings.Builder
	b.WriteString(r.Time.Format("2006-01-02 15:04:05"))
	b.WriteString(" [GOPHER-RDP GUI ")
	switch {
	case r.Level >= slog.LevelError:
		b.WriteString("ERROR")
	case r.Level >= slog.LevelWarn:
		b.WriteString("WARN")
	case r.Level >= slog.LevelInfo:
		b.WriteString("INFO")
	case r.Level >= slog.LevelDebug:
		b.WriteString("DEBUG")
	default:
		b.WriteString("TRACE")
	}
	b.WriteString("] ")
	b.WriteString(r.Message)
	for _, a := range allAttrs {
		if a.Key == "component" {
			continue
		}
		b.WriteString(", ")
		b.WriteString(a.Key)
		b.WriteByte('=')
		b.WriteString(a.Value.String())
	}
	b.WriteByte('\n')
	_, err := io.WriteString(h.w, b.String())
	return err
}

func (h *childLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	combined := make([]slog.Attr, len(h.preAttrs), len(h.preAttrs)+len(attrs))
	copy(combined, h.preAttrs)
	combined = append(combined, attrs...)
	return &childLogHandler{w: h.w, level: h.level, preAttrs: combined}
}

func (h *childLogHandler) WithGroup(string) slog.Handler {
	return h
}
