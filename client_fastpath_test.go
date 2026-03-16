package rdp

import (
	"bytes"
	"encoding/binary"
	"log/slog"
	"testing"

	"gopher-rdp/protocol/fastpath"
)

var discardLogger = slog.New(slog.DiscardHandler)

// buildUpdate builds a single TS_FP_UPDATE entry (same logic as fastpath_test.go).
func buildUpdate(code, frag, compression byte, updateData []byte) []byte {
	hdr := code&0x0F | (frag&0x03)<<4 | (compression&0x03)<<6
	var buf []byte
	buf = append(buf, hdr)
	if compression == 0x2 {
		buf = append(buf, 0x00)
	}
	var sizeBuf [2]byte
	binary.LittleEndian.PutUint16(sizeBuf[:], uint16(len(updateData)))
	buf = append(buf, sizeBuf[:]...)
	buf = append(buf, updateData...)
	return buf
}

// buildBitmapPayload builds a minimal fast-path bitmap update payload:
// updateType(u16) + numRects(u16) + one TS_BITMAP_DATA with the given pixel data.
// Uses 16bpp and computes width from len(pixels) so dimensions match the data.
func buildBitmapPayload(pixels []byte) []byte {
	// updateType = UPDATETYPE_BITMAP (0x0001)
	var typeBuf [2]byte
	binary.LittleEndian.PutUint16(typeBuf[:], 0x0001)
	payload := append([]byte{}, typeBuf[:]...)
	// numRects = 1
	var buf [2]byte
	binary.LittleEndian.PutUint16(buf[:], 1)
	payload = append(payload, buf[:]...)

	// 16bpp = 2 bytes per pixel; width = len(pixels)/2, height = 1
	w := uint16(len(pixels) / 2)
	if w == 0 {
		w = 1
	}

	// TS_BITMAP_DATA: 7 u16 fields (14 bytes) + flags(u16) + bitmapLength(u16) + data
	var rect [18]byte
	binary.LittleEndian.PutUint16(rect[0:2], 0)      // destLeft
	binary.LittleEndian.PutUint16(rect[2:4], 0)      // destTop
	binary.LittleEndian.PutUint16(rect[4:6], w-1)    // destRight
	binary.LittleEndian.PutUint16(rect[6:8], 0)      // destBottom
	binary.LittleEndian.PutUint16(rect[8:10], w)     // width
	binary.LittleEndian.PutUint16(rect[10:12], 1)    // height
	binary.LittleEndian.PutUint16(rect[12:14], 16)   // bitsPerPixel
	binary.LittleEndian.PutUint16(rect[14:16], 0)    // flags (uncompressed)
	binary.LittleEndian.PutUint16(rect[16:18], uint16(len(pixels)))
	payload = append(payload, rect[:]...)
	payload = append(payload, pixels...)
	return payload
}

func TestHandleFastPathPDU_SingleUpdate(t *testing.T) {
	pixels := []byte{0xAA, 0xBB}
	bmpPayload := buildBitmapPayload(pixels)
	data := buildUpdate(fastpath.UpdateBitmap, fastpath.FragSingle, 0, bmpPayload)

	var got []BitmapUpdate
	c := &Client{
		log:      discardLogger,
		logFp:    discardLogger,
		logSec:   discardLogger,
		logPdu:   discardLogger,
		done:     make(chan struct{}),
		onBitmap: func(u *BitmapUpdate) { got = append(got, *u) },
	}
	c.handleFastPathPDU(0, data)

	if len(got) != 1 {
		t.Fatalf("got %d bitmap callbacks, want 1", len(got))
	}
	if !bytes.Equal(got[0].Data, pixels) {
		t.Errorf("data = %X, want %X", got[0].Data, pixels)
	}
}

func TestHandleFastPathPDU_FragFirstLast(t *testing.T) {
	pixels := []byte{0x01, 0x02, 0x03, 0x04}
	bmpPayload := buildBitmapPayload(pixels)
	mid := len(bmpPayload) / 2

	var data []byte
	data = append(data, buildUpdate(fastpath.UpdateBitmap, fastpath.FragFirst, 0, bmpPayload[:mid])...)
	data = append(data, buildUpdate(fastpath.UpdateBitmap, fastpath.FragLast, 0, bmpPayload[mid:])...)

	var got []BitmapUpdate
	c := &Client{
		log:      discardLogger,
		logFp:    discardLogger,
		logSec:   discardLogger,
		logPdu:   discardLogger,
		done:     make(chan struct{}),
		onBitmap: func(u *BitmapUpdate) { got = append(got, *u) },
	}
	c.handleFastPathPDU(0, data)

	if len(got) != 1 {
		t.Fatalf("got %d bitmap callbacks, want 1", len(got))
	}
	if !bytes.Equal(got[0].Data, pixels) {
		t.Errorf("data = %X, want %X", got[0].Data, pixels)
	}
}

