//go:build gui

package gui

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Broker → Child message types.
const (
	MsgInit       byte = 0x00 // [mon_idx:u8][off_x:i32][off_y:i32][w:u16][h:u16][kb_mode:u8][primary:u8]
	MsgBitmap     byte = 0x01 // [x:u16][y:u16][w:u16][h:u16][rgba...]
	MsgCursor     byte = 0x02 // [subtype:u8][data...]
	MsgDisconnect byte = 0x03 // (empty)
	MsgResize     byte = 0x04 // [w:u16][h:u16]
	MsgClipboard  byte = 0x05 // [utf8...]
	MsgAudio      byte = 0x06 // [channels:u16][rate:u32][bps:u16][pcm...]
	MsgBeginPaint byte = 0x07 // (empty)
	MsgEndPaint   byte = 0x08 // (empty)
)

// Child → Broker message types.
const (
	MsgKeyboard        byte = 0x01 // [scancode:u16][flags:u16]
	MsgMouse           byte = 0x02 // [x:u16][y:u16][buttons:u16]
	MsgWheel           byte = 0x03 // [x:u16][y:u16][delta:i16][horiz:u8]
	MsgChildResize     byte = 0x04 // [w:u16][h:u16]
	MsgChildClipboard  byte = 0x05 // [utf8...]
	MsgUnicodeInput    byte = 0x06 // [codepoint:u16][flags:u16]
	MsgChildDisconnect byte = 0x07 // (empty) — child requests session disconnect
)

// Cursor subtypes within MsgCursor.
const (
	CursorNull    byte = 0
	CursorDefault byte = 1
	CursorShape   byte = 2
	CursorCached  byte = 3
)

// WriteMsg writes a length-prefixed message: [len:u32 LE][type:u8][payload...].
// The length includes the type byte + payload.
func WriteMsg(w io.Writer, msgType byte, payload []byte) error {
	totalLen := 1 + len(payload)
	var hdr [5]byte
	binary.LittleEndian.PutUint32(hdr[:4], uint32(totalLen))
	hdr[4] = msgType
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if len(payload) > 0 {
		_, err := w.Write(payload)
		return err
	}
	return nil
}

// ReadMsg reads a length-prefixed message. It reuses buf if large enough,
// otherwise allocates. Returns the message type and payload (excluding type byte).
func ReadMsg(r io.Reader, buf []byte) (msgType byte, payload []byte, err error) {
	var hdr [5]byte
	if _, err = io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	totalLen := int(binary.LittleEndian.Uint32(hdr[:4]))
	msgType = hdr[4]
	payloadLen := totalLen - 1
	if payloadLen < 0 {
		return 0, nil, fmt.Errorf("invalid message length %d", totalLen)
	}
	if payloadLen == 0 {
		return msgType, nil, nil
	}
	if cap(buf) >= payloadLen {
		payload = buf[:payloadLen]
	} else {
		payload = make([]byte, payloadLen)
	}
	if _, err = io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return msgType, payload, nil
}

// EncodeBitmapInto encodes a bitmap message payload into dst (zero-alloc hot path).
// Returns the number of bytes written. Caller must ensure dst is large enough:
// 8 + len(rgba) bytes.
func EncodeBitmapInto(dst []byte, x, y, w, h uint16, rgba []byte) int {
	binary.LittleEndian.PutUint16(dst[0:2], x)
	binary.LittleEndian.PutUint16(dst[2:4], y)
	binary.LittleEndian.PutUint16(dst[4:6], w)
	binary.LittleEndian.PutUint16(dst[6:8], h)
	copy(dst[8:], rgba)
	return 8 + len(rgba)
}

// DecodeBitmap decodes a MsgBitmap payload.
func DecodeBitmap(p []byte) (x, y, w, h uint16, rgba []byte) {
	x = binary.LittleEndian.Uint16(p[0:2])
	y = binary.LittleEndian.Uint16(p[2:4])
	w = binary.LittleEndian.Uint16(p[4:6])
	h = binary.LittleEndian.Uint16(p[6:8])
	rgba = p[8:]
	return
}

// EncodeInit encodes a MsgInit payload.
func EncodeInit(monIdx uint8, offX, offY int32, w, h uint16, kbMode uint8, primary bool) []byte {
	var buf [15]byte
	buf[0] = monIdx
	binary.LittleEndian.PutUint32(buf[1:5], uint32(offX))
	binary.LittleEndian.PutUint32(buf[5:9], uint32(offY))
	binary.LittleEndian.PutUint16(buf[9:11], w)
	binary.LittleEndian.PutUint16(buf[11:13], h)
	buf[13] = kbMode
	if primary {
		buf[14] = 1
	}
	return buf[:]
}

// DecodeInit decodes a MsgInit payload.
func DecodeInit(p []byte) (monIdx uint8, offX, offY int32, w, h uint16, kbMode uint8, primary bool) {
	monIdx = p[0]
	offX = int32(binary.LittleEndian.Uint32(p[1:5]))
	offY = int32(binary.LittleEndian.Uint32(p[5:9]))
	w = binary.LittleEndian.Uint16(p[9:11])
	h = binary.LittleEndian.Uint16(p[11:13])
	kbMode = p[13]
	if len(p) > 14 {
		primary = p[14] != 0
	}
	return
}

