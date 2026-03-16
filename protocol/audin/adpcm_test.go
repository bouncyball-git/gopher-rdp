package audin

import (
	"encoding/binary"
	"math"
	"testing"
)

// --- IMA ADPCM helpers (local decode for round-trip testing) ---

func imaDecodeNibble(nibble byte, stepIndex *int32, predictor int32) int32 {
	step := int32(imaStepTable[*stepIndex])
	diff := step >> 3
	if nibble&4 != 0 {
		diff += step
	}
	if nibble&2 != 0 {
		diff += step >> 1
	}
	if nibble&1 != 0 {
		diff += step >> 2
	}
	if nibble&8 != 0 {
		predictor -= diff
	} else {
		predictor += diff
	}
	if predictor > 32767 {
		predictor = 32767
	} else if predictor < -32768 {
		predictor = -32768
	}
	*stepIndex += int32(imaIndexTable[nibble])
	if *stepIndex < 0 {
		*stepIndex = 0
	} else if *stepIndex > 88 {
		*stepIndex = 88
	}
	return predictor
}

// decodeIMAMono decodes a single mono IMA ADPCM block back to PCM.
func decodeIMAMono(blk []byte, samplesPerBlock int) []int16 {
	if len(blk) < 4 {
		return nil
	}
	predictor := int32(int16(binary.LittleEndian.Uint16(blk[0:])))
	stepIndex := int32(blk[2])
	if stepIndex > 88 {
		stepIndex = 88
	}

	out := make([]int16, 0, samplesPerBlock)
	out = append(out, int16(predictor))

	data := blk[4:]
	for i := 0; i < len(data) && len(out) < samplesPerBlock; i++ {
		nibLo := data[i] & 0x0F
		predictor = imaDecodeNibble(nibLo, &stepIndex, predictor)
		out = append(out, int16(predictor))
		if len(out) >= samplesPerBlock {
			break
		}
		nibHi := data[i] >> 4
		predictor = imaDecodeNibble(nibHi, &stepIndex, predictor)
		out = append(out, int16(predictor))
	}
	return out
}

// decodeIMAStereo decodes a single stereo IMA ADPCM block back to interleaved PCM.
func decodeIMAStereo(blk []byte, samplesPerBlock int) []int16 {
	if len(blk) < 8 {
		return nil
	}
	var predictor [2]int32
	var stepIndex [2]int32
	for ch := range 2 {
		off := ch * 4
		predictor[ch] = int32(int16(binary.LittleEndian.Uint16(blk[off:])))
		stepIndex[ch] = int32(blk[off+2])
		if stepIndex[ch] > 88 {
			stepIndex[ch] = 88
		}
	}

	out := make([]int16, 0, samplesPerBlock*2)
	out = append(out, int16(predictor[0]), int16(predictor[1]))

	data := blk[8:]
	decoded := 1
	byteOff := 0
	for decoded < samplesPerBlock {
		var chSamples [2][8]int16
		var chCount [2]int
		for ch := range 2 {
			for j := 0; j < 4 && byteOff < len(data); j++ {
				b := data[byteOff]
				byteOff++
				predictor[ch] = imaDecodeNibble(b&0x0F, &stepIndex[ch], predictor[ch])
				chSamples[ch][chCount[ch]] = int16(predictor[ch])
				chCount[ch]++
				predictor[ch] = imaDecodeNibble(b>>4, &stepIndex[ch], predictor[ch])
				chSamples[ch][chCount[ch]] = int16(predictor[ch])
				chCount[ch]++
			}
		}
		n := chCount[0]
		if chCount[1] < n {
			n = chCount[1]
		}
		for i := range n {
			out = append(out, chSamples[0][i], chSamples[1][i])
		}
		decoded += n
	}
	return out
}

// --- MS-ADPCM helpers (local decode for round-trip testing) ---

func msDecodeNibble(nibble int32, delta, samp1, samp2 *int32, coeff [2]int32) int32 {
	if nibble >= 8 {
		nibble -= 16
	}
	predictor := ((*samp1)*coeff[0] + (*samp2)*coeff[1]) >> 8
	predictor += nibble * (*delta)
	if predictor > 32767 {
		predictor = 32767
	} else if predictor < -32768 {
		predictor = -32768
	}
	*samp2 = *samp1
	*samp1 = predictor
	idx := nibble
	if idx < 0 {
		idx += 16
	}
	*delta = (*delta * msAdaptTable[idx]) >> 8
	if *delta < 16 {
		*delta = 16
	}
	return predictor
}