func TestHandleFastPathPDU_FragFirstNextLast(t *testing.T) {
	pixels := []byte{0x10, 0x20, 0x30, 0x40, 0x50, 0x60}
	bmpPayload := buildBitmapPayload(pixels)
	third := len(bmpPayload) / 3

	var data []byte
	data = append(data, buildUpdate(fastpath.UpdateBitmap, fastpath.FragFirst, 0, bmpPayload[:third])...)
	data = append(data, buildUpdate(fastpath.UpdateBitmap, fastpath.FragNext, 0, bmpPayload[third:2*third])...)
	data = append(data, buildUpdate(fastpath.UpdateBitmap, fastpath.FragLast, 0, bmpPayload[2*third:])...)

	var got []BitmapUpdate
	c := &Client{
		log:      discardLogger,
		logFp:    discardLogger,
		logSec:   discardLogger,
		logPdu:   discardLogger,
		done:     make(chan struct{}),
		onBitmap: func(u *BitmapUpdate) { got = append(got, *u) },
	}
	c.handleFastPathPDU(0, data)

	if len(got) != 1 {
		t.Fatalf("got %d bitmap callbacks, want 1", len(got))
	}
	if !bytes.Equal(got[0].Data, pixels) {
		t.Errorf("data = %X, want %X", got[0].Data, pixels)
	}
}

func TestHandleFastPathPDU_OrphanFragments(t *testing.T) {
	bmpPayload := buildBitmapPayload([]byte{0xFF})

	tests := []struct {
		name string
		frag byte
	}{
		{"OrphanFragNext", fastpath.FragNext},
		{"OrphanFragLast", fastpath.FragLast},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := buildUpdate(fastpath.UpdateBitmap, tt.frag, 0, bmpPayload)
			called := false
			c := &Client{
				log:      discardLogger,
				logFp:    discardLogger,
				logSec:   discardLogger,
				logPdu:   discardLogger,
				done:     make(chan struct{}),
				onBitmap: func(u *BitmapUpdate) { called = true },
			}
			c.handleFastPathPDU(0, data)
			if called {
				t.Error("bitmap callback should not fire for orphan fragment")
			}
		})
	}
}

func TestHandleFastPathPDU_BufferReuse(t *testing.T) {
	// First fragmented sequence
	pixels1 := []byte{0xAA, 0xBB}
	bmp1 := buildBitmapPayload(pixels1)
	mid1 := len(bmp1) / 2

	// Second fragmented sequence
	pixels2 := []byte{0xCC, 0xDD}
	bmp2 := buildBitmapPayload(pixels2)
	mid2 := len(bmp2) / 2

	// Callback copies data (as real consumers must — Data sub-slices the
	// reassembly buffer, same contract as FragSingle sub-slicing tpkt readBuf).
	type snapshot struct{ pixels []byte }
	var got []snapshot
	c := &Client{
		log:    discardLogger,
		logFp:  discardLogger,
		logSec: discardLogger,
		logPdu: discardLogger,
		done:   make(chan struct{}),
		onBitmap: func(u *BitmapUpdate) {
			cp := make([]byte, len(u.Data))
			copy(cp, u.Data)
			got = append(got, snapshot{cp})
		},
	}

	// Deliver first sequence
	var data1 []byte
	data1 = append(data1, buildUpdate(fastpath.UpdateBitmap, fastpath.FragFirst, 0, bmp1[:mid1])...)
	data1 = append(data1, buildUpdate(fastpath.UpdateBitmap, fastpath.FragLast, 0, bmp1[mid1:])...)
	c.handleFastPathPDU(0, data1)

	// Deliver second sequence (reuses fragBuf backing array)
	var data2 []byte
	data2 = append(data2, buildUpdate(fastpath.UpdateBitmap, fastpath.FragFirst, 0, bmp2[:mid2])...)
	data2 = append(data2, buildUpdate(fastpath.UpdateBitmap, fastpath.FragLast, 0, bmp2[mid2:])...)
	c.handleFastPathPDU(0, data2)

	if len(got) != 2 {
		t.Fatalf("got %d bitmap callbacks, want 2", len(got))
	}
	if !bytes.Equal(got[0].pixels, pixels1) {
		t.Errorf("first data = %X, want %X", got[0].pixels, pixels1)
	}
	if !bytes.Equal(got[1].pixels, pixels2) {
		t.Errorf("second data = %X, want %X", got[1].pixels, pixels2)
	}
}
