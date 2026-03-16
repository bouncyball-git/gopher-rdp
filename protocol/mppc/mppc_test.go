package mppc

import (
	"bytes"
	"testing"
)

func TestDecompress_NotCompressed(t *testing.T) {
	d := &Decompressor{}
	input := []byte{0x41, 0x42, 0x43} // "ABC"
	out, err := d.Decompress(input, 0)  // no flags
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(out, input) {
		t.Fatalf("got %v, want %v", out, input)
	}
}

func TestDecompress_FlushResetsHistory(t *testing.T) {
	d := &Decompressor{}
	d.hist[0] = 0xFF
	d.off = 100

	input := []byte{0x41}
	_, _ = d.Decompress(input, FlagFlush)

	if d.off != 0 {
		t.Fatalf("expected off=0 after flush, got %d", d.off)
	}
	if d.hist[0] != 0 {
		t.Fatal("expected history zeroed after flush")
	}
}

func TestDecompress_EmptyCompressed(t *testing.T) {
	d := &Decompressor{}
	out, err := d.Decompress(nil, FlagCompressed|FlagFlush)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected empty output, got %d bytes", len(out))
	}
}

func TestDecompress_ResetOnly(t *testing.T) {
	d := &Decompressor{}
	d.off = 500
	input := []byte{0x01, 0x02}
	out, err := d.Decompress(input, FlagReset)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(out, input) {
		t.Fatalf("got %v, want %v", out, input)
	}
	if d.off != 0 {
		t.Fatalf("expected off=0 after reset, got %d", d.off)
	}
}
