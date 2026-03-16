package rdpsnd

import (
	"encoding/binary"
	"testing"
)

// buildIMAMonoBlock creates an IMA ADPCM mono block with the given header
// and nibble data bytes.
func buildIMAMonoBlock(predictor int16, stepIndex byte, nibbleBytes []byte) []byte {
	blk := make([]byte, 4+len(nibbleBytes))
	binary.LittleEndian.PutUint16(blk[0:], uint16(predictor))
	blk[2] = stepIndex
	blk[3] = 0 // reserved
	copy(blk[4:], nibbleBytes)
	return blk
}

// buildIMAStereoBlock creates an IMA ADPCM stereo block with the given
// per-channel headers and interleaved nibble data.
func buildIMAStereoBlock(predL, predR int16, stepL, stepR byte, nibbleBytes []byte) []byte {
	blk := make([]byte, 8+len(nibbleBytes))
	// Left channel header
	binary.LittleEndian.PutUint16(blk[0:], uint16(predL))
	blk[2] = stepL
	blk[3] = 0
	// Right channel header
	binary.LittleEndian.PutUint16(blk[4:], uint16(predR))
	blk[6] = stepR
	blk[7] = 0
	copy(blk[8:], nibbleBytes)
	return blk
}

func TestDecodeIMAADPCM_Mono(t *testing.T) {
	tests := []struct {
		name            string
		predictor       int16
		stepIndex       byte
		nibbleBytes     []byte
		samplesPerBlock int
		want            []int16
	}{
		{
			name:            "silence nibbles",
			predictor:       0,
			stepIndex:       0,
			nibbleBytes:     []byte{0x00, 0x00}, // all zero nibbles = no change
			samplesPerBlock: 5,
			want:            []int16{0, 0, 0, 0, 0},
		},
		{
			name:            "header only",
			predictor:       1000,
			stepIndex:       7,
			nibbleBytes:     nil,
			samplesPerBlock: 1,
			want:            []int16{1000},
		},
		{
			name:      "ascending nibbles",
			predictor: 0,
			stepIndex: 7, // step = 14
			// Nibble 0x07 (max positive): diff = step>>3 + step + step>>1 + step>>2 = 1+14+7+3 = 25
			// Nibble byte 0x77: low=7, high=7 → two +25 steps
			nibbleBytes:     []byte{0x77},
			samplesPerBlock: 3,
			want:            []int16{0, 25, 81},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := buildIMAMonoBlock(tt.predictor, tt.stepIndex, tt.nibbleBytes)
			got := decodeIMAADPCM(src, 1, tt.samplesPerBlock, len(src), nil)
			if len(got) != len(tt.want) {
				t.Fatalf("len(got) = %d, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("sample[%d] = %d, want %d", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestDecodeIMAADPCM_Stereo(t *testing.T) {
	// Stereo block: headers for L and R, then 8 bytes of nibble data
	// (4 bytes = 8 nibbles for L, 4 bytes = 8 nibbles for R).
	// All zero nibbles → output is initial predictors repeated.
	src := buildIMAStereoBlock(100, -100, 0, 0, make([]byte, 8))
	// samplesPerBlock = 1 (header) + 8 (from nibble data) = 9
	got := decodeIMAADPCM(src, 2, 9, len(src), nil)
	// Expected: [100, -100] header, then 8 × [100, -100] since nibbles are 0 at step 0
	// Actually step 0 means step=7, nibble 0 gives diff = 7>>3 = 0, predictor stays same
	// Wait: diff = step>>3 = 0 (7>>3=0). So predictor unchanged.
	want := []int16{100, -100, 100, -100, 100, -100, 100, -100, 100, -100, 100, -100, 100, -100, 100, -100, 100, -100}
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d\ngot: %v", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("sample[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestDecodeIMAADPCM_DstReuse(t *testing.T) {
	src := buildIMAMonoBlock(500, 0, nil)
	dst := make([]int16, 0, 64)
	got := decodeIMAADPCM(src, 1, 1, len(src), dst)
	if len(got) != 1 || got[0] != 500 {
		t.Fatalf("got %v, want [500]", got)
	}
	// Verify we reused the buffer.
	if cap(got) != 64 {
		t.Errorf("dst buffer not reused: cap = %d, want 64", cap(got))
	}
}

// buildMSMonoBlock creates an MS-ADPCM mono block.
func buildMSMonoBlock(predIdx byte, delta int16, samp1, samp2 int16, nibbleBytes []byte) []byte {
	blk := make([]byte, 7+len(nibbleBytes))
	blk[0] = predIdx
	binary.LittleEndian.PutUint16(blk[1:], uint16(delta))
	binary.LittleEndian.PutUint16(blk[3:], uint16(samp1))
	binary.LittleEndian.PutUint16(blk[5:], uint16(samp2))
	copy(blk[7:], nibbleBytes)
	return blk
}

func TestDecodeMSADPCM_Mono(t *testing.T) {
	tests := []struct {
		name            string
		predIdx         byte
		delta           int16
		samp1           int16
		samp2           int16
		nibbleBytes     []byte
		samplesPerBlock int
		want            []int16
	}{
		{
			name:            "header only",
			predIdx:         0,
			delta:           16,
			samp1:           1000,
			samp2:           500,
			nibbleBytes:     nil,
			samplesPerBlock: 2,
			want:            []int16{500, 1000},
		},
		{
			name:    "zero nibbles coeff0",
			predIdx: 0, // coeff = {256, 0}
			delta:   16,
			samp1:   100,
			samp2:   0,
			// Nibble 0x00 → signed 0 → predicted = (samp1*256 + samp2*0)/256 + 0*delta = samp1
			nibbleBytes:     []byte{0x00},
			samplesPerBlock: 4, // 2 header + 2 from nibble byte
			want:            []int16{0, 100, 100, 100},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := buildMSMonoBlock(tt.predIdx, tt.delta, tt.samp1, tt.samp2, tt.nibbleBytes)
			blockAlign := len(src)
			got := decodeMSADPCM(src, 1, tt.samplesPerBlock, blockAlign, nil)
			if len(got) != len(tt.want) {
				t.Fatalf("len(got) = %d, want %d\ngot: %v", len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("sample[%d] = %d, want %d", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestDecodeMSADPCM_Stereo(t *testing.T) {
	// Stereo: predIdx(1) × 2 + delta(2) × 2 + samp1(2) × 2 + samp2(2) × 2 = 14 bytes header
	blk := make([]byte, 14)
	blk[0] = 0  // predIdx left: coeff {256, 0}
	blk[1] = 0  // predIdx right: coeff {256, 0}
	off := 2
	binary.LittleEndian.PutUint16(blk[off:], uint16(16))   // delta left
	binary.LittleEndian.PutUint16(blk[off+2:], uint16(16)) // delta right
	off += 4
	binary.LittleEndian.PutUint16(blk[off:], uint16(200))    // samp1 left
	binary.LittleEndian.PutUint16(blk[off+2:], uint16((-200)&0xFFFF)) // samp1 right (-200)
	off += 4
	binary.LittleEndian.PutUint16(blk[off:], uint16(100))    // samp2 left
	binary.LittleEndian.PutUint16(blk[off+2:], uint16((-100)&0xFFFF)) // samp2 right (-100)

	got := decodeMSADPCM(blk, 2, 2, len(blk), nil)
	// samplesPerBlock=2, so we get 2 samples per channel: samp2 then samp1
	// Interleaved: [samp2L, samp2R, samp1L, samp1R]
	want := []int16{100, -100, 200, -200}
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d\ngot: %v", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("sample[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestDecodeMSADPCM_DstReuse(t *testing.T) {
	src := buildMSMonoBlock(0, 16, 42, 0, nil)
	dst := make([]int16, 0, 128)
	got := decodeMSADPCM(src, 1, 2, len(src), dst)
	if len(got) != 2 {
		t.Fatalf("got %v, want 2 samples", got)
	}
	if cap(got) != 128 {
		t.Errorf("dst buffer not reused: cap = %d, want 128", cap(got))
	}
}

func BenchmarkDecodeIMAADPCM(b *testing.B) {
	const spb = 505
	// Build a mono block with realistic data bytes.
	nibbleBytes := make([]byte, (spb-1+1)/2)
	for i := range nibbleBytes {
		nibbleBytes[i] = 0x37 // mix of nibbles
	}
	src := buildIMAMonoBlock(0, 20, nibbleBytes)
	dst := make([]int16, spb)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		decodeIMAADPCM(src, 1, spb, len(src), dst)
	}
}

func BenchmarkDecodeIMAADPCM_Stereo(b *testing.B) {
	const spb = 505
	chunks := (spb - 1 + 7) / 8
	nibbleBytes := make([]byte, chunks*4*2)
	for i := range nibbleBytes {
		nibbleBytes[i] = 0x37
	}
	src := buildIMAStereoBlock(0, 0, 20, 20, nibbleBytes)
	dst := make([]int16, spb*2)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		decodeIMAADPCM(src, 2, spb, len(src), dst)
	}
}

func BenchmarkDecodeMSADPCM(b *testing.B) {
	const spb = 500
	const blockAlign = 256
	blk := make([]byte, blockAlign)
	blk[0] = 1 // predIdx
	binary.LittleEndian.PutUint16(blk[1:], 64) // delta
	binary.LittleEndian.PutUint16(blk[3:], 1000) // samp1
	binary.LittleEndian.PutUint16(blk[5:], 500) // samp2
	for i := 7; i < blockAlign; i++ {
		blk[i] = 0x35
	}
	dst := make([]int16, spb)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		decodeMSADPCM(blk, 1, spb, blockAlign, dst)
	}
}

func TestDecodeIMAADPCM_Clamp(t *testing.T) {
	// Test clamping: start near max and push positive.
	// stepIndex 40 → step=400. Nibble 0x07 → diff = 400>>3 + 400 + 200 + 100 = 750
	src := buildIMAMonoBlock(32700, 40, []byte{0x77})
	got := decodeIMAADPCM(src, 1, 3, len(src), nil)
	if len(got) != 3 {
		t.Fatalf("got %d samples, want 3", len(got))
	}
	if got[0] != 32700 {
		t.Errorf("sample[0] = %d, want 32700", got[0])
	}
	// Should be clamped to 32767
	if got[1] != 32767 {
		t.Errorf("sample[1] = %d, want 32767 (clamped)", got[1])
	}
}