// EncodeResize encodes a MsgResize or MsgChildResize payload.
func EncodeResize(w, h uint16) []byte {
	var buf [4]byte
	binary.LittleEndian.PutUint16(buf[0:2], w)
	binary.LittleEndian.PutUint16(buf[2:4], h)
	return buf[:]
}

// DecodeResize decodes a MsgResize or MsgChildResize payload.
func DecodeResize(p []byte) (w, h uint16) {
	w = binary.LittleEndian.Uint16(p[0:2])
	h = binary.LittleEndian.Uint16(p[2:4])
	return
}

// EncodeKeyboard encodes a MsgKeyboard payload.
func EncodeKeyboard(scancode, flags uint16) []byte {
	var buf [4]byte
	binary.LittleEndian.PutUint16(buf[0:2], scancode)
	binary.LittleEndian.PutUint16(buf[2:4], flags)
	return buf[:]
}

// DecodeKeyboard decodes a MsgKeyboard payload.
func DecodeKeyboard(p []byte) (scancode, flags uint16) {
	scancode = binary.LittleEndian.Uint16(p[0:2])
	flags = binary.LittleEndian.Uint16(p[2:4])
	return
}

// EncodeMouse encodes a MsgMouse payload.
func EncodeMouse(x, y, buttons uint16) []byte {
	var buf [6]byte
	binary.LittleEndian.PutUint16(buf[0:2], x)
	binary.LittleEndian.PutUint16(buf[2:4], y)
	binary.LittleEndian.PutUint16(buf[4:6], buttons)
	return buf[:]
}

// DecodeMouse decodes a MsgMouse payload.
func DecodeMouse(p []byte) (x, y, buttons uint16) {
	x = binary.LittleEndian.Uint16(p[0:2])
	y = binary.LittleEndian.Uint16(p[2:4])
	buttons = binary.LittleEndian.Uint16(p[4:6])
	return
}

// EncodeWheel encodes a MsgWheel payload.
func EncodeWheel(x, y uint16, delta int16, horiz bool) []byte {
	var buf [7]byte
	binary.LittleEndian.PutUint16(buf[0:2], x)
	binary.LittleEndian.PutUint16(buf[2:4], y)
	binary.LittleEndian.PutUint16(buf[4:6], uint16(delta))
	if horiz {
		buf[6] = 1
	}
	return buf[:]
}

// DecodeWheel decodes a MsgWheel payload.
func DecodeWheel(p []byte) (x, y uint16, delta int16, horiz bool) {
	x = binary.LittleEndian.Uint16(p[0:2])
	y = binary.LittleEndian.Uint16(p[2:4])
	delta = int16(binary.LittleEndian.Uint16(p[4:6]))
	horiz = p[6] != 0
	return
}

// EncodeUnicode encodes a MsgUnicodeInput payload.
func EncodeUnicode(codepoint, flags uint16) []byte {
	var buf [4]byte
	binary.LittleEndian.PutUint16(buf[0:2], codepoint)
	binary.LittleEndian.PutUint16(buf[2:4], flags)
	return buf[:]
}

// DecodeUnicode decodes a MsgUnicodeInput payload.
func DecodeUnicode(p []byte) (codepoint, flags uint16) {
	codepoint = binary.LittleEndian.Uint16(p[0:2])
	flags = binary.LittleEndian.Uint16(p[2:4])
	return
}

// EncodeCursorShape encodes cursor shape data for MsgCursor.
func EncodeCursorShape(cacheIndex, hotX, hotY, w, h uint16, rgba []byte) []byte {
	buf := make([]byte, 1+10+len(rgba))
	buf[0] = CursorShape
	binary.LittleEndian.PutUint16(buf[1:3], cacheIndex)
	binary.LittleEndian.PutUint16(buf[3:5], hotX)
	binary.LittleEndian.PutUint16(buf[5:7], hotY)
	binary.LittleEndian.PutUint16(buf[7:9], w)
	binary.LittleEndian.PutUint16(buf[9:11], h)
	copy(buf[11:], rgba)
	return buf
}

// EncodeCursorCached encodes a cached cursor for MsgCursor.
func EncodeCursorCached(cacheIndex uint16) []byte {
	var buf [3]byte
	buf[0] = CursorCached
	binary.LittleEndian.PutUint16(buf[1:3], cacheIndex)
	return buf[:]
}

// EncodeAudio encodes a MsgAudio payload.
func EncodeAudio(channels uint16, rate uint32, bps uint16, pcm []byte) []byte {
	buf := make([]byte, 8+len(pcm))
	binary.LittleEndian.PutUint16(buf[0:2], channels)
	binary.LittleEndian.PutUint32(buf[2:6], rate)
	binary.LittleEndian.PutUint16(buf[6:8], bps)
	copy(buf[8:], pcm)
	return buf
}

// DecodeAudio decodes a MsgAudio payload.
func DecodeAudio(p []byte) (channels uint16, rate uint32, bps uint16, pcm []byte) {
	channels = binary.LittleEndian.Uint16(p[0:2])
	rate = binary.LittleEndian.Uint32(p[2:6])
	bps = binary.LittleEndian.Uint16(p[6:8])
	pcm = p[8:]
	return
}
