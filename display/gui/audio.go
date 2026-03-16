//go:build gui

package gui

import (
	"encoding/binary"
	"sync"

	"gopher-rdp/protocol/rdpsnd"
)

const ringBufSize = 262144 // 256KB

// pcmStream is a thread-safe ring buffer that implements io.Reader for
// Ebiten's audio player. Write converts incoming rdpsnd samples to signed
// 16-bit LE stereo at the context sample rate. Read never blocks — it
// returns buffered data and fills any remainder with silence.
type pcmStream struct {
	mu    sync.Mutex
	buf   [ringBufSize]byte
	r     int // read position
	w     int // write position
	count int // bytes buffered
}

func newPCMStream() *pcmStream {
	return &pcmStream{}
}

// Read fills p from the ring buffer. Any unfilled portion is zeroed (silence).
// Never returns io.EOF — Ebiten's audio thread must never block or stop.
func (s *pcmStream) Read(p []byte) (int, error) {
	s.mu.Lock()
	n := s.count
	if n > len(p) {
		n = len(p)
	}
	// Copy from ring buffer
	for i := 0; i < n; i++ {
		p[i] = s.buf[(s.r+i)%ringBufSize]
	}
	s.r = (s.r + n) % ringBufSize
	s.count -= n
	s.mu.Unlock()

	// Fill remainder with silence
	for i := n; i < len(p); i++ {
		p[i] = 0
	}
	return len(p), nil
}

// write appends raw bytes to the ring buffer. If full, drops oldest data.
func (s *pcmStream) write(data []byte) {
	s.mu.Lock()
	for _, b := range data {
		if s.count == ringBufSize {
			// Drop oldest byte
			s.r = (s.r + 1) % ringBufSize
			s.count--
		}
		s.buf[s.w] = b
		s.w = (s.w + 1) % ringBufSize
		s.count++
	}
	s.mu.Unlock()
}

// WriteRaw writes PCM audio directly to the ring buffer. Converts 8-bit
// to 16-bit and mono to stereo as needed, but does no resampling — the
// Ebiten audio context must be created at the server's sample rate.
func (s *pcmStream) WriteRaw(sample *rdpsnd.AudioSample) {
	if len(sample.Data) == 0 {
		return
	}

	bps := int(sample.Format.BitsPerSample)
	channels := int(sample.Format.Channels)

	if bps == 16 && channels >= 2 {
		// Fast path: already 16-bit stereo, write directly
		s.write(sample.Data)
		return
	}

	// Decode to []int16
	var samples []int16
	switch bps {
	case 8:
		samples = make([]int16, len(sample.Data))
		for i, b := range sample.Data {
			samples[i] = int16(int(b)-128) << 8
		}
	case 16:
		nSamples := len(sample.Data) / 2
		samples = make([]int16, nSamples)
		for i := 0; i < nSamples; i++ {
			samples[i] = int16(binary.LittleEndian.Uint16(sample.Data[i*2:]))
		}
	default:
		return
	}

	// Convert mono to stereo
	if channels == 1 {
		stereo := make([]int16, len(samples)*2)
		for i, v := range samples {
			stereo[i*2] = v
			stereo[i*2+1] = v
		}
		samples = stereo
	}

	// Encode to bytes
	out := make([]byte, len(samples)*2)
	for i, v := range samples {
		binary.LittleEndian.PutUint16(out[i*2:], uint16(v))
	}
	s.write(out)
}
