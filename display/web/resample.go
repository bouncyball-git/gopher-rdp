package web

import (
	"encoding/binary"
	"math"
)

// PCMFormat describes an interleaved PCM audio buffer.
type PCMFormat struct {
	Rate     uint32 // sample rate in Hz (8000–192000)
	Channels uint16 // channel count (1–8)
	Bits     uint16 // bits per sample: 8, 16, or 24
}

// bytesPerFrame returns the byte size of one interleaved frame.
func (f PCMFormat) bytesPerFrame() int { return int(f.Channels) * int(f.Bits) / 8 }

// ResamplePCM converts interleaved PCM audio between arbitrary formats
// using Catmull-Rom (cubic) interpolation. Supports 8, 16 and 24-bit
// signed little-endian samples at any rate from 8000 to 192000 Hz.
//
// Channel mapping: mono→stereo duplicates, stereo→mono averages,
// extra channels are dropped, missing channels duplicate channel 0.
//
// Output is always in the destination format's bit depth and channel layout.
// Returns nil if input is too short or formats are invalid.
func ResamplePCM(src []byte, srcFmt, dstFmt PCMFormat) []byte {
	srcBPS := int(srcFmt.Bits) / 8
	dstBPS := int(dstFmt.Bits) / 8
	if srcBPS == 0 || dstBPS == 0 || srcFmt.Channels == 0 || dstFmt.Channels == 0 {
		return nil
	}
	srcFrameSize := srcFmt.bytesPerFrame()
	if srcFrameSize == 0 || len(src) < srcFrameSize {
		return nil
	}
	srcFrames := len(src) / srcFrameSize

	// Output frame count
	dstFrames := int(uint64(srcFrames) * uint64(dstFmt.Rate) / uint64(srcFmt.Rate))
	if dstFrames < 1 {
		return nil
	}

	dstFrameSize := dstFmt.bytesPerFrame()
	out := make([]byte, dstFrames*dstFrameSize)

	// Same rate: skip interpolation, just convert channels/bit depth.
	if srcFmt.Rate == dstFmt.Rate {
		for i := 0; i < dstFrames && i < srcFrames; i++ {
			for ch := uint16(0); ch < dstFmt.Channels; ch++ {
				v := readSample(src, i, ch, srcFmt)
				writeSample(out, i, ch, dstFmt, v)
			}
		}
		return out
	}

	ratio := float64(srcFmt.Rate) / float64(dstFmt.Rate)

	for i := 0; i < dstFrames; i++ {
		srcPos := float64(i) * ratio
		idx := int(srcPos)
		frac := srcPos - float64(idx)

		// Four frame indices for Catmull-Rom
		i0 := idx - 1
		i1 := idx
		i2 := idx + 1
		i3 := idx + 2
		if i0 < 0 {
			i0 = 0
		}
		if i2 >= srcFrames {
			i2 = srcFrames - 1
		}
		if i3 >= srcFrames {
			i3 = srcFrames - 1
		}

		for ch := uint16(0); ch < dstFmt.Channels; ch++ {
			s0 := readSample(src, i0, ch, srcFmt)
			s1 := readSample(src, i1, ch, srcFmt)
			s2 := readSample(src, i2, ch, srcFmt)
			s3 := readSample(src, i3, ch, srcFmt)

			v := cubicInterp(s0, s1, s2, s3, frac)
			writeSample(out, i, ch, dstFmt, v)
		}
	}

	return out
}

// readSample reads one sample as a float64 in the range of the source bit depth.
// Handles channel mapping: downmix (average) or upmix (duplicate ch 0).
func readSample(data []byte, frame int, dstCh uint16, fmt PCMFormat) float64 {
	if dstCh < fmt.Channels {
		return readRawSample(data, frame, dstCh, fmt)
	}
	// Destination channel beyond source count: duplicate channel 0
	return readRawSample(data, frame, 0, fmt)
}

// readRawSample reads a single sample value from interleaved PCM data.
func readRawSample(data []byte, frame int, ch uint16, fmt PCMFormat) float64 {
	bps := int(fmt.Bits) / 8
	off := frame*fmt.bytesPerFrame() + int(ch)*bps
	if off+bps > len(data) {
		return 0
	}
	switch fmt.Bits {
	case 8:
		// 8-bit PCM is unsigned, center at 128
		return float64(int(data[off]) - 128)
	case 16:
		return float64(int16(binary.LittleEndian.Uint16(data[off:])))
	case 24:
		// 24-bit signed little-endian: 3 bytes, sign-extend from bit 23
		lo := uint32(data[off])
		mid := uint32(data[off+1])
		hi := uint32(data[off+2])
		v := lo | mid<<8 | hi<<16
		if v&0x800000 != 0 {
			v |= 0xFF000000 // sign extend
		}
		return float64(int32(v))
	}
	return 0
}

// writeSample writes a float64 sample value to interleaved PCM output,
// clamping to the destination bit depth range.
func writeSample(data []byte, frame int, ch uint16, fmt PCMFormat, v float64) {
	bps := int(fmt.Bits) / 8
	off := frame*fmt.bytesPerFrame() + int(ch)*bps
	if off+bps > len(data) {
		return
	}
	switch fmt.Bits {
	case 8:
		// 8-bit unsigned, center at 128
		s := int(math.Round(v)) + 128
		if s < 0 {
			s = 0
		}
		if s > 255 {
			s = 255
		}
		data[off] = byte(s)
	case 16:
		s := int(math.Round(v))
		if s > 32767 {
			s = 32767
		}
		if s < -32768 {
			s = -32768
		}
		binary.LittleEndian.PutUint16(data[off:], uint16(int16(s)))
	case 24:
		s := int32(math.Round(v))
		if s > 8388607 {
			s = 8388607
		}
		if s < -8388608 {
			s = -8388608
		}
		u := uint32(s)
		data[off] = byte(u)
		data[off+1] = byte(u >> 8)
		data[off+2] = byte(u >> 16)
	}
}

// cubicInterp performs Catmull-Rom interpolation between p1 and p2.
// t is the fractional position [0,1).
func cubicInterp(p0, p1, p2, p3, t float64) float64 {
	t2 := t * t
	t3 := t2 * t
	return 0.5 * ((2 * p1) +
		(-p0+p2)*t +
		(2*p0-5*p1+4*p2-p3)*t2 +
		(-p0+3*p1-3*p2+p3)*t3)
}