// decodeMSMono decodes a single mono MS-ADPCM block back to PCM.
func decodeMSMono(blk []byte, samplesPerBlock int) []int16 {
	if len(blk) < 7 {
		return nil
	}
	idx := int(blk[0])
	if idx > 6 {
		idx = 0
	}
	coeff := msAdpcmCoeffs[idx]
	delta := int32(int16(binary.LittleEndian.Uint16(blk[1:])))
	samp1 := int32(int16(binary.LittleEndian.Uint16(blk[3:])))
	samp2 := int32(int16(binary.LittleEndian.Uint16(blk[5:])))

	out := make([]int16, 0, samplesPerBlock)
	out = append(out, int16(samp2), int16(samp1))

	data := blk[7:]
	for i := 0; i < len(data) && len(out) < samplesPerBlock; i++ {
		nib := int32(data[i] >> 4)
		s := msDecodeNibble(nib, &delta, &samp1, &samp2, coeff)
		out = append(out, int16(s))
		if len(out) >= samplesPerBlock {
			break
		}
		nib = int32(data[i] & 0x0F)
		s = msDecodeNibble(nib, &delta, &samp1, &samp2, coeff)
		out = append(out, int16(s))
	}
	return out
}

// decodeMSStereo decodes a single stereo MS-ADPCM block back to interleaved PCM.
func decodeMSStereo(blk []byte, samplesPerBlock int) []int16 {
	if len(blk) < 14 {
		return nil
	}
	var coeff [2][2]int32
	var delta [2]int32
	var samp1 [2]int32
	var samp2 [2]int32

	off := 0
	for ch := range 2 {
		idx := int(blk[off])
		off++
		if idx > 6 {
			idx = 0
		}
		coeff[ch] = msAdpcmCoeffs[idx]
	}
	for ch := range 2 {
		delta[ch] = int32(int16(binary.LittleEndian.Uint16(blk[off:])))
		off += 2
	}
	for ch := range 2 {
		samp1[ch] = int32(int16(binary.LittleEndian.Uint16(blk[off:])))
		off += 2
	}
	for ch := range 2 {
		samp2[ch] = int32(int16(binary.LittleEndian.Uint16(blk[off:])))
		off += 2
	}

	out := make([]int16, 0, samplesPerBlock*2)
	out = append(out, int16(samp2[0]), int16(samp2[1]), int16(samp1[0]), int16(samp1[1]))

	data := blk[off:]
	decoded := 2
	ch := 0
	for i := 0; i < len(data) && decoded < samplesPerBlock; i++ {
		nib := int32(data[i] >> 4)
		s := msDecodeNibble(nib, &delta[ch], &samp1[ch], &samp2[ch], coeff[ch])
		out = append(out, int16(s))
		if ch == 1 || samplesPerBlock-decoded <= 1 {
			if ch == 1 {
				decoded++
			}
		}
		ch ^= 1

		if decoded >= samplesPerBlock {
			break
		}

		nib = int32(data[i] & 0x0F)
		s = msDecodeNibble(nib, &delta[ch], &samp1[ch], &samp2[ch], coeff[ch])
		out = append(out, int16(s))
		if ch == 1 {
			decoded++
		}
		ch ^= 1
	}
	return out
}

// --- Tests ---

// rmsError computes the root-mean-square error between two int16 slices.
func rmsError(a, b []int16) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	if n == 0 {
		return 0
	}
	var sum float64
	for i := range n {
		d := float64(a[i]) - float64(b[i])
		sum += d * d
	}
	return math.Sqrt(sum / float64(n))
}

func TestEncodeIMAADPCM_Mono_RoundTrip(t *testing.T) {
	// Generate a low-frequency sine wave. IMA ADPCM step index starts at 0
	// and needs warm-up, so use a gentle signal with long period.
	const spb = 505 // samplesPerBlock
	samples := make([]int16, spb)
	for i := range samples {
		samples[i] = int16(8000 * math.Sin(2*math.Pi*float64(i)/200))
	}

	encoded := EncodeIMAADPCM(samples, 1, spb, nil, nil)
	decoded := decodeIMAMono(encoded, spb)

	if len(decoded) != spb {
		t.Fatalf("decoded len = %d, want %d", len(decoded), spb)
	}

	// First sample must be exact (it's in the header).
	if decoded[0] != samples[0] {
		t.Errorf("first sample = %d, want %d", decoded[0], samples[0])
	}

	rms := rmsError(samples, decoded)
	t.Logf("IMA mono RMS error: %.2f", rms)
	if rms > 200 {
		t.Errorf("RMS error too high: %.2f", rms)
	}
}

