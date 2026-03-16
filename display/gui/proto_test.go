//go:build gui

package gui

import (
	"bytes"
	"io"
	"testing"
)

func TestWriteReadMsg(t *testing.T) {
	tests := []struct {
		name    string
		msgType byte
		payload []byte
	}{
		{"empty", MsgDisconnect, nil},
		{"init", MsgInit, EncodeInit(0, 0, 0, 1920, 1080, 0, false)},
		{"resize", MsgResize, EncodeResize(1600, 900)},
		{"keyboard", MsgKeyboard, EncodeKeyboard(0x1C, 0x0100)},
		{"mouse", MsgMouse, EncodeMouse(100, 200, 0x0800)},
		{"wheel", MsgWheel, EncodeWheel(50, 75, -120, false)},
		{"unicode", MsgUnicodeInput, EncodeUnicode(0x00E9, 0)},
		{"clipboard", MsgClipboard, []byte("hello world")},
		{"cursor_null", MsgCursor, []byte{CursorNull}},
		{"cursor_cached", MsgCursor, EncodeCursorCached(42)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteMsg(&buf, tt.msgType, tt.payload); err != nil {
				t.Fatalf("WriteMsg: %v", err)
			}

			gotType, gotPayload, err := ReadMsg(&buf, nil)
			if err != nil {
				t.Fatalf("ReadMsg: %v", err)
			}
			if gotType != tt.msgType {
				t.Errorf("type: got %d, want %d", gotType, tt.msgType)
			}
			if !bytes.Equal(gotPayload, tt.payload) {
				t.Errorf("payload mismatch: got %d bytes, want %d bytes", len(gotPayload), len(tt.payload))
			}
		})
	}
}

func TestBitmapRoundTrip(t *testing.T) {
	rgba := make([]byte, 10*10*4)
	for i := range rgba {
		rgba[i] = byte(i % 256)
	}

	var buf bytes.Buffer
	payload := make([]byte, 8+len(rgba))
	n := EncodeBitmapInto(payload, 5, 10, 10, 10, rgba)
	if n != 8+len(rgba) {
		t.Fatalf("EncodeBitmapInto returned %d, want %d", n, 8+len(rgba))
	}

	if err := WriteMsg(&buf, MsgBitmap, payload[:n]); err != nil {
		t.Fatal(err)
	}

	gotType, gotPayload, err := ReadMsg(&buf, nil)
	if err != nil {
		t.Fatal(err)
	}
	if gotType != MsgBitmap {
		t.Fatalf("type: got %d, want %d", gotType, MsgBitmap)
	}

	x, y, w, h, gotRGBA := DecodeBitmap(gotPayload)
	if x != 5 || y != 10 || w != 10 || h != 10 {
		t.Errorf("bitmap coords: got (%d,%d,%d,%d), want (5,10,10,10)", x, y, w, h)
	}
	if !bytes.Equal(gotRGBA, rgba) {
		t.Error("pixel data mismatch")
	}
}

func TestInitRoundTrip(t *testing.T) {
	payload := EncodeInit(2, -1920, 0, 1920, 1080, 1, true)
	monIdx, offX, offY, w, h, kbMode, primary := DecodeInit(payload)
	if monIdx != 2 || offX != -1920 || offY != 0 || w != 1920 || h != 1080 || kbMode != 1 || !primary {
		t.Errorf("init decode mismatch: %d %d %d %d %d %d %v", monIdx, offX, offY, w, h, kbMode, primary)
	}
}

func TestAudioRoundTrip(t *testing.T) {
	pcm := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	payload := EncodeAudio(2, 44100, 16, pcm)
	ch, rate, bps, gotPCM := DecodeAudio(payload)
	if ch != 2 || rate != 44100 || bps != 16 {
		t.Errorf("audio format: got %d/%d/%d", ch, rate, bps)
	}
	if !bytes.Equal(gotPCM, pcm) {
		t.Error("pcm data mismatch")
	}
}

func TestMultipleMessages(t *testing.T) {
	// Write several messages, read them all back.
	var buf bytes.Buffer
	WriteMsg(&buf, MsgDisconnect, nil)
	WriteMsg(&buf, MsgResize, EncodeResize(800, 600))
	WriteMsg(&buf, MsgClipboard, []byte("test"))

	var readBuf [256]byte

	typ1, _, err := ReadMsg(&buf, readBuf[:])
	if err != nil || typ1 != MsgDisconnect {
		t.Fatalf("msg1: type=%d err=%v", typ1, err)
	}
	typ2, p2, err := ReadMsg(&buf, readBuf[:])
	if err != nil || typ2 != MsgResize {
		t.Fatalf("msg2: type=%d err=%v", typ2, err)
	}
	w, h := DecodeResize(p2)
	if w != 800 || h != 600 {
		t.Errorf("resize: %dx%d", w, h)
	}
	typ3, p3, err := ReadMsg(&buf, readBuf[:])
	if err != nil || typ3 != MsgClipboard {
		t.Fatalf("msg3: type=%d err=%v", typ3, err)
	}
	if string(p3) != "test" {
		t.Errorf("clipboard: %q", p3)
	}

	// Should be EOF now.
	_, _, err = ReadMsg(&buf, readBuf[:])
	if err != io.EOF {
		t.Errorf("expected EOF, got %v", err)
	}
}

func TestReadMsgReusesBuffer(t *testing.T) {
	var buf bytes.Buffer
	WriteMsg(&buf, MsgResize, EncodeResize(1920, 1080))

	bigBuf := make([]byte, 1024)
	_, payload, err := ReadMsg(&buf, bigBuf)
	if err != nil {
		t.Fatal(err)
	}
	// payload should reuse bigBuf (same backing array).
	if &payload[0] != &bigBuf[0] {
		t.Error("ReadMsg did not reuse provided buffer")
	}
}
