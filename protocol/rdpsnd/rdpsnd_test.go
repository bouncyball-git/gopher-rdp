package rdpsnd

import (
	"encoding/binary"
	"log/slog"
	"testing"
)

func TestEncodeDecode_SNDPROLOG(t *testing.T) {
	tests := []struct {
		name    string
		msgType byte
		body    []byte
	}{
		{"empty body", SNDCClose, nil},
		{"training body", SNDCTraining, []byte{0x10, 0x20, 0x30, 0x40}},
		{"formats body", SNDCFormats, make([]byte, 100)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pdu := encodePDU(tt.msgType, tt.body)

			msgType, body, err := decodePDU(pdu)
			if err != nil {
				t.Fatalf("decodePDU: %v", err)
			}
			if msgType != tt.msgType {
				t.Errorf("msgType = 0x%02X, want 0x%02X", msgType, tt.msgType)
			}
			if len(body) != len(tt.body) {
				t.Errorf("body len = %d, want %d", len(body), len(tt.body))
			}
		})
	}
}

func TestDecodePDU_TooShort(t *testing.T) {
	_, _, err := decodePDU([]byte{0x01, 0x02})
	if err == nil {
		t.Fatal("expected error for 2-byte input")
	}
}

// buildPCMFormat creates a PCM AUDIO_FORMAT for testing.
func buildPCMFormat(channels uint16, rate uint32, bits uint16) AudioFormat {
	return AudioFormat{
		Tag:            WaveFormatPCM,
		Channels:       channels,
		SamplesPerSec:  rate,
		AvgBytesPerSec: rate * uint32(channels) * uint32(bits/8),
		BlockAlign:     channels * (bits / 8),
		BitsPerSample:  bits,
	}
}

func TestDecodeAudioFormat_PCM(t *testing.T) {
	// Encode a PCM 44100/16/stereo format, then decode it.
	want := buildPCMFormat(2, 44100, 16)
	buf := make([]byte, audioFormatFixedSize)
	encodeAudioFormat(buf, 0, &want)

	got, consumed, err := decodeAudioFormat(buf, 0)
	if err != nil {
		t.Fatalf("decodeAudioFormat: %v", err)
	}
	if consumed != audioFormatFixedSize {
		t.Errorf("consumed = %d, want %d", consumed, audioFormatFixedSize)
	}
	if got.Tag != WaveFormatPCM {
		t.Errorf("Tag = 0x%04X, want 0x%04X", got.Tag, WaveFormatPCM)
	}
	if got.SamplesPerSec != 44100 {
		t.Errorf("SamplesPerSec = %d, want 44100", got.SamplesPerSec)
	}
	if got.BitsPerSample != 16 {
		t.Errorf("BitsPerSample = %d, want 16", got.BitsPerSample)
	}
	if got.Channels != 2 {
		t.Errorf("Channels = %d, want 2", got.Channels)
	}
}

func TestDecodeAudioFormat_WithExtraData(t *testing.T) {
	extra := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	f := AudioFormat{
		Tag:            0x0055, // MP3
		Channels:       2,
		SamplesPerSec:  44100,
		AvgBytesPerSec: 16000,
		BlockAlign:     1,
		BitsPerSample:  0,
		ExtraData:      extra,
	}
	buf := make([]byte, audioFormatFixedSize+len(extra))
	n := encodeAudioFormat(buf, 0, &f)
	if n != audioFormatFixedSize+len(extra) {
		t.Fatalf("encoded %d bytes, want %d", n, audioFormatFixedSize+len(extra))
	}

	got, consumed, err := decodeAudioFormat(buf, 0)
	if err != nil {
		t.Fatalf("decodeAudioFormat: %v", err)
	}
	if consumed != n {
		t.Errorf("consumed = %d, want %d", consumed, n)
	}
	if len(got.ExtraData) != 4 {
		t.Fatalf("ExtraData len = %d, want 4", len(got.ExtraData))
	}
	for i, b := range got.ExtraData {
		if b != extra[i] {
			t.Errorf("ExtraData[%d] = 0x%02X, want 0x%02X", i, b, extra[i])
		}
	}
}

