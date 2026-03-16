package ber

import (
	"bytes"
	"testing"
)

func TestWriteTagShort(t *testing.T) {
	tests := []struct {
		name        string
		class       byte
		constructed byte
		tag         int
		want        []byte
	}{
		{"Universal Integer", ClassUniversal, Primitive, TagInteger, []byte{0x02}},
		{"Universal OctetString", ClassUniversal, Primitive, TagOctetString, []byte{0x04}},
		{"Universal Sequence", ClassUniversal, Constructed, TagSequence, []byte{0x30}},
		{"Context 0 Primitive", ClassContext, Primitive, 0, []byte{0x80}},
		{"Context 1 Constructed", ClassContext, Constructed, 1, []byte{0xA1}},
		{"Application 3 Constructed", ClassApplication, Constructed, 3, []byte{0x63}},
		{"Boolean", ClassUniversal, Primitive, TagBoolean, []byte{0x01}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteTag(&buf, tt.class, tt.constructed, tt.tag); err != nil {
				t.Fatalf("WriteTag error: %v", err)
			}
			if !bytes.Equal(buf.Bytes(), tt.want) {
				t.Errorf("got %X, want %X", buf.Bytes(), tt.want)
			}
		})
	}
}

func TestWriteTagLong(t *testing.T) {
	tests := []struct {
		name string
		tag  int
		want []byte
	}{
		{"Tag 31", 31, []byte{0x7F, 0x1F}},          // Application Constructed, long form
		{"Tag 101", 101, []byte{0x7F, 0x65}},         // Application 101 (MCS Connect Initial)
		{"Tag 102", 102, []byte{0x7F, 0x66}},         // Application 102 (MCS Connect Response)
		{"Tag 128", 128, []byte{0x7F, 0x81, 0x00}},   // Needs 2 base-128 bytes
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteTag(&buf, ClassApplication, Constructed, tt.tag); err != nil {
				t.Fatalf("WriteTag error: %v", err)
			}
			if !bytes.Equal(buf.Bytes(), tt.want) {
				t.Errorf("got %X, want %X", buf.Bytes(), tt.want)
			}
		})
	}
}

func TestWriteLength(t *testing.T) {
	tests := []struct {
		name   string
		length int
		want   []byte
	}{
		{"Zero", 0, []byte{0x00}},
		{"Short 1", 1, []byte{0x01}},
		{"Short 127", 127, []byte{0x7F}},
		{"Long 128", 128, []byte{0x81, 0x80}},
		{"Long 255", 255, []byte{0x81, 0xFF}},
		{"Long 256", 256, []byte{0x82, 0x01, 0x00}},
		{"Long 65535", 65535, []byte{0x82, 0xFF, 0xFF}},
		{"Long 65536", 65536, []byte{0x83, 0x01, 0x00, 0x00}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteLength(&buf, tt.length); err != nil {
				t.Fatalf("WriteLength error: %v", err)
			}
			if !bytes.Equal(buf.Bytes(), tt.want) {
				t.Errorf("got %X, want %X", buf.Bytes(), tt.want)
			}
		})
	}
}

