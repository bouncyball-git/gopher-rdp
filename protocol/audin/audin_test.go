package audin

import (
	"encoding/binary"
	"log/slog"
	"testing"
)

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

func TestEncodeDecodeAudioFormat(t *testing.T) {
	tests := []struct {
		name string
		fmt  AudioFormat
	}{
		{"PCM mono 8kHz 16-bit", buildPCMFormat(1, 8000, 16)},
		{"PCM stereo 44100Hz 16-bit", buildPCMFormat(2, 44100, 16)},
		{"PCM mono 48kHz 16-bit", buildPCMFormat(1, 48000, 16)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := make([]byte, audioFormatFixedSize)
			encodeAudioFormat(buf, 0, &tt.fmt)

			got, consumed, err := decodeAudioFormat(buf, 0)
			if err != nil {
				t.Fatalf("decodeAudioFormat: %v", err)
			}
			if consumed != audioFormatFixedSize {
				t.Errorf("consumed = %d, want %d", consumed, audioFormatFixedSize)
			}
			if got.Tag != tt.fmt.Tag {
				t.Errorf("Tag = 0x%04X, want 0x%04X", got.Tag, tt.fmt.Tag)
			}
			if got.SamplesPerSec != tt.fmt.SamplesPerSec {
				t.Errorf("SamplesPerSec = %d, want %d", got.SamplesPerSec, tt.fmt.SamplesPerSec)
			}
			if got.BitsPerSample != tt.fmt.BitsPerSample {
				t.Errorf("BitsPerSample = %d, want %d", got.BitsPerSample, tt.fmt.BitsPerSample)
			}
			if got.Channels != tt.fmt.Channels {
				t.Errorf("Channels = %d, want %d", got.Channels, tt.fmt.Channels)
			}
		})
	}
}

func TestDecodeAudioFormat_TooShort(t *testing.T) {
	_, _, err := decodeAudioFormat([]byte{0x01, 0x02}, 0)
	if err == nil {
		t.Fatal("expected error for short input")
	}
}

// buildVersionPDU creates a MSG_SNDIN_VERSION PDU.
func buildVersionPDU(version uint32) []byte {
	buf := make([]byte, 5)
	buf[0] = MsgVersion
	binary.LittleEndian.PutUint32(buf[1:5], version)
	return buf
}

// buildFormatsPDU creates a MSG_SNDIN_FORMATS PDU with the given formats.
func buildFormatsPDU(formats []AudioFormat) []byte {
	fmtSize := 0
	for i := range formats {
		fmtSize += audioFormatFixedSize + len(formats[i].ExtraData)
	}
	buf := make([]byte, 1+8+fmtSize)
	buf[0] = MsgFormats
	binary.LittleEndian.PutUint32(buf[1:5], uint32(len(formats)))
	binary.LittleEndian.PutUint32(buf[5:9], uint32(8+fmtSize))
	off := 9
	for i := range formats {
		off += encodeAudioFormat(buf, off, &formats[i])
	}
	return buf
}

// buildOpenPDU creates a MSG_SNDIN_OPEN PDU.
func buildOpenPDU(framesPerPacket, formatIdx uint32) []byte {
	buf := make([]byte, 9)
	buf[0] = MsgOpen
	binary.LittleEndian.PutUint32(buf[1:5], framesPerPacket)
	binary.LittleEndian.PutUint32(buf[5:9], formatIdx)
	return buf
}

// buildFormatChangePDU creates a MSG_SNDIN_FORMATCHANGE PDU.
func buildFormatChangePDU(formatIdx uint32) []byte {
	buf := make([]byte, 5)
	buf[0] = MsgFormatChange
	binary.LittleEndian.PutUint32(buf[1:5], formatIdx)
	return buf
}

func TestVersionExchange(t *testing.T) {
	var sent []byte
	h := NewHandler(func(data []byte) error {
		sent = make([]byte, len(data))
		copy(sent, data)
		return nil
	}, slog.New(slog.DiscardHandler))

	// Server sends version 2, client should echo it back (max 2)
	h.ProcessPDU(buildVersionPDU(2))

	if sent == nil {
		t.Fatal("no response sent")
	}
	if len(sent) < 5 {
		t.Fatalf("response too short: %d", len(sent))
	}
	if sent[0] != MsgVersion {
		t.Errorf("response type = 0x%02X, want 0x%02X", sent[0], MsgVersion)
	}
	ver := binary.LittleEndian.Uint32(sent[1:5])
	if ver != 2 {
		t.Errorf("response version = %d, want 2", ver)
	}
}

