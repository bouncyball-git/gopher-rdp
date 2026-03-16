package web

import "testing"

func TestClipBitmapToMonitor(t *testing.T) {
	tests := []struct {
		name    string
		msg     bitmapMsg
		mon     MonitorRect
		wantOK  bool
		wantX   int
		wantY   int
		wantW   int
		wantH   int
		wantPix []byte // first row of expected pixel data (nil = skip check)
	}{
		{
			name:   "fully inside",
			msg:    bitmapMsg{X: 100, Y: 50, Width: 200, Height: 100, BitsPerPixel: 32, TopDown: true, Data: make([]byte, 200*100*4)},
			mon:    MonitorRect{X: 0, Y: 0, Width: 1920, Height: 1080},
			wantOK: true, wantX: 100, wantY: 50, wantW: 200, wantH: 100,
		},
		{
			name:   "no intersection - right of monitor",
			msg:    bitmapMsg{X: 2000, Y: 50, Width: 200, Height: 100, BitsPerPixel: 32, TopDown: true, Data: make([]byte, 200*100*4)},
			mon:    MonitorRect{X: 0, Y: 0, Width: 1920, Height: 1080},
			wantOK: false,
		},
		{
			name:   "no intersection - left of monitor",
			msg:    bitmapMsg{X: 0, Y: 0, Width: 100, Height: 100, BitsPerPixel: 32, TopDown: true, Data: make([]byte, 100*100*4)},
			mon:    MonitorRect{X: 1920, Y: 0, Width: 1920, Height: 1080},
			wantOK: false,
		},
		{
			name:   "partial overlap - right edge",
			msg:    bitmapMsg{X: 1900, Y: 0, Width: 40, Height: 10, BitsPerPixel: 32, TopDown: true, Data: make([]byte, 40*10*4)},
			mon:    MonitorRect{X: 0, Y: 0, Width: 1920, Height: 1080},
			wantOK: true, wantX: 1900, wantY: 0, wantW: 20, wantH: 10,
		},
		{
			name:   "partial overlap - spans two monitors",
			msg:    bitmapMsg{X: 1900, Y: 0, Width: 40, Height: 10, BitsPerPixel: 32, TopDown: true, Data: make([]byte, 40*10*4)},
			mon:    MonitorRect{X: 1920, Y: 0, Width: 1920, Height: 1080},
			wantOK: true, wantX: 0, wantY: 0, wantW: 20, wantH: 10,
		},
		{
			name:   "translate to monitor-local coords",
			msg:    bitmapMsg{X: 2000, Y: 100, Width: 50, Height: 50, BitsPerPixel: 32, TopDown: true, Data: make([]byte, 50*50*4)},
			mon:    MonitorRect{X: 1920, Y: 0, Width: 1920, Height: 1080},
			wantOK: true, wantX: 80, wantY: 100, wantW: 50, wantH: 50,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := clipBitmapToMonitor(tt.msg, tt.mon)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if got.X != tt.wantX || got.Y != tt.wantY {
				t.Errorf("position = (%d, %d), want (%d, %d)", got.X, got.Y, tt.wantX, tt.wantY)
			}
			if got.Width != tt.wantW || got.Height != tt.wantH {
				t.Errorf("size = %dx%d, want %dx%d", got.Width, got.Height, tt.wantW, tt.wantH)
			}
		})
	}
}

func TestClipBitmapPixelData(t *testing.T) {
	// Create a 4x2 bitmap where each pixel has a unique byte pattern.
	// Pixel (x,y) = [x*4+y*16, x*4+y*16+1, x*4+y*16+2, x*4+y*16+3]
	data := make([]byte, 4*2*4) // 4 wide, 2 tall, 4 bytes per pixel
	for y := range 2 {
		for x := range 4 {
			off := (y*4 + x) * 4
			v := byte(x*4 + y*16)
			data[off+0] = v
			data[off+1] = v + 1
			data[off+2] = v + 2
			data[off+3] = v + 3
		}
	}

	msg := bitmapMsg{X: 10, Y: 20, Width: 4, Height: 2, BitsPerPixel: 32, TopDown: true, Data: data}
	// Monitor covers X: 12..100 — so we clip to columns 2-3 of the bitmap.
	mon := MonitorRect{X: 12, Y: 20, Width: 100, Height: 100}

	got, ok := clipBitmapToMonitor(msg, mon)
	if !ok {
		t.Fatal("expected intersection")
	}
	if got.X != 0 || got.Y != 0 {
		t.Errorf("position = (%d, %d), want (0, 0)", got.X, got.Y)
	}
	if got.Width != 2 || got.Height != 2 {
		t.Errorf("size = %dx%d, want 2x2", got.Width, got.Height)
	}

	// Check pixel data: should be columns 2-3 of the original.
	// Row 0: pixels at original x=2,3 → byte values 8,9,10,11 and 12,13,14,15
	// Row 1: pixels at original x=2,3 → byte values 24,25,26,27 and 28,29,30,31
	expected := []byte{
		8, 9, 10, 11, 12, 13, 14, 15,
		24, 25, 26, 27, 28, 29, 30, 31,
	}
	if len(got.Data) != len(expected) {
		t.Fatalf("data length = %d, want %d", len(got.Data), len(expected))
	}
	for i := range expected {
		if got.Data[i] != expected[i] {
			t.Errorf("data[%d] = %d, want %d", i, got.Data[i], expected[i])
		}
	}
}