func TestAudioFormatRoundTrip(t *testing.T) {
	formats := []AudioFormat{
		buildPCMFormat(1, 8000, 8),
		buildPCMFormat(2, 44100, 16),
		buildPCMFormat(1, 22050, 16),
	}
	for _, want := range formats {
		buf := make([]byte, audioFormatFixedSize)
		encodeAudioFormat(buf, 0, &want)
		got, _, err := decodeAudioFormat(buf, 0)
		if err != nil {
			t.Fatalf("decodeAudioFormat: %v", err)
		}
		if got.Tag != want.Tag || got.Channels != want.Channels ||
			got.SamplesPerSec != want.SamplesPerSec || got.BitsPerSample != want.BitsPerSample ||
			got.BlockAlign != want.BlockAlign || got.AvgBytesPerSec != want.AvgBytesPerSec {
			t.Errorf("round-trip mismatch: got %+v, want %+v", got, want)
		}
	}
}

// buildServerFormatsPDU constructs a fake SNDC_FORMATS PDU from the server.
func buildServerFormatsPDU(version uint16, formats []AudioFormat) []byte {
	fmtSize := 0
	for i := range formats {
		fmtSize += audioFormatFixedSize + len(formats[i].ExtraData)
	}
	body := make([]byte, formatsHeaderSize+fmtSize)
	// dwFlags=ALIVE|VOLUME at [0:4]
	binary.LittleEndian.PutUint32(body[0:4], 0x0003)
	// dwVolume at [4:8] = 0xFFFFFFFF
	binary.LittleEndian.PutUint32(body[4:8], 0xFFFFFFFF)
	// [14:16] wNumberOfFormats
	binary.LittleEndian.PutUint16(body[14:16], uint16(len(formats)))
	// [17:19] wVersion
	binary.LittleEndian.PutUint16(body[17:19], version)

	off := formatsHeaderSize
	for i := range formats {
		off += encodeAudioFormat(body, off, &formats[i])
	}
	return encodePDU(SNDCFormats, body[:off])
}

func TestHandshake_ServerFormats(t *testing.T) {
	// Server offers 3 formats: PCM 44100/16/2, ADPCM, PCM 22050/8/1
	pcm1 := buildPCMFormat(2, 44100, 16)
	adpcm := AudioFormat{Tag: 0x0002, Channels: 2, SamplesPerSec: 44100,
		AvgBytesPerSec: 22050, BlockAlign: 256, BitsPerSample: 4}
	pcm2 := buildPCMFormat(2, 22050, 8)

	serverPDU := buildServerFormatsPDU(6, []AudioFormat{pcm1, adpcm, pcm2})

	var sent [][]byte
	h := NewHandler(func(data []byte) error {
		cp := make([]byte, len(data))
		copy(cp, data)
		sent = append(sent, cp)
		return nil
	}, slog.New(slog.DiscardHandler))

	h.ProcessPDU(serverPDU)

	if !h.ready {
		t.Fatal("handler not ready after format exchange")
	}
	// Should have accepted all 3 formats (PCM + ADPCM + PCM)
	if len(h.clientFormats) != 3 {
		t.Fatalf("clientFormats = %d, want 3", len(h.clientFormats))
	}
	if h.clientFormats[0].Tag != WaveFormatPCM || h.clientFormats[0].SamplesPerSec != 44100 {
		t.Errorf("clientFormats[0]: tag=%d rate=%d, want PCM/44100", h.clientFormats[0].Tag, h.clientFormats[0].SamplesPerSec)
	}
	if h.clientFormats[1].Tag != WaveFormatADPCM {
		t.Errorf("clientFormats[1].Tag = %d, want ADPCM (0x0002)", h.clientFormats[1].Tag)
	}
	if h.clientFormats[2].Tag != WaveFormatPCM || h.clientFormats[2].SamplesPerSec != 22050 {
		t.Errorf("clientFormats[2]: tag=%d rate=%d, want PCM/22050", h.clientFormats[2].Tag, h.clientFormats[2].SamplesPerSec)
	}

	// Verify client response PDUs: formats + quality mode (version >= 6)
	if len(sent) != 2 {
		t.Fatalf("sent %d PDUs, want 2 (formats + quality mode)", len(sent))
	}
	respType, respBody, err := decodePDU(sent[0])
	if err != nil {
		t.Fatalf("decodePDU(response): %v", err)
	}
	if respType != SNDCFormats {
		t.Errorf("response type = 0x%02X, want 0x%02X", respType, SNDCFormats)
	}
	if len(respBody) < formatsHeaderSize {
		t.Fatalf("response body too short (%d bytes)", len(respBody))
	}
	// Check dwFlags includes ALIVE
	flags := binary.LittleEndian.Uint32(respBody[0:4])
	if flags&uint32(TSSNDCapsAlive) == 0 {
		t.Errorf("response dwFlags=0x%08X, missing TSSNDCAPS_ALIVE", flags)
	}
	// Check format count = 3 (2 PCM + 1 ADPCM)
	numFmt := binary.LittleEndian.Uint16(respBody[14:16])
	if numFmt != 3 {
		t.Errorf("response wNumberOfFormats = %d, want 3", numFmt)
	}
	// Check cLastBlockConfirmed = 0
	if respBody[16] != 0 {
		t.Errorf("response cLastBlockConfirmed = %d, want 0", respBody[16])
	}
	// Check version clamped
	ver := binary.LittleEndian.Uint16(respBody[17:19])
	if ver != 6 {
		t.Errorf("response wVersion = %d, want 6", ver)
	}
	// Verify Quality Mode PDU
	qmType, qmBody, err := decodePDU(sent[1])
	if err != nil {
		t.Fatalf("decodePDU(quality mode): %v", err)
	}
	if qmType != SNDCQualityMode {
		t.Errorf("quality mode type = 0x%02X, want 0x%02X", qmType, SNDCQualityMode)
	}
	if len(qmBody) < 4 {
		t.Fatalf("quality mode body too short (%d bytes)", len(qmBody))
	}
	qm := binary.LittleEndian.Uint16(qmBody[0:2])
	if qm != QualityModeHigh {
		t.Errorf("wQualityMode = %d, want %d (HIGH_QUALITY)", qm, QualityModeHigh)
	}
}