func TestReadLength(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want int
	}{
		{"Zero", []byte{0x00}, 0},
		{"Short 1", []byte{0x01}, 1},
		{"Short 127", []byte{0x7F}, 127},
		{"Long 128", []byte{0x81, 0x80}, 128},
		{"Long 255", []byte{0x81, 0xFF}, 255},
		{"Long 256", []byte{0x82, 0x01, 0x00}, 256},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := bytes.NewReader(tt.data)
			got, err := ReadLength(r)
			if err != nil {
				t.Fatalf("ReadLength error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestWriteReadLengthRoundTrip(t *testing.T) {
	lengths := []int{0, 1, 50, 127, 128, 255, 256, 1000, 65535}
	for _, l := range lengths {
		var buf bytes.Buffer
		if err := WriteLength(&buf, l); err != nil {
			t.Fatalf("WriteLength(%d) error: %v", l, err)
		}
		got, err := ReadLength(bytes.NewReader(buf.Bytes()))
		if err != nil {
			t.Fatalf("ReadLength for %d error: %v", l, err)
		}
		if got != l {
			t.Errorf("round-trip %d: got %d", l, got)
		}
	}
}

func TestWriteReadIntegerRoundTrip(t *testing.T) {
	values := []int{0, 1, 127, 128, 255, 256, 65535, 100000, -1, -128, -129}
	for _, v := range values {
		var buf bytes.Buffer
		if err := WriteInteger(&buf, v); err != nil {
			t.Fatalf("WriteInteger(%d) error: %v", v, err)
		}
		got, err := ReadInteger(bytes.NewReader(buf.Bytes()))
		if err != nil {
			t.Fatalf("ReadInteger for %d error: %v", v, err)
		}
		if got != v {
			t.Errorf("round-trip %d: got %d", v, got)
		}
	}
}

func TestWriteIntegerEncoding(t *testing.T) {
	tests := []struct {
		name  string
		value int
		want  []byte
	}{
		{"Zero", 0, []byte{0x02, 0x01, 0x00}},
		{"One", 1, []byte{0x02, 0x01, 0x01}},
		{"127", 127, []byte{0x02, 0x01, 0x7F}},
		{"128", 128, []byte{0x02, 0x02, 0x00, 0x80}}, // needs leading zero
		{"256", 256, []byte{0x02, 0x02, 0x01, 0x00}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteInteger(&buf, tt.value); err != nil {
				t.Fatalf("WriteInteger error: %v", err)
			}
			if !bytes.Equal(buf.Bytes(), tt.want) {
				t.Errorf("got %X, want %X", buf.Bytes(), tt.want)
			}
		})
	}
}

func TestWriteOctetString(t *testing.T) {
	data := []byte{0x01, 0x02, 0x03}
	var buf bytes.Buffer
	if err := WriteOctetString(&buf, data); err != nil {
		t.Fatalf("WriteOctetString error: %v", err)
	}
	want := []byte{0x04, 0x03, 0x01, 0x02, 0x03}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("got %X, want %X", buf.Bytes(), want)
	}
}

func TestWriteOctetStringEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteOctetString(&buf, []byte{}); err != nil {
		t.Fatalf("WriteOctetString error: %v", err)
	}
	want := []byte{0x04, 0x00}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("got %X, want %X", buf.Bytes(), want)
	}
}

func TestWriteBoolean(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteBoolean(&buf, true); err != nil {
		t.Fatalf("WriteBoolean(true) error: %v", err)
	}
	want := []byte{0x01, 0x01, 0xFF}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("true: got %X, want %X", buf.Bytes(), want)
	}

	buf.Reset()
	if err := WriteBoolean(&buf, false); err != nil {
		t.Fatalf("WriteBoolean(false) error: %v", err)
	}
	want = []byte{0x01, 0x01, 0x00}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("false: got %X, want %X", buf.Bytes(), want)
	}
}

func TestReadTag(t *testing.T) {
	tests := []struct {
		name        string
		data        []byte
		wantClass   byte
		wantConstr  bool
		wantTag     int
	}{
		{"Integer", []byte{0x02}, ClassUniversal, false, TagInteger},
		{"Sequence", []byte{0x30}, ClassUniversal, true, TagSequence},
		{"Application 101", []byte{0x7F, 0x65}, ClassApplication, true, 101},
		{"Context 0", []byte{0x80}, ClassContext, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := bytes.NewReader(tt.data)
			class, constr, tag, err := ReadTag(r)
			if err != nil {
				t.Fatalf("ReadTag error: %v", err)
			}
			if class != tt.wantClass {
				t.Errorf("class: got 0x%02X, want 0x%02X", class, tt.wantClass)
			}
			if constr != tt.wantConstr {
				t.Errorf("constructed: got %v, want %v", constr, tt.wantConstr)
			}
			if tag != tt.wantTag {
				t.Errorf("tag: got %d, want %d", tag, tt.wantTag)
			}
		})
	}
}

