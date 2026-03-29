//go:build gui

package gui

import (
	"encoding/binary"
	"sync"
	"testing"

	"github.com/bouncyball-git/gopher-rdp/protocol/rdpsnd"
)

func TestPCMStream_ReadSilenceWhenEmpty(t *testing.T) {
	s := newPCMStream()
	buf := make([]byte, 128)
	n, err := s.Read(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 128 {
		t.Fatalf("expected n=128, got %d", n)
	}
	for i, b := range buf {
		if b != 0 {
			t.Fatalf("expected silence at byte %d, got 0x%02X", i, b)
		}
	}
}

func TestPCMStream_WriteReadRoundTrip(t *testing.T) {
	// Stereo 16-bit 44100 Hz — should pass through with no conversion
	s := newPCMStream()
	// Write 4 stereo samples (4 frames × 2 channels × 2 bytes = 16 bytes)
	pcm := make([]byte, 16)
	for i := 0; i < 4; i++ {
		binary.LittleEndian.PutUint16(pcm[i*4:], uint16(int16(1000*(i+1))))   // L
		binary.LittleEndian.PutUint16(pcm[i*4+2:], uint16(int16(-1000*(i+1)))) // R
	}
	sample := &rdpsnd.AudioSample{
		Format: rdpsnd.AudioFormat{
			Tag:           rdpsnd.WaveFormatPCM,
			Channels:      2,
			SamplesPerSec: 44100,
			BitsPerSample: 16,
		},
		Data: pcm,
	}
	s.WriteRaw(sample)

	out := make([]byte, 32)
	n, _ := s.Read(out)
	if n != 32 {
		t.Fatalf("expected n=32, got %d", n)
	}
	// First 16 bytes should match the input
	for i := 0; i < 16; i++ {
		if out[i] != pcm[i] {
			t.Fatalf("mismatch at byte %d: want 0x%02X got 0x%02X", i, pcm[i], out[i])
		}
	}
	// Remaining should be silence
	for i := 16; i < 32; i++ {
		if out[i] != 0 {
			t.Fatalf("expected silence at byte %d, got 0x%02X", i, out[i])
		}
	}
}

func TestPCMStream_MonoToStereo(t *testing.T) {
	s := newPCMStream()
	// 2 mono 16-bit samples: [500, -500]
	pcm := make([]byte, 4)
	binary.LittleEndian.PutUint16(pcm[0:], uint16(int16(500)))
	neg500 := int16(-500)
	binary.LittleEndian.PutUint16(pcm[2:], uint16(neg500))

	sample := &rdpsnd.AudioSample{
		Format: rdpsnd.AudioFormat{
			Tag:           rdpsnd.WaveFormatPCM,
			Channels:      1,
			SamplesPerSec: 44100,
			BitsPerSample: 16,
		},
		Data: pcm,
	}
	s.WriteRaw(sample)

	// Expect 2 stereo frames: [500,500, -500,-500] = 8 bytes
	out := make([]byte, 8)
	s.Read(out)

	got := make([]int16, 4)
	for i := range got {
		got[i] = int16(binary.LittleEndian.Uint16(out[i*2:]))
	}
	want := []int16{500, 500, -500, -500}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("sample[%d]: want %d got %d", i, w, got[i])
		}
	}
}

func TestPCMStream_8BitTo16Bit(t *testing.T) {
	s := newPCMStream()
	// 2 mono 8-bit samples: silence(128) and loud(255)
	sample := &rdpsnd.AudioSample{
		Format: rdpsnd.AudioFormat{
			Tag:           rdpsnd.WaveFormatPCM,
			Channels:      1,
			SamplesPerSec: 44100,
			BitsPerSample: 8,
		},
		Data: []byte{128, 255},
	}
	s.WriteRaw(sample)

	// 2 mono → 2 stereo frames = 8 bytes
	out := make([]byte, 8)
	s.Read(out)

	// Sample 128 → int16(0)<<8 = 0
	s0L := int16(binary.LittleEndian.Uint16(out[0:]))
	if s0L != 0 {
		t.Errorf("128→16bit: want 0, got %d", s0L)
	}
	// Sample 255 → int16(127)<<8 = 32512
	s1L := int16(binary.LittleEndian.Uint16(out[4:]))
	if s1L != 32512 {
		t.Errorf("255→16bit: want 32512, got %d", s1L)
	}
}

func TestPCMStream_BufferOverflow(t *testing.T) {
	s := newPCMStream()

	// Fill the entire ring buffer with 0xFF
	full := make([]byte, ringBufSize)
	for i := range full {
		full[i] = 0xFF
	}
	s.write(full)

	// Write 4 more bytes — should drop oldest and preserve newest
	s.write([]byte{0xAA, 0xBB, 0xCC, 0xDD})

	// Read everything
	out := make([]byte, ringBufSize)
	s.Read(out)

	// Last 4 bytes should be our newest data
	if out[ringBufSize-4] != 0xAA || out[ringBufSize-3] != 0xBB ||
		out[ringBufSize-2] != 0xCC || out[ringBufSize-1] != 0xDD {
		t.Errorf("newest data not preserved at end: got %X %X %X %X",
			out[ringBufSize-4], out[ringBufSize-3], out[ringBufSize-2], out[ringBufSize-1])
	}

	// First 4 bytes should be 0xFF (oldest surviving data from original fill)
	// Actually the first bytes should be from the original fill minus 4 dropped
	for i := 0; i < ringBufSize-4; i++ {
		if out[i] != 0xFF {
			t.Errorf("byte %d: want 0xFF got 0x%02X", i, out[i])
			break
		}
	}
}

func TestPCMStream_ConcurrentAccess(t *testing.T) {
	s := newPCMStream()
	var wg sync.WaitGroup

	// Concurrent writer
	wg.Add(1)
	go func() {
		defer wg.Done()
		sample := &rdpsnd.AudioSample{
			Format: rdpsnd.AudioFormat{
				Tag:           rdpsnd.WaveFormatPCM,
				Channels:      2,
				SamplesPerSec: 44100,
				BitsPerSample: 16,
			},
			Data: make([]byte, 256),
		}
		for i := 0; i < 1000; i++ {
			s.WriteRaw(sample)
		}
	}()

	// Concurrent reader
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 128)
		for i := 0; i < 2000; i++ {
			s.Read(buf)
		}
	}()

	wg.Wait()
	// No panic or data race = pass
}