func TestHandshake_VersionClamp(t *testing.T) {
	pcm := buildPCMFormat(1, 8000, 8)
	// Server version 12 → should be clamped to 8
	serverPDU := buildServerFormatsPDU(12, []AudioFormat{pcm})

	var sent [][]byte
	h := NewHandler(func(data []byte) error {
		cp := make([]byte, len(data))
		copy(cp, data)
		sent = append(sent, cp)
		return nil
	}, slog.New(slog.DiscardHandler))
	h.ProcessPDU(serverPDU)

	// formats + quality mode (version 12 clamped to 8, which is >= 6)
	if len(sent) != 2 {
		t.Fatalf("sent %d PDUs, want 2", len(sent))
	}
	_, respBody, _ := decodePDU(sent[0])
	ver := binary.LittleEndian.Uint16(respBody[17:19])
	if ver != 8 {
		t.Errorf("response wVersion = %d, want 8 (clamped)", ver)
	}
	// Quality mode should also be sent
	qmType, _, _ := decodePDU(sent[1])
	if qmType != SNDCQualityMode {
		t.Errorf("second PDU type = 0x%02X, want SNDC_QUALITYMODE (0x%02X)", qmType, SNDCQualityMode)
	}
}

func TestTrainingEcho(t *testing.T) {
	var sent [][]byte
	h := NewHandler(func(data []byte) error {
		cp := make([]byte, len(data))
		copy(cp, data)
		sent = append(sent, cp)
		return nil
	}, slog.New(slog.DiscardHandler))

	// Build SNDC_TRAINING: wTimeStamp=0x1234, wPackSize=0x0100
	var trainingBody [4]byte
	binary.LittleEndian.PutUint16(trainingBody[0:2], 0x1234)
	binary.LittleEndian.PutUint16(trainingBody[2:4], 0x0100)
	pdu := encodePDU(SNDCTraining, trainingBody[:])

	h.ProcessPDU(pdu)

	if len(sent) != 1 {
		t.Fatalf("sent %d PDUs, want 1", len(sent))
	}
	// Verify echoed timestamp and pack size
	resp := sent[0]
	if resp[0] != SNDCTraining {
		t.Errorf("response type = 0x%02X, want 0x%02X", resp[0], SNDCTraining)
	}
	ts := binary.LittleEndian.Uint16(resp[4:6])
	if ts != 0x1234 {
		t.Errorf("response wTimeStamp = 0x%04X, want 0x1234", ts)
	}
	ps := binary.LittleEndian.Uint16(resp[6:8])
	if ps != 0x0100 {
		t.Errorf("response wPackSize = 0x%04X, want 0x0100", ps)
	}
}