func TestEncodeIMAADPCM_Stereo_RoundTrip(t *testing.T) {
	const spb = 505 // samplesPerBlock (per-channel)
	samples := make([]int16, spb*2)
	for i := 0; i < spb; i++ {
		samples[i*2] = int16(6000 * math.Sin(2*math.Pi*float64(i)/200))   // left
		samples[i*2+1] = int16(5000 * math.Sin(2*math.Pi*float64(i)/250)) // right
	}

	encoded := EncodeIMAADPCM(samples, 2, spb, nil, nil)
	decoded := decodeIMAStereo(encoded, spb)

	if len(decoded) != spb*2 {
		t.Fatalf("decoded len = %d, want %d", len(decoded), spb*2)
	}

	// First stereo pair must be exact.
	if decoded[0] != samples[0] {
		t.Errorf("first L = %d, want %d", decoded[0], samples[0])
	}
	if decoded[1] != samples[1] {
		t.Errorf("first R = %d, want %d", decoded[1], samples[1])
	}

	rms := rmsError(samples, decoded)
	t.Logf("IMA stereo RMS error: %.2f", rms)
	if rms > 200 {
		t.Errorf("RMS error too high: %.2f", rms)
	}
}

func TestEncodeIMAADPCM_Silence(t *testing.T) {
	const spb = 100
	samples := make([]int16, spb) // all zeros

	encoded := EncodeIMAADPCM(samples, 1, spb, nil, nil)
	decoded := decodeIMAMono(encoded, spb)

	if len(decoded) != spb {
		t.Fatalf("decoded len = %d, want %d", len(decoded), spb)
	}

	// All decoded samples should be 0 for silence input at step index 0.
	for i, s := range decoded {
		if s != 0 {
			t.Errorf("sample[%d] = %d, want 0", i, s)
			break
		}
	}
}

func TestEncodeIMAADPCM_DstReuse(t *testing.T) {
	samples := []int16{100, 200, 300, 400}
	dst := make([]byte, 0, 256)
	got := EncodeIMAADPCM(samples, 1, 4, dst, nil)
	if cap(got) != 256 {
		t.Errorf("dst buffer not reused: cap = %d, want 256", cap(got))
	}
}

func TestEncodeMSADPCM_Mono_RoundTrip(t *testing.T) {
	const spb = 500
	const blockAlign = 256 // typical block size
	samples := make([]int16, spb)
	for i := range samples {
		samples[i] = int16(14000 * math.Sin(2*math.Pi*float64(i)/40))
	}

	encoded := encodeMSADPCM(samples, 1, spb, blockAlign, nil)
	if len(encoded) != blockAlign {
		t.Fatalf("encoded len = %d, want %d", len(encoded), blockAlign)
	}

	decoded := decodeMSMono(encoded, spb)
	if len(decoded) != spb {
		t.Fatalf("decoded len = %d, want %d", len(decoded), spb)
	}

	// First two samples must match header values.
	if decoded[0] != samples[0] {
		t.Errorf("sample[0] = %d, want %d", decoded[0], samples[0])
	}
	if decoded[1] != samples[1] {
		t.Errorf("sample[1] = %d, want %d", decoded[1], samples[1])
	}

	rms := rmsError(samples, decoded)
	t.Logf("MS-ADPCM mono RMS error: %.2f", rms)
	if rms > 400 {
		t.Errorf("RMS error too high: %.2f", rms)
	}
}