func TestFormatNegotiation_FilterUnsupported(t *testing.T) {
	var sent []byte
	h := NewHandler(func(data []byte) error {
		sent = make([]byte, len(data))
		copy(sent, data)
		return nil
	}, slog.New(slog.DiscardHandler))

	// Server offers PCM + an unsupported format (tag=0x0006 ALAW).
	// Only PCM should be accepted; ALAW is not in our supported set.
	pcm := buildPCMFormat(1, 8000, 16)
	alaw := AudioFormat{Tag: 0x0006, Channels: 1, SamplesPerSec: 8000, AvgBytesPerSec: 8000, BlockAlign: 1, BitsPerSample: 8}
	h.ProcessPDU(buildFormatsPDU([]AudioFormat{pcm, alaw}))

	if sent == nil {
		t.Fatal("no response sent")
	}
	if sent[0] != MsgFormats {
		t.Errorf("response type = 0x%02X, want 0x%02X", sent[0], MsgFormats)
	}
	numFmt := binary.LittleEndian.Uint32(sent[1:5])
	if numFmt != 1 {
		t.Errorf("numFormats = %d, want 1", numFmt)
	}
}

func TestFormatNegotiation_AcceptsADPCM(t *testing.T) {
	var sent []byte
	h := NewHandler(func(data []byte) error {
		sent = make([]byte, len(data))
		copy(sent, data)
		return nil
	}, slog.New(slog.DiscardHandler))

	// Server offers PCM + IMA ADPCM + MS-ADPCM — IMA should be dropped
	// because MS-ADPCM is available (better quality at comparable bitrate).
	pcm := buildPCMFormat(1, 8000, 16)
	ima := AudioFormat{Tag: WaveFormatIMAADPCM, Channels: 1, SamplesPerSec: 8000, AvgBytesPerSec: 4064, BlockAlign: 256, BitsPerSample: 4, ExtraData: []byte{0xF9, 0x01}} // samplesPerBlock=505
	msadpcm := AudioFormat{Tag: WaveFormatADPCM, Channels: 1, SamplesPerSec: 8000, AvgBytesPerSec: 4096, BlockAlign: 256, BitsPerSample: 4, ExtraData: []byte{0xF4, 0x01, 0x07, 0x00}} // samplesPerBlock=500, 7 coeffs
	h.ProcessPDU(buildFormatsPDU([]AudioFormat{pcm, ima, msadpcm}))

	if sent == nil {
		t.Fatal("no response sent")
	}
	numFmt := binary.LittleEndian.Uint32(sent[1:5])
	if numFmt != 2 {
		t.Errorf("numFormats = %d, want 2 (PCM + MS-ADPCM, IMA dropped)", numFmt)
	}
}

func TestFormatNegotiation_NoSupported(t *testing.T) {
	var sent []byte
	h := NewHandler(func(data []byte) error {
		sent = make([]byte, len(data))
		copy(sent, data)
		return nil
	}, slog.New(slog.DiscardHandler))

	alaw := AudioFormat{Tag: 0x0006, Channels: 1, SamplesPerSec: 8000, AvgBytesPerSec: 8000, BlockAlign: 1, BitsPerSample: 8}
	h.ProcessPDU(buildFormatsPDU([]AudioFormat{alaw}))

	if sent == nil {
		t.Fatal("no response sent")
	}
	numFmt := binary.LittleEndian.Uint32(sent[1:5])
	if numFmt != 0 {
		t.Errorf("numFormats = %d, want 0", numFmt)
	}
}