func TestWave_TwoPart(t *testing.T) {
	pcm := buildPCMFormat(1, 8000, 8)
	var sent [][]byte
	var receivedSample *AudioSample

	h := NewHandler(func(data []byte) error {
		cp := make([]byte, len(data))
		copy(cp, data)
		sent = append(sent, cp)
		return nil
	}, slog.New(slog.DiscardHandler))
	h.OnWaveData(func(s *AudioSample) {
		receivedSample = s
	})

	// Set up formats
	h.clientFormats = []AudioFormat{pcm}
	h.ready = true

	// Part 1: WaveInfo
	// Body: wTimeStamp(2) + wFormatNo(2) + cBlockNo(1) + bPad(3) + waveData(4) = 12
	waveInfoBody := make([]byte, 12)
	binary.LittleEndian.PutUint16(waveInfoBody[0:2], 500)  // wTimeStamp
	binary.LittleEndian.PutUint16(waveInfoBody[2:4], 0)    // wFormatNo
	waveInfoBody[4] = 7                                     // cBlockNo
	copy(waveInfoBody[8:12], []byte{0xAA, 0xBB, 0xCC, 0xDD}) // first 4 audio bytes
	waveInfoPDU := encodePDU(SNDCWave, waveInfoBody)
	h.ProcessPDU(waveInfoPDU)

	if !h.pendingWave {
		t.Fatal("pendingWave should be true after WaveInfo")
	}
	if receivedSample != nil {
		t.Fatal("callback should not fire after WaveInfo")
	}

	// Part 2: continuation — first 4 bytes are junk, replaced by saved waveData
	// Audio = [0xAA, 0xBB, 0xCC, 0xDD, 0x01, 0x02, 0x03, 0x04]
	contBody := []byte{0x00, 0x00, 0x00, 0x00, 0x01, 0x02, 0x03, 0x04} // 4 junk + 4 real audio
	contPDU := encodePDU(0x00, contBody) // msgType doesn't matter for continuation
	h.ProcessPDU(contPDU)

	if h.pendingWave {
		t.Fatal("pendingWave should be false after continuation")
	}
	if receivedSample == nil {
		t.Fatal("callback should fire after continuation")
	}

	// Verify audio data: first 4 bytes replaced with saved waveData
	wantAudio := []byte{0xAA, 0xBB, 0xCC, 0xDD, 0x01, 0x02, 0x03, 0x04}
	if len(receivedSample.Data) != len(wantAudio) {
		t.Fatalf("audio len = %d, want %d", len(receivedSample.Data), len(wantAudio))
	}
	for i, b := range receivedSample.Data {
		if b != wantAudio[i] {
			t.Errorf("audio[%d] = 0x%02X, want 0x%02X", i, b, wantAudio[i])
		}
	}
	if receivedSample.WaveTimestamp != 500 {
		t.Errorf("WaveTimestamp = %d, want 500", receivedSample.WaveTimestamp)
	}
	if receivedSample.AudioTimestamp != 0 {
		t.Errorf("AudioTimestamp = %d, want 0 (SNDC_WAVE)", receivedSample.AudioTimestamp)
	}

	// Verify WaveConfirm was sent
	// sent[0] is the WaveConfirm (no format negotiation in this test)
	if len(sent) != 1 {
		t.Fatalf("sent %d PDUs, want 1 (WaveConfirm)", len(sent))
	}
	confirm := sent[0]
	if confirm[0] != SNDCWaveConfirm {
		t.Errorf("confirm type = 0x%02X, want 0x%02X", confirm[0], SNDCWaveConfirm)
	}
	confirmTS := binary.LittleEndian.Uint16(confirm[4:6])
	if confirmTS != 500 {
		t.Errorf("confirm wTimeStamp = %d, want 500", confirmTS)
	}
	if confirm[6] != 7 {
		t.Errorf("confirm cBlockNo = %d, want 7", confirm[6])
	}
}

