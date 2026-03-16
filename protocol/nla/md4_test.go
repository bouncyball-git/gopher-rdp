package nla

import (
	"encoding/hex"
	"testing"
)

func TestMD4RFC1320Vectors(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "31d6cfe0d16ae931b73c59d7e0c089c0"},
		{"a", "bde52cb31de33e46245e05fbdbd6fb24"},
		{"abc", "a448017aaf21d8525fc10ae87aa6729d"},
		{"message digest", "d9130a8164549fe818874806e1c7014b"},
		{"abcdefghijklmnopqrstuvwxyz", "d79e1c308aa5bbcdeea8ed63df412da9"},
		{"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789", "043f8582f241db351ce627e153e7f0e4"},
		{"12345678901234567890123456789012345678901234567890123456789012345678901234567890", "e33b4ddc9c38f2199c3e7b164fcc0536"},
	}

	for _, tt := range tests {
		got := md4Sum([]byte(tt.input))
		gotHex := hex.EncodeToString(got[:])
		if gotHex != tt.want {
			t.Errorf("md4(%q) = %s, want %s", tt.input, gotHex, tt.want)
		}
	}
}

func TestMD4NTHash(t *testing.T) {
	// NT hash = MD4(UTF16LE("Password"))
	// Known value from MS-NLMP spec and online NT hash calculators
	password := encodeUTF16LE("Password")
	got := md4Sum(password)
	want := "a4f49c406510bdcab6824ee7c30fd852"
	gotHex := hex.EncodeToString(got[:])
	if gotHex != want {
		t.Errorf("NT hash of 'Password' = %s, want %s", gotHex, want)
	}
}
