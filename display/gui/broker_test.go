//go:build gui

package gui

import (
	"bytes"
	"testing"
)

func TestClipBitmapToMonitor(t *testing.T) {
	// Helper to make pixel data of a given size (w*h*4 bytes).
	makePixels := func(w, h int) []byte {
		data := make([]byte, w*h*4)
		for i := range data {
			data[i] = byte(i % 256)
		}
		return data
	}

	tests := []struct {
		name   string
		rect   bitmapRect
		mon    MonitorInfo
		wantOK bool
		wantX  int
		wantY  int
		wantW  int
		wantH  int
	}{
		{
			name:   "fully inside",
			rect:   bitmapRect{X: 100, Y: 100, Width: 50, Height: 50, Data: makePixels(50, 50)},
			mon:    MonitorInfo{X: 0, Y: 0, Width: 1920, Height: 1080},
			wantOK: true,
			wantX:  100, wantY: 100, wantW: 50, wantH: 50,
		},
		{
			name:   "fully outside right",
			rect:   bitmapRect{X: 2000, Y: 0, Width: 100, Height: 100, Data: makePixels(100, 100)},
			mon:    MonitorInfo{X: 0, Y: 0, Width: 1920, Height: 1080},
			wantOK: false,
		},
		{
			name:   "fully outside left",
			rect:   bitmapRect{X: 0, Y: 0, Width: 100, Height: 100, Data: makePixels(100, 100)},
			mon:    MonitorInfo{X: 1920, Y: 0, Width: 1920, Height: 1080},
			wantOK: false,
		},
		{
			name:   "partial overlap right edge",
			rect:   bitmapRect{X: 1900, Y: 0, Width: 40, Height: 10, Data: makePixels(40, 10)},
			mon:    MonitorInfo{X: 0, Y: 0, Width: 1920, Height: 1080},
			wantOK: true,
			wantX:  1900, wantY: 0, wantW: 20, wantH: 10,
		},
		{
			name:   "on second monitor",
			rect:   bitmapRect{X: 1920, Y: 0, Width: 100, Height: 100, Data: makePixels(100, 100)},
			mon:    MonitorInfo{X: 1920, Y: 0, Width: 1920, Height: 1080},
			wantOK: true,
			wantX:  0, wantY: 0, wantW: 100, wantH: 100,
		},
		{
			name:   "spans both monitors",
			rect:   bitmapRect{X: 1910, Y: 500, Width: 20, Height: 10, Data: makePixels(20, 10)},
			mon:    MonitorInfo{X: 1920, Y: 0, Width: 1920, Height: 1080},
			wantOK: true,
			wantX:  0, wantY: 500, wantW: 10, wantH: 10,
		},
		{
			name:   "exact monitor bounds",
			rect:   bitmapRect{X: 0, Y: 0, Width: 1920, Height: 1080, Data: makePixels(1920, 1080)},
			mon:    MonitorInfo{X: 0, Y: 0, Width: 1920, Height: 1080},
			wantOK: true,
			wantX:  0, wantY: 0, wantW: 1920, wantH: 1080,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clipped, ok := clipBitmapToMonitor(tt.rect, tt.mon)
			if ok != tt.wantOK {
				t.Fatalf("ok: got %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if clipped.X != tt.wantX || clipped.Y != tt.wantY ||
				clipped.Width != tt.wantW || clipped.Height != tt.wantH {
				t.Errorf("rect: got (%d,%d,%d,%d), want (%d,%d,%d,%d)",
					clipped.X, clipped.Y, clipped.Width, clipped.Height,
					tt.wantX, tt.wantY, tt.wantW, tt.wantH)
			}
			expectedDataLen := tt.wantW * tt.wantH * 4
			if len(clipped.Data) != expectedDataLen {
				t.Errorf("data len: got %d, want %d", len(clipped.Data), expectedDataLen)
			}
		})
	}
}

func TestClipBitmapPixelData(t *testing.T) {
	// 4x2 bitmap with distinct pixel values.
	// Row 0: [R0,R1,R2,R3], Row 1: [R4,R5,R6,R7]
	data := make([]byte, 4*2*4)
	for i := range 8 {
		off := i * 4
		data[off] = byte(i)   // R
		data[off+1] = byte(i) // G
		data[off+2] = byte(i) // B
		data[off+3] = 0xFF    // A
	}

	// Bitmap at global X=0, monitor at X=2 with W=2 → clips to rightmost 2 columns.
	rect := bitmapRect{X: 0, Y: 0, Width: 4, Height: 2, Data: data}
	mon := MonitorInfo{X: 2, Y: 0, Width: 2, Height: 2}

	clipped, ok := clipBitmapToMonitor(rect, mon)
	if !ok {
		t.Fatal("expected intersection")
	}
	if clipped.Width != 2 || clipped.Height != 2 {
		t.Fatalf("size: %dx%d", clipped.Width, clipped.Height)
	}

	// Should contain pixels [2,3] from row 0 and [6,7] from row 1.
	expected := []byte{
		2, 2, 2, 0xFF, 3, 3, 3, 0xFF, // row 0
		6, 6, 6, 0xFF, 7, 7, 7, 0xFF, // row 1
	}
	if !bytes.Equal(clipped.Data, expected) {
		t.Errorf("pixel data mismatch:\n  got:  %v\n  want: %v", clipped.Data, expected)
	}
}
