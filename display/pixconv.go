// Package display provides pixel format conversion, clipboard access,
// and shared utilities for RDP display frontends (gui, web).
package display

import (
	"encoding/binary"
)

// Lookup tables for 15bpp and 16bpp → RGBA conversion.
// Each entry is a packed uint32 in RGBA byte order (little-endian: 0xAABBGGRR).
// Computed once at init; 128KB + 256KB of static data.
var (
	lut15 [1 << 15]uint32 // 32K entries
	lut16 [1 << 16]uint32 // 64K entries
)

func init() {
	// 15bpp (5-5-5): 0RRRRRGGGGGBBBBB
	for pixel := range uint16(1 << 15) {
		r5 := byte((pixel >> 10) & 0x1F)
		g5 := byte((pixel >> 5) & 0x1F)
		b5 := byte(pixel & 0x1F)
		r8 := (r5 << 3) | (r5 >> 2)
		g8 := (g5 << 3) | (g5 >> 2)
		b8 := (b5 << 3) | (b5 >> 2)
		// Little-endian uint32: bytes [R, G, B, 0xFF] → value = 0xFF<<24 | B<<16 | G<<8 | R
		lut15[pixel] = 0xFF000000 | uint32(b8)<<16 | uint32(g8)<<8 | uint32(r8)
	}
	// 16bpp (5-6-5): RRRRRGGGGGGBBBBB
	for pixel := range uint32(1 << 16) {
		r5 := byte((pixel >> 11) & 0x1F)
		g6 := byte((pixel >> 5) & 0x3F)
		b5 := byte(pixel & 0x1F)
		r8 := (r5 << 3) | (r5 >> 2)
		g8 := (g6 << 2) | (g6 >> 4)
		b8 := (b5 << 3) | (b5 >> 2)
		lut16[pixel] = 0xFF000000 | uint32(b8)<<16 | uint32(g8)<<8 | uint32(r8)
	}
}

// srcRowY returns the source row index for output row y.
// Legacy RDP data is bottom-up, so source rows are read in reverse.
// EGFX data is top-down, so source rows are read in order.
func srcRowY(y, height int, topDown bool) int {
	if topDown {
		return y
	}
	return height - 1 - y
}

// ConvertToRGBA converts RDP pixel data to top-down RGBA suitable for
// screen display. Source is bottom-up by default (legacy RDP); set topDown
// for EGFX surfaces. Writes into dst which must be at least width*height*4
// bytes. Returns dst for convenience.
//
// Supported formats:
//   - 15 bpp (5-5-5): LE u16 0RRRRRGGGGGBBBBB
//   - 16 bpp (5-6-5): LE u16 RRRRRGGGGGGBBBBB
//   - 24 bpp (8-8-8): byte order B,G,R
//   - 32 bpp (8-8-8-8): byte order B,G,R,A (alpha ignored, set to 0xFF)
func ConvertToRGBA(dst, src []byte, width, height, bpp int, topDown ...bool) []byte {
	if width <= 0 || height <= 0 || bpp <= 0 {
		return dst
	}
	srcBytesPerPixel := bpp / 8
	if srcBytesPerPixel == 0 {
		srcBytesPerPixel = 1
	}
	if len(src) < width*height*srcBytesPerPixel || len(dst) < width*height*4 {
		return dst
	}
	td := len(topDown) > 0 && topDown[0]
	dstStride := width * 4
	switch bpp {
	case 15:
		srcStride := width * 2
		for y := 0; y < height; y++ {
			srcRow := src[srcRowY(y, height, td)*srcStride:]
			dstRow := dst[y*dstStride:]
			for x := 0; x < width; x++ {
				pixel := uint16(srcRow[x*2]) | uint16(srcRow[x*2+1])<<8
				binary.LittleEndian.PutUint32(dstRow[x*4:], lut15[pixel&0x7FFF])
			}
		}
	case 16:
		srcStride := width * 2
		for y := 0; y < height; y++ {
			srcRow := src[srcRowY(y, height, td)*srcStride:]
			dstRow := dst[y*dstStride:]
			for x := 0; x < width; x++ {
				pixel := uint16(srcRow[x*2]) | uint16(srcRow[x*2+1])<<8
				binary.LittleEndian.PutUint32(dstRow[x*4:], lut16[pixel])
			}
		}
	case 24:
		srcStride := width * 3
		for y := 0; y < height; y++ {
			srcRow := src[srcRowY(y, height, td)*srcStride:]
			dstRow := dst[y*dstStride:]
			for x := 0; x < width; x++ {
				si := x * 3
				di := x * 4
				dstRow[di] = srcRow[si+2]   // R
				dstRow[di+1] = srcRow[si+1] // G
				dstRow[di+2] = srcRow[si]   // B
				dstRow[di+3] = 0xFF
			}
		}
	case 32:
		// Input is already RGBA — flip bottom-up to top-down and force alpha=0xFF.
		// RDP desktops are always fully opaque; servers and order renderers sometimes
		// leave alpha=0 which causes desktop bleed-through in composited window managers.
		srcStride := width * 4
		total := width * height * 4
		if td {
			copy(dst[:total], src[:total])
		} else {
			for y := 0; y < height; y++ {
				srcOff := (height - 1 - y) * srcStride
				dstOff := y * dstStride
				copy(dst[dstOff:dstOff+srcStride], src[srcOff:srcOff+srcStride])
			}
		}
		for i := 3; i < total; i += 4 {
			if dst[i] == 0 {
				dst[i] = 0xFF
			}
		}
	}
	return dst
}