func TestOpenAndRecording(t *testing.T) {
	var sentPDUs [][]byte
	h := NewHandler(func(data []byte) error {
		cp := make([]byte, len(data))
		copy(cp, data)
		sentPDUs = append(sentPDUs, cp)
		return nil
	}, slog.New(slog.DiscardHandler))

	var openedFmt AudioFormat
	h.OnOpen(func(f AudioFormat) {
		openedFmt = f
	})

	// Negotiate formats
	pcm := buildPCMFormat(1, 44100, 16)
	h.ProcessPDU(buildFormatsPDU([]AudioFormat{pcm}))
	sentPDUs = sentPDUs[:0] // clear formats response

	// Open
	h.ProcessPDU(buildOpenPDU(1024, 0))

	if !h.Recording() {
		t.Error("expected recording=true after open")
	}
	if openedFmt.SamplesPerSec != 44100 {
		t.Errorf("opened format rate = %d, want 44100", openedFmt.SamplesPerSec)
	}

	// Should have sent OPEN_REPLY then FORMAT_CHANGE.
	if len(sentPDUs) < 2 {
		t.Fatalf("expected 2 PDUs (OPEN_REPLY + FORMAT_CHANGE), got %d", len(sentPDUs))
	}
	reply := sentPDUs[0]
	if reply[0] != MsgOpenReply {
		t.Errorf("first PDU type = 0x%02X, want 0x%02X (OPEN_REPLY)", reply[0], MsgOpenReply)
	}
	fmtChange := sentPDUs[1]
	if fmtChange[0] != MsgFormatChange {
		t.Errorf("second PDU type = 0x%02X, want 0x%02X (FORMAT_CHANGE)", fmtChange[0], MsgFormatChange)
	}
}

func TestSendAudioData(t *testing.T) {
	var sentPDUs [][]byte
	h := NewHandler(func(data []byte) error {
		cp := make([]byte, len(data))
		copy(cp, data)
		sentPDUs = append(sentPDUs, cp)
		return nil
	}, slog.New(slog.DiscardHandler))

	// Not recording — should be no-op
	if err := h.SendAudioData([]byte{0x01, 0x02}); err != nil {
		t.Fatalf("SendAudioData: %v", err)
	}
	if len(sentPDUs) != 0 {
		t.Error("expected no PDUs when not recording")
	}

	// Set up recording
	pcm := buildPCMFormat(1, 8000, 16)
	h.ProcessPDU(buildFormatsPDU([]AudioFormat{pcm}))
	h.ProcessPDU(buildOpenPDU(1024, 0))
	sentPDUs = sentPDUs[:0]

	// Send audio data
	audio := []byte{0xAA, 0xBB, 0xCC, 0xDD}
	if err := h.SendAudioData(audio); err != nil {
		t.Fatalf("SendAudioData: %v", err)
	}

	if len(sentPDUs) != 2 {
		t.Fatalf("expected 2 PDUs (DATA_INCOMING + DATA), got %d", len(sentPDUs))
	}

	// DATA_INCOMING
	if sentPDUs[0][0] != MsgDataIncoming {
		t.Errorf("first PDU type = 0x%02X, want 0x%02X", sentPDUs[0][0], MsgDataIncoming)
	}

	// DATA
	if sentPDUs[1][0] != MsgData {
		t.Errorf("second PDU type = 0x%02X, want 0x%02X", sentPDUs[1][0], MsgData)
	}
	if len(sentPDUs[1]) != 1+len(audio) {
		t.Errorf("DATA PDU len = %d, want %d", len(sentPDUs[1]), 1+len(audio))
	}
	for i, b := range audio {
		if sentPDUs[1][1+i] != b {
			t.Errorf("DATA[%d] = 0x%02X, want 0x%02X", i, sentPDUs[1][1+i], b)
		}
	}
}

func TestSendAudioData_EmptyNoop(t *testing.T) {
	h := NewHandler(func(data []byte) error {
		t.Fatal("should not send for empty data")
		return nil
	}, slog.New(slog.DiscardHandler))

	h.recording.Store(true)
	if err := h.SendAudioData(nil); err != nil {
		t.Fatalf("SendAudioData(nil): %v", err)
	}
	if err := h.SendAudioData([]byte{}); err != nil {
		t.Fatalf("SendAudioData(empty): %v", err)
	}
}