func TestEncodeMSADPCM_Stereo_RoundTrip(t *testing.T) {
	const spb = 500
	const blockAlign = 512
	samples := make([]int16, spb*2)
	for i := 0; i < spb; i++ {
		samples[i*2] = int16(11000 * math.Sin(2*math.Pi*float64(i)/55))
		samples[i*2+1] = int16(9000 * math.Sin(2*math.Pi*float64(i)/70))
	}

	encoded := encodeMSADPCM(samples, 2, spb, blockAlign, nil)
	if len(encoded) != blockAlign {
		t.Fatalf("encoded len = %d, want %d", len(encoded), blockAlign)
	}

	decoded := decodeMSStereo(encoded, spb)
	if decoded == nil {
		t.Fatal("decode returned nil")
	}

	// First two samples per channel from header.
	if decoded[0] != samples[0] {
		t.Errorf("sample[0] (L samp2) = %d, want %d", decoded[0], samples[0])
	}
	if decoded[1] != samples[1] {
		t.Errorf("sample[1] (R samp2) = %d, want %d", decoded[1], samples[1])
	}

	rms := rmsError(samples, decoded)
	t.Logf("MS-ADPCM stereo RMS error: %.2f", rms)
	if rms > 400 {
		t.Errorf("RMS error too high: %.2f", rms)
	}
}

func TestEncodeMSADPCM_Silence(t *testing.T) {
	const spb = 100
	const blockAlign = 256
	samples := make([]int16, spb) // all zeros

	encoded := encodeMSADPCM(samples, 1, spb, blockAlign, nil)
	decoded := decodeMSMono(encoded, spb)

	if len(decoded) != spb {
		t.Fatalf("decoded len = %d, want %d", len(decoded), spb)
	}

	// All decoded samples should be near zero.
	for i, s := range decoded {
		if s > 1 || s < -1 {
			t.Errorf("sample[%d] = %d, want ~0", i, s)
			break
		}
	}
}

func TestEncodeMSADPCM_DstReuse(t *testing.T) {
	samples := make([]int16, 100)
	dst := make([]byte, 0, 512)
	got := encodeMSADPCM(samples, 1, 100, 256, dst)
	if cap(got) != 512 {
		t.Errorf("dst buffer not reused: cap = %d, want 512", cap(got))
	}
}

func TestEncodeMSADPCM_InvalidChannels(t *testing.T) {
	got := encodeMSADPCM(nil, 0, 10, 256, nil)
	if len(got) != 0 {
		t.Errorf("expected empty result for 0 channels, got %d bytes", len(got))
	}
	got = encodeMSADPCM(nil, 3, 10, 256, nil)
	if len(got) != 0 {
		t.Errorf("expected empty result for 3 channels, got %d bytes", len(got))
	}
}

func BenchmarkEncodeIMAADPCM(b *testing.B) {
	const spb = 505
	samples := make([]int16, spb)
	for i := range samples {
		samples[i] = int16(8000 * math.Sin(2*math.Pi*float64(i)/200))
	}
	dst := make([]byte, 4+(spb-1+1)/2)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		EncodeIMAADPCM(samples, 1, spb, dst, nil)
	}
}

func BenchmarkEncodeIMAADPCM_Stereo(b *testing.B) {
	const spb = 505
	samples := make([]int16, spb*2)
	for i := 0; i < spb; i++ {
		samples[i*2] = int16(6000 * math.Sin(2*math.Pi*float64(i)/200))
		samples[i*2+1] = int16(5000 * math.Sin(2*math.Pi*float64(i)/250))
	}
	chunks := (spb - 1 + 7) / 8
	dst := make([]byte, 8+chunks*4*2)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		EncodeIMAADPCM(samples, 2, spb, dst, nil)
	}
}

func BenchmarkEncodeMSADPCM(b *testing.B) {
	const spb = 500
	const blockAlign = 256
	samples := make([]int16, spb)
	for i := range samples {
		samples[i] = int16(14000 * math.Sin(2*math.Pi*float64(i)/40))
	}
	dst := make([]byte, blockAlign)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		encodeMSADPCM(samples, 1, spb, blockAlign, dst)
	}
}

func BenchmarkEncodeMSADPCM_Stereo(b *testing.B) {
	const spb = 500
	const blockAlign = 512
	samples := make([]int16, spb*2)
	for i := 0; i < spb; i++ {
		samples[i*2] = int16(11000 * math.Sin(2*math.Pi*float64(i)/55))
		samples[i*2+1] = int16(9000 * math.Sin(2*math.Pi*float64(i)/70))
	}
	dst := make([]byte, blockAlign)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		encodeMSADPCM(samples, 2, spb, blockAlign, dst)
	}
}

func TestEncodeIMAADPCM_InvalidChannels(t *testing.T) {
	got := EncodeIMAADPCM(nil, 0, 10, nil, nil)
	if len(got) != 0 {
		t.Errorf("expected empty result for 0 channels, got %d bytes", len(got))
	}
}