func TestWave2_SinglePDU(t *testing.T) {
	pcm := buildPCMFormat(2, 44100, 16)
	var sent [][]byte
	var receivedSample *AudioSample

	h := NewHandler(func(data []byte) error {
		cp := make([]byte, len(data))
		copy(cp, data)
		sent = append(sent, cp)
		return nil
	}, slog.New(slog.DiscardHandler))
	h.OnWaveData(func(s *AudioSample) {
		receivedSample = s
	})

	h.clientFormats = []AudioFormat{pcm}
	h.ready = true

	// SNDC_WAVE2 body: wTimeStamp(2) + wFormatNo(2) + cBlockNo(1) + bPad(3) + dwAudioTS(4) + data
	audioPayload := []byte{0x10, 0x20, 0x30, 0x40, 0x50, 0x60}
	wave2Body := make([]byte, 12+len(audioPayload))
	binary.LittleEndian.PutUint16(wave2Body[0:2], 1000)    // wTimeStamp
	binary.LittleEndian.PutUint16(wave2Body[2:4], 0)       // wFormatNo
	wave2Body[4] = 3                                        // cBlockNo
	binary.LittleEndian.PutUint32(wave2Body[8:12], 999999) // dwAudioTimeStamp
	copy(wave2Body[12:], audioPayload)

	pdu := encodePDU(SNDCWave2, wave2Body)
	h.ProcessPDU(pdu)

	if receivedSample == nil {
		t.Fatal("callback should fire for SNDC_WAVE2")
	}
	if len(receivedSample.Data) != len(audioPayload) {
		t.Fatalf("audio len = %d, want %d", len(receivedSample.Data), len(audioPayload))
	}
	for i, b := range receivedSample.Data {
		if b != audioPayload[i] {
			t.Errorf("audio[%d] = 0x%02X, want 0x%02X", i, b, audioPayload[i])
		}
	}
	if receivedSample.WaveTimestamp != 1000 {
		t.Errorf("WaveTimestamp = %d, want 1000", receivedSample.WaveTimestamp)
	}
	if receivedSample.AudioTimestamp != 999999 {
		t.Errorf("AudioTimestamp = %d, want 999999", receivedSample.AudioTimestamp)
	}

	// Verify WaveConfirm
	if len(sent) != 1 {
		t.Fatalf("sent %d PDUs, want 1", len(sent))
	}
	confirm := sent[0]
	if confirm[0] != SNDCWaveConfirm {
		t.Errorf("confirm type = 0x%02X, want 0x%02X", confirm[0], SNDCWaveConfirm)
	}
	if confirm[6] != 3 {
		t.Errorf("confirm cBlockNo = %d, want 3", confirm[6])
	}
}

func TestWave_OutOfRangeFormatNo(t *testing.T) {
	pcm := buildPCMFormat(1, 8000, 8)
	var callbackFired bool
	var sent [][]byte

	h := NewHandler(func(data []byte) error {
		cp := make([]byte, len(data))
		copy(cp, data)
		sent = append(sent, cp)
		return nil
	}, slog.New(slog.DiscardHandler))
	h.OnWaveData(func(s *AudioSample) {
		callbackFired = true
	})
	h.clientFormats = []AudioFormat{pcm}
	h.ready = true

	// SNDC_WAVE2 with formatNo=5 (out of range, we only have 1 format)
	wave2Body := make([]byte, 16)
	binary.LittleEndian.PutUint16(wave2Body[0:2], 100) // wTimeStamp
	binary.LittleEndian.PutUint16(wave2Body[2:4], 5)   // wFormatNo (out of range!)
	wave2Body[4] = 1                                    // cBlockNo
	copy(wave2Body[12:], []byte{0x01, 0x02, 0x03, 0x04})

	pdu := encodePDU(SNDCWave2, wave2Body)
	h.ProcessPDU(pdu)

	if callbackFired {
		t.Error("callback should NOT fire for out-of-range formatNo")
	}
	// WaveConfirm should still be sent
	if len(sent) != 1 {
		t.Fatalf("sent %d PDUs, want 1 (WaveConfirm still sent)", len(sent))
	}
	if sent[0][0] != SNDCWaveConfirm {
		t.Errorf("sent PDU type = 0x%02X, want WaveConfirm", sent[0][0])
	}
}

func TestClose_Callback(t *testing.T) {
	var closeFired bool
	h := NewHandler(func(data []byte) error { return nil }, slog.New(slog.DiscardHandler))
	h.OnClose(func() { closeFired = true })
	h.ready = true

	pdu := encodePDU(SNDCClose, nil)
	h.ProcessPDU(pdu)

	if !closeFired {
		t.Error("close callback should have fired")
	}
	if h.ready {
		t.Error("ready should be false after close")
	}
}

func TestWaveConfirm_LowAlloc(t *testing.T) {
	h := NewHandler(func(data []byte) error { return nil }, slog.New(slog.DiscardHandler))
	allocs := testing.AllocsPerRun(100, func() {
		h.sendWaveConfirm(1234, 5)
	})
	// 1 alloc: [8]byte escapes through sendFn func value (compiler can't prove no-retain)
	if allocs > 1 {
		t.Errorf("sendWaveConfirm allocs = %.0f, want <= 1", allocs)
	}
}

func TestTrainingConfirm_LowAlloc(t *testing.T) {
	h := NewHandler(func(data []byte) error { return nil }, slog.New(slog.DiscardHandler))
	body := []byte{0x12, 0x34, 0x01, 0x00}
	allocs := testing.AllocsPerRun(100, func() {
		h.handleTraining(body)
	})
	// 1 alloc: [8]byte escapes through sendFn func value
	if allocs > 1 {
		t.Errorf("handleTraining allocs = %.0f, want <= 1", allocs)
	}
}