func TestFormatChange(t *testing.T) {
	var sentPDUs [][]byte
	h := NewHandler(func(data []byte) error {
		cp := make([]byte, len(data))
		copy(cp, data)
		sentPDUs = append(sentPDUs, cp)
		return nil
	}, slog.New(slog.DiscardHandler))

	var lastFmt AudioFormat
	h.OnOpen(func(f AudioFormat) {
		lastFmt = f
	})

	// Negotiate two PCM formats
	pcm8k := buildPCMFormat(1, 8000, 16)
	pcm44k := buildPCMFormat(2, 44100, 16)
	h.ProcessPDU(buildFormatsPDU([]AudioFormat{pcm8k, pcm44k}))
	h.ProcessPDU(buildOpenPDU(1024, 0))

	if lastFmt.SamplesPerSec != 8000 {
		t.Errorf("initial format rate = %d, want 8000", lastFmt.SamplesPerSec)
	}

	// Format change to index 1
	h.ProcessPDU(buildFormatChangePDU(1))

	if lastFmt.SamplesPerSec != 44100 {
		t.Errorf("changed format rate = %d, want 44100", lastFmt.SamplesPerSec)
	}
	if lastFmt.Channels != 2 {
		t.Errorf("changed format channels = %d, want 2", lastFmt.Channels)
	}
}

func TestStop(t *testing.T) {
	h := NewHandler(func(data []byte) error { return nil }, slog.New(slog.DiscardHandler))

	var closeCalled bool
	h.OnClose(func() { closeCalled = true })

	h.recording.Store(true)
	h.Stop()

	if h.Recording() {
		t.Error("expected recording=false after stop")
	}
	if !closeCalled {
		t.Error("OnClose callback not called")
	}

	// Double stop should not call callback again
	closeCalled = false
	h.Stop()
	if closeCalled {
		t.Error("OnClose called on second stop")
	}
}

func TestOpenADPCM_CallerGetsPCMFormat(t *testing.T) {
	h := NewHandler(func(data []byte) error { return nil }, slog.New(slog.DiscardHandler))

	var openedFmt AudioFormat
	h.OnOpen(func(f AudioFormat) {
		openedFmt = f
	})

	// Negotiate IMA ADPCM format.
	ima := AudioFormat{
		Tag: WaveFormatIMAADPCM, Channels: 1, SamplesPerSec: 8000,
		AvgBytesPerSec: 4064, BlockAlign: 256, BitsPerSample: 4,
		ExtraData: []byte{0xF9, 0x01}, // samplesPerBlock=505
	}
	h.ProcessPDU(buildFormatsPDU([]AudioFormat{ima}))
	h.ProcessPDU(buildOpenPDU(1024, 0))

	if !h.Recording() {
		t.Fatal("expected recording=true")
	}

	// Callback should receive PCM format (not ADPCM).
	if openedFmt.Tag != WaveFormatPCM {
		t.Errorf("onOpen tag = 0x%04X, want 0x%04X (PCM)", openedFmt.Tag, WaveFormatPCM)
	}
	if openedFmt.SamplesPerSec != 8000 {
		t.Errorf("onOpen rate = %d, want 8000", openedFmt.SamplesPerSec)
	}
	if openedFmt.BitsPerSample != 16 {
		t.Errorf("onOpen bits = %d, want 16", openedFmt.BitsPerSample)
	}
	if openedFmt.Channels != 1 {
		t.Errorf("onOpen channels = %d, want 1", openedFmt.Channels)
	}

	// ActiveFormat() should also return PCM.
	af := h.ActiveFormat()
	if af.Tag != WaveFormatPCM {
		t.Errorf("ActiveFormat tag = 0x%04X, want PCM", af.Tag)
	}
}

func TestOpenFormatIndexOutOfRange(t *testing.T) {
	h := NewHandler(func(data []byte) error { return nil }, slog.New(slog.DiscardHandler))

	pcm := buildPCMFormat(1, 8000, 16)
	h.ProcessPDU(buildFormatsPDU([]AudioFormat{pcm}))

	// Open with out-of-range index — should not panic or set recording
	h.ProcessPDU(buildOpenPDU(1024, 99))
	if h.Recording() {
		t.Error("should not be recording with invalid format index")
	}
}

func TestProcessPDU_TooShort(t *testing.T) {
	h := NewHandler(func(data []byte) error { return nil }, slog.New(slog.DiscardHandler))
	// Should not panic
	h.ProcessPDU(nil)
	h.ProcessPDU([]byte{})
}

func TestProcessPDU_UnknownType(t *testing.T) {
	h := NewHandler(func(data []byte) error { return nil }, slog.New(slog.DiscardHandler))
	// Should not panic
	h.ProcessPDU([]byte{0xFF, 0x01, 0x02})
}
