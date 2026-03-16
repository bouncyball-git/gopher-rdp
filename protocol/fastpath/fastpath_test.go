package fastpath

import (
	"bytes"
	"log/slog"
	"encoding/binary"
	"testing"
)

// buildUpdate builds a single TS_FP_UPDATE entry.
// compression: 0 = none (no compressionFlags byte), 2 = compressed (extra byte).
func buildUpdate(code, frag, compression byte, updateData []byte) []byte {
	hdr := code&0x0F | (frag&0x03)<<4 | (compression&0x03)<<6
	var buf []byte
	buf = append(buf, hdr)
	if compression == 0x2 {
		buf = append(buf, 0x00) // compressionFlags placeholder
	}
	var sizeBuf [2]byte
	binary.LittleEndian.PutUint16(sizeBuf[:], uint16(len(updateData)))
	buf = append(buf, sizeBuf[:]...)
	buf = append(buf, updateData...)
	return buf
}

func TestDecodeUpdatesSingleBitmap(t *testing.T) {
	payload := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	data := buildUpdate(UpdateBitmap, FragSingle, 0, payload)

	updates, err := DecodeUpdates(slog.Default(), data)
	if err != nil {
		t.Fatalf("DecodeUpdates error: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("got %d updates, want 1", len(updates))
	}
	u := updates[0]
	if u.Code != UpdateBitmap {
		t.Errorf("code = %d, want %d", u.Code, UpdateBitmap)
	}
	if u.Frag != FragSingle {
		t.Errorf("frag = %d, want %d", u.Frag, FragSingle)
	}
	if !bytes.Equal(u.Data, payload) {
		t.Errorf("data = %X, want %X", u.Data, payload)
	}
}

func TestDecodeUpdatesMultiple(t *testing.T) {
	bmpData := []byte{0x01, 0x02}
	syncData := []byte{}
	bmpData2 := []byte{0x03, 0x04, 0x05}

	var data []byte
	data = append(data, buildUpdate(UpdateBitmap, FragSingle, 0, bmpData)...)
	data = append(data, buildUpdate(UpdateSynchronize, FragSingle, 0, syncData)...)
	data = append(data, buildUpdate(UpdateBitmap, FragSingle, 0, bmpData2)...)

	updates, err := DecodeUpdates(slog.Default(), data)
	if err != nil {
		t.Fatalf("DecodeUpdates error: %v", err)
	}
	if len(updates) != 3 {
		t.Fatalf("got %d updates, want 3", len(updates))
	}

	if updates[0].Code != UpdateBitmap || len(updates[0].Data) != 2 {
		t.Errorf("update[0]: code=%d len=%d, want bitmap/2", updates[0].Code, len(updates[0].Data))
	}
	if updates[1].Code != UpdateSynchronize || len(updates[1].Data) != 0 {
		t.Errorf("update[1]: code=%d len=%d, want synchronize/0", updates[1].Code, len(updates[1].Data))
	}
	if updates[2].Code != UpdateBitmap || len(updates[2].Data) != 3 {
		t.Errorf("update[2]: code=%d len=%d, want bitmap/3", updates[2].Code, len(updates[2].Data))
	}
}

func TestDecodeUpdatesWithCompression(t *testing.T) {
	payload := []byte{0xAA, 0xBB}
	data := buildUpdate(UpdateBitmap, FragSingle, 0x2, payload)

	updates, err := DecodeUpdates(slog.Default(), data)
	if err != nil {
		t.Fatalf("DecodeUpdates error: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("got %d updates, want 1", len(updates))
	}
	if updates[0].Code != UpdateBitmap {
		t.Errorf("code = %d, want %d", updates[0].Code, UpdateBitmap)
	}
	if !bytes.Equal(updates[0].Data, payload) {
		t.Errorf("data = %X, want %X", updates[0].Data, payload)
	}
}

func TestDecodeUpdatesTruncated(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{
			"ShortHeader",
			// updateHeader byte only, missing size
			[]byte{0x01},
		},
		{
			"ShortSize",
			// updateHeader + 1 byte of size (need 2)
			[]byte{0x01, 0x05},
		},
		{
			"ShortPayload",
			// updateHeader + size=10 but only 2 bytes of data
			func() []byte {
				buf := []byte{0x01}
				var s [2]byte
				binary.LittleEndian.PutUint16(s[:], 10)
				buf = append(buf, s[:]...)
				buf = append(buf, 0x01, 0x02)
				return buf
			}(),
		},
		{
			"ShortCompressionFlags",
			// compression=0x2 but no compressionFlags byte
			[]byte{0x81}, // code=1, compression=0x2
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeUpdates(slog.Default(), tt.data)
			if err == nil {
				t.Fatal("expected error for truncated data")
			}
		})
	}
}

func TestDecodeUpdatesEmpty(t *testing.T) {
	updates, err := DecodeUpdates(slog.Default(), nil)
	if err != nil {
		t.Fatalf("DecodeUpdates error: %v", err)
	}
	if len(updates) != 0 {
		t.Errorf("got %d updates, want 0", len(updates))
	}

	updates, err = DecodeUpdates(slog.Default(), []byte{})
	if err != nil {
		t.Fatalf("DecodeUpdates error: %v", err)
	}
	if len(updates) != 0 {
		t.Errorf("got %d updates, want 0", len(updates))
	}
}

func TestDecodeUpdatesFragmentation(t *testing.T) {
	data := buildUpdate(UpdateBitmap, FragFirst, 0, []byte{0x01})

	updates, err := DecodeUpdates(slog.Default(), data)
	if err != nil {
		t.Fatalf("DecodeUpdates error: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("got %d updates, want 1", len(updates))
	}
	if updates[0].Frag != FragFirst {
		t.Errorf("frag = %d, want %d", updates[0].Frag, FragFirst)
	}
}