func TestWriteReadTagRoundTrip(t *testing.T) {
	cases := []struct {
		class       byte
		constructed byte
		tag         int
	}{
		{ClassUniversal, Primitive, TagInteger},
		{ClassUniversal, Constructed, TagSequence},
		{ClassApplication, Constructed, 101},
		{ClassApplication, Constructed, 102},
		{ClassContext, Primitive, 0},
		{ClassContext, Constructed, 1},
		{ClassApplication, Constructed, 128},
	}
	for _, tc := range cases {
		var buf bytes.Buffer
		if err := WriteTag(&buf, tc.class, tc.constructed, tc.tag); err != nil {
			t.Fatalf("WriteTag error: %v", err)
		}
		class, constr, tag, err := ReadTag(bytes.NewReader(buf.Bytes()))
		if err != nil {
			t.Fatalf("ReadTag error: %v", err)
		}
		if class != tc.class || constr != (tc.constructed != 0) || tag != tc.tag {
			t.Errorf("round-trip mismatch for class=0x%02X constructed=0x%02X tag=%d: got class=0x%02X constructed=%v tag=%d",
				tc.class, tc.constructed, tc.tag, class, constr, tag)
		}
	}
}

func TestDomainParametersRoundTrip(t *testing.T) {
	p := DomainParameters{
		MaxChannelIDs:   34,
		MaxUserIDs:      2,
		MaxTokenIDs:     0,
		NumPriorities:   1,
		MinThroughput:   0,
		MaxHeight:       1,
		MaxMCSPDUSize:   65535,
		ProtocolVersion: 2,
	}

	var buf bytes.Buffer
	if err := WriteDomainParameters(&buf, p); err != nil {
		t.Fatalf("WriteDomainParameters error: %v", err)
	}

	got, err := ReadDomainParameters(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("ReadDomainParameters error: %v", err)
	}

	if got != p {
		t.Errorf("round-trip mismatch:\ngot:  %+v\nwant: %+v", got, p)
	}
}

func TestLengthSize(t *testing.T) {
	tests := []struct {
		length int
		want   int
	}{
		{0, 1},
		{127, 1},
		{128, 2},
		{255, 2},
		{256, 3},
		{65535, 3},
		{65536, 4},
	}
	for _, tt := range tests {
		got := LengthSize(tt.length)
		if got != tt.want {
			t.Errorf("LengthSize(%d) = %d, want %d", tt.length, got, tt.want)
		}
	}
}

func TestTagSize(t *testing.T) {
	tests := []struct {
		tag  int
		want int
	}{
		{0, 1},
		{30, 1},
		{31, 2},
		{101, 2},
		{127, 2},
		{128, 3},
	}
	for _, tt := range tests {
		got := TagSize(tt.tag)
		if got != tt.want {
			t.Errorf("TagSize(%d) = %d, want %d", tt.tag, got, tt.want)
		}
	}
}

func TestReadLengthErrors(t *testing.T) {
	// Empty reader
	_, err := ReadLength(bytes.NewReader([]byte{}))
	if err == nil {
		t.Error("expected error for empty reader")
	}

	// Long form with zero length bytes (invalid)
	_, err = ReadLength(bytes.NewReader([]byte{0x80}))
	if err == nil {
		t.Error("expected error for 0x80 (indefinite length)")
	}

	// Long form but truncated
	_, err = ReadLength(bytes.NewReader([]byte{0x82, 0x01}))
	if err == nil {
		t.Error("expected error for truncated long form")
	}
}

func TestReadIntegerErrors(t *testing.T) {
	// Wrong tag
	_, err := ReadInteger(bytes.NewReader([]byte{0x04, 0x01, 0x00}))
	if err == nil {
		t.Error("expected error for non-INTEGER tag")
	}

	// Truncated
	_, err = ReadInteger(bytes.NewReader([]byte{0x02, 0x02, 0x01}))
	if err == nil {
		t.Error("expected error for truncated integer value")
	}
}
