package rdpdr

import (
	"encoding/binary"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFiletimeRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		t    time.Time
	}{
		{"unix epoch", time.Unix(0, 0).UTC()},
		{"2024-01-15", time.Date(2024, 1, 15, 12, 30, 0, 0, time.UTC)},
		{"2000-01-01", time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"zero", time.Time{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ft := timeToFiletime(tc.t)
			got := filetimeToTime(ft)

			if tc.t.IsZero() {
				if !got.IsZero() {
					t.Errorf("expected zero time, got %v", got)
				}
				return
			}

			// Truncate to 100ns precision for comparison
			want := tc.t.Truncate(100 * time.Nanosecond)
			got = got.Truncate(100 * time.Nanosecond)
			if !got.Equal(want) {
				t.Errorf("round-trip mismatch: want %v, got %v", want, got)
			}
		})
	}
}

func TestPathTraversal(t *testing.T) {
	root := t.TempDir()
	dev := NewDiskDevice(1, "test", root, false, slog.New(slog.DiscardHandler))

	tests := []struct {
		name    string
		path    string
		wantOK  bool
	}{
		{"simple file", "file.txt", true},
		{"subdir", "subdir\\file.txt", true},
		{"root path", "", true},
		{"dotdot escape", "..\\etc\\passwd", false},
		{"dotdot in middle", "subdir\\..\\..\\etc\\passwd", false},
		{"double dotdot", "..\\..", false},
		{"forward slash escape", "../etc/passwd", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path, ok := dev.resolvePath(tc.path)
			if ok != tc.wantOK {
				t.Errorf("resolvePath(%q) ok=%v, want ok=%v (path=%q)", tc.path, ok, tc.wantOK, path)
			}
		})
	}
}

func TestSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	// Create a symlink inside root that points outside
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skip("symlinks not supported:", err)
	}

	dev := NewDiskDevice(1, "test", root, false, slog.New(slog.DiscardHandler))

	// Accessing through the symlink should be rejected
	_, ok := dev.resolvePath("escape\\secret.txt")
	if ok {
		t.Error("resolvePath should reject symlink escaping root")
	}

	// Direct symlink dir access should also be rejected
	_, ok = dev.resolvePath("escape")
	if ok {
		t.Error("resolvePath should reject symlink directory escaping root")
	}
}

func TestSymlinkWithinRoot(t *testing.T) {
	root := t.TempDir()

	// Create a subdirectory and a symlink to it (stays within root)
	subdir := filepath.Join(root, "real")
	os.Mkdir(subdir, 0755)
	link := filepath.Join(root, "link")
	if err := os.Symlink(subdir, link); err != nil {
		t.Skip("symlinks not supported:", err)
	}

	dev := NewDiskDevice(1, "test", root, false, slog.New(slog.DiscardHandler))

	// Symlink within root should be allowed
	_, ok := dev.resolvePath("link")
	if !ok {
		t.Error("resolvePath should allow symlink staying within root")
	}
}

func TestFileInfoToAttributes(t *testing.T) {
	root := t.TempDir()

	// Create test files
	os.Mkdir(filepath.Join(root, "dir"), 0755)
	os.WriteFile(filepath.Join(root, "normal.txt"), []byte("hi"), 0644)
	os.WriteFile(filepath.Join(root, "readonly.txt"), []byte("hi"), 0444)
	os.WriteFile(filepath.Join(root, ".hidden"), []byte("hi"), 0644)

	tests := []struct {
		name     string
		path     string
		wantAttr uint32
	}{
		{"directory", filepath.Join(root, "dir"), FileAttrDirectory},
		{"normal file", filepath.Join(root, "normal.txt"), FileAttrNormal | FileAttrArchive},
		{"read-only file", filepath.Join(root, "readonly.txt"), FileAttrReadOnly | FileAttrArchive},
		{"hidden dotfile", filepath.Join(root, ".hidden"), FileAttrHidden | FileAttrArchive},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fi, err := os.Stat(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			got := fileInfoToAttributes(fi)

			// Check that expected flags are present
			if tc.wantAttr&FileAttrDirectory != 0 && got&FileAttrDirectory == 0 {
				t.Errorf("expected FileAttrDirectory, got 0x%08X", got)
			}
			if tc.wantAttr&FileAttrReadOnly != 0 && got&FileAttrReadOnly == 0 {
				t.Errorf("expected FileAttrReadOnly, got 0x%08X", got)
			}
			if tc.wantAttr&FileAttrHidden != 0 && got&FileAttrHidden == 0 {
				t.Errorf("expected FileAttrHidden, got 0x%08X", got)
			}
		})
	}
}

func TestSharedHeaderEncoding(t *testing.T) {
	buf := EncodeSharedHeader(ComponentRDPDR, PakIDClientIDConfirm)
	if len(buf) != 4 {
		t.Fatalf("expected 4 bytes, got %d", len(buf))
	}
	component := binary.LittleEndian.Uint16(buf[0:2])
	packetID := binary.LittleEndian.Uint16(buf[2:4])
	if component != ComponentRDPDR {
		t.Errorf("component = 0x%04X, want 0x%04X", component, ComponentRDPDR)
	}
	if packetID != PakIDClientIDConfirm {
		t.Errorf("packetID = 0x%04X, want 0x%04X", packetID, PakIDClientIDConfirm)
	}
}

func TestInitSequence(t *testing.T) {
	var sent [][]byte
	sendFn := func(data []byte) error {
		cp := make([]byte, len(data))
		copy(cp, data)
		sent = append(sent, cp)
		return nil
	}

	h := NewHandler(sendFn, slog.New(slog.DiscardHandler), "TESTPC")
	h.AddDrive(1, "share", t.TempDir(), false)

	// Step 1: Server Announce Request → Client Announce Reply + Client Name
	announce := make([]byte, 12)
	binary.LittleEndian.PutUint16(announce[0:2], ComponentRDPDR)
	binary.LittleEndian.PutUint16(announce[2:4], PakIDServerAnnounce)
	binary.LittleEndian.PutUint16(announce[4:6], 1)      // versionMajor
	binary.LittleEndian.PutUint16(announce[6:8], 13)     // versionMinor
	binary.LittleEndian.PutUint32(announce[8:12], 0x1234) // clientID
	h.ProcessPDU(announce)

	if len(sent) < 2 {
		t.Fatalf("expected at least 2 sent PDUs after Server Announce, got %d", len(sent))
	}

	// Verify Client Announce Reply (uses PakIDClientIDConfirm = 0x4343)
	reply := sent[0]
	if binary.LittleEndian.Uint16(reply[0:2]) != ComponentRDPDR {
		t.Error("reply component mismatch")
	}
	if binary.LittleEndian.Uint16(reply[2:4]) != PakIDClientIDConfirm {
		t.Errorf("reply packetID = 0x%04X, want 0x%04X", binary.LittleEndian.Uint16(reply[2:4]), PakIDClientIDConfirm)
	}
	if binary.LittleEndian.Uint16(reply[4:6]) != 1 || binary.LittleEndian.Uint16(reply[6:8]) != 0x000C {
		t.Error("reply version mismatch")
	}
	if binary.LittleEndian.Uint32(reply[8:12]) != 0x1234 {
		t.Error("reply clientID mismatch")
	}

	// Verify Client Name PDU (16 bytes header + name)
	namePDU := sent[1]
	if binary.LittleEndian.Uint16(namePDU[2:4]) != PakIDClientNameReq {
		t.Error("name PDU packetID mismatch")
	}
	if binary.LittleEndian.Uint32(namePDU[4:8]) != 1 {
		t.Error("name PDU unicode flag should be 1")
	}
	// Verify name length field at offset 12
	nameLen := binary.LittleEndian.Uint32(namePDU[12:16])
	if int(nameLen)+16 != len(namePDU) {
		t.Errorf("name PDU length mismatch: nameLen=%d, pduLen=%d", nameLen, len(namePDU))
	}

	sent = sent[:0]

	// Step 2: Server Core Capability Request with General cap VERSION_02
	// SharedHeader(4) + numCaps(2) + pad(2) + GeneralCap(44) = 52
	coreCap := make([]byte, 52)
	binary.LittleEndian.PutUint16(coreCap[0:2], ComponentRDPDR)
	binary.LittleEndian.PutUint16(coreCap[2:4], PakIDServerCoreCap)
	binary.LittleEndian.PutUint16(coreCap[4:6], 1) // numCaps = 1
	// General capability at offset 8
	binary.LittleEndian.PutUint16(coreCap[8:10], CapGeneralType)
	binary.LittleEndian.PutUint16(coreCap[10:12], 44) // length
	binary.LittleEndian.PutUint32(coreCap[12:16], GeneralCapVersion2)
	h.ProcessPDU(coreCap)

	if len(sent) != 0 {
		t.Fatalf("expected 0 sent PDUs after Core Cap alone, got %d", len(sent))
	}

	// Step 3: Server Client ID Confirm → triggers Client Cap Response + empty Device List
	confirm := make([]byte, 12)
	binary.LittleEndian.PutUint16(confirm[0:2], ComponentRDPDR)
	binary.LittleEndian.PutUint16(confirm[2:4], PakIDClientIDConfirm)
	binary.LittleEndian.PutUint16(confirm[4:6], 1)
	binary.LittleEndian.PutUint16(confirm[6:8], 13)
	binary.LittleEndian.PutUint32(confirm[8:12], 0x1234)
	h.ProcessPDU(confirm)

	if len(sent) != 2 {
		t.Fatalf("expected 2 sent PDUs (cap response + empty device list) after ID Confirm, got %d", len(sent))
	}
	if binary.LittleEndian.Uint16(sent[0][2:4]) != PakIDClientCoreCap {
		t.Errorf("cap response packetID = 0x%04X, want 0x%04X", binary.LittleEndian.Uint16(sent[0][2:4]), PakIDClientCoreCap)
	}
	// Empty device list (drives withheld until User Logged On)
	emptyList := sent[1]
	if binary.LittleEndian.Uint16(emptyList[2:4]) != PakIDDeviceListAnnounce {
		t.Error("device list packetID mismatch")
	}
	if binary.LittleEndian.Uint32(emptyList[4:8]) != 0 {
		t.Errorf("expected 0 devices pre-logon, got %d", binary.LittleEndian.Uint32(emptyList[4:8]))
	}

	sent = sent[:0]

	// Step 4: Server User Logged On → announces drives
	loggedOn := make([]byte, 4)
	binary.LittleEndian.PutUint16(loggedOn[0:2], ComponentRDPDR)
	binary.LittleEndian.PutUint16(loggedOn[2:4], PakIDUserLoggedOn)
	h.ProcessPDU(loggedOn)

	if len(sent) != 1 {
		t.Fatalf("expected 1 sent PDU (device list) after User Logged On, got %d", len(sent))
	}
	devList := sent[0]
	if binary.LittleEndian.Uint16(devList[2:4]) != PakIDDeviceListAnnounce {
		t.Error("device list packetID mismatch")
	}
	numDevices := binary.LittleEndian.Uint32(devList[4:8])
	if numDevices != 1 {
		t.Errorf("expected 1 device after logon, got %d", numDevices)
	}
}

func TestDeviceListWireFormat(t *testing.T) {
	var sent [][]byte
	sendFn := func(data []byte) error {
		cp := make([]byte, len(data))
		copy(cp, data)
		sent = append(sent, cp)
		return nil
	}

	h := NewHandler(sendFn, slog.New(slog.DiscardHandler), "PC")
	h.serverCapGenVer = GeneralCapVersion2 // simulate server announcing VERSION_02
	h.AddDrive(42, "MYSHARE", t.TempDir(), false)

	// Trigger device list with drives included
	h.sendDeviceListFiltered(true)

	if len(sent) != 1 {
		t.Fatalf("expected 1 PDU, got %d", len(sent))
	}

	pdu := sent[0]
	// "MYSHARE" = 7 ASCII chars + null = 8 bytes DeviceData (MS-RDPEFS 2.2.1.3)
	// header(4) + deviceCount(4) + deviceEntry(20 + 8) = 36
	if len(pdu) != 36 {
		t.Fatalf("expected 36 bytes, got %d", len(pdu))
	}

	// Device type should be disk
	devType := binary.LittleEndian.Uint32(pdu[8:12])
	if devType != DeviceTypeDisk {
		t.Errorf("device type = 0x%08X, want 0x%08X", devType, DeviceTypeDisk)
	}

	// Device ID
	devID := binary.LittleEndian.Uint32(pdu[12:16])
	if devID != 42 {
		t.Errorf("device ID = %d, want 42", devID)
	}

	// Preferred DOS name (8 bytes, ASCII)
	dosName := string(pdu[16:23])
	if dosName != "MYSHARE" {
		t.Errorf("DOS name = %q, want MYSHARE", dosName)
	}

	// DeviceDataLength should be 8 (7 ASCII chars + null terminator)
	devDataLen := binary.LittleEndian.Uint32(pdu[24:28])
	if devDataLen != 8 {
		t.Errorf("deviceDataLength = %d, want 8", devDataLen)
	}

	// DeviceData should be null-terminated ASCII "MYSHARE"
	devDataName := string(pdu[28:35])
	if devDataName != "MYSHARE" {
		t.Errorf("deviceData name = %q, want MYSHARE", devDataName)
	}
}

func TestReadOnlyRejectsWrite(t *testing.T) {
	var sent [][]byte
	sendFn := func(data []byte) error {
		cp := make([]byte, len(data))
		copy(cp, data)
		sent = append(sent, cp)
		return nil
	}

	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "test.txt"), []byte("hello"), 0644)

	h := NewHandler(sendFn, slog.New(slog.DiscardHandler), "PC")
	h.AddDrive(1, "share", root, true) // readOnly = true

	dev := h.devices[1]

	// First, open the file
	createPayload := make([]byte, 64)
	binary.LittleEndian.PutUint32(createPayload[20:24], FileOpen) // disposition
	pathUTF16 := encodeUTF16LE("test.txt")
	binary.LittleEndian.PutUint32(createPayload[28:32], uint32(len(pathUTF16)))
	createPayload = append(createPayload[:32], pathUTF16...)

	sent = sent[:0]
	dev.HandleIRP(h, &IORequest{
		DeviceID:     1,
		FileID:       0,
		CompletionID: 1,
		MajorFn:      IrpCreate,
		Payload:      createPayload,
	})

	if len(sent) == 0 {
		t.Fatal("no response from create")
	}
	createResp := sent[len(sent)-1]
	createStatus := binary.LittleEndian.Uint32(createResp[12:16])
	if createStatus != StatusSuccess {
		t.Fatalf("create failed with status 0x%08X", createStatus)
	}
	fileID := binary.LittleEndian.Uint32(createResp[16:20])

	// Now try to write
	writePayload := make([]byte, 36)
	binary.LittleEndian.PutUint32(writePayload[0:4], 5) // length
	// offset = 0
	writePayload = append(writePayload, []byte("world")...)

	sent = sent[:0]
	dev.HandleIRP(h, &IORequest{
		DeviceID:     1,
		FileID:       fileID,
		CompletionID: 2,
		MajorFn:      IrpWrite,
		Payload:      writePayload,
	})

	if len(sent) == 0 {
		t.Fatal("no response from write")
	}
	writeResp := sent[0]
	writeStatus := binary.LittleEndian.Uint32(writeResp[12:16])
	if writeStatus != StatusAccessDenied {
		t.Errorf("write status = 0x%08X, want STATUS_ACCESS_DENIED (0x%08X)", writeStatus, StatusAccessDenied)
	}
}

func TestUTF16Roundtrip(t *testing.T) {
	tests := []string{
		"hello",
		"test.txt",
		"",
		"日本語",
	}

	for _, s := range tests {
		encoded := encodeUTF16LE(s)
		decoded := decodeUTF16LE(encoded)
		if decoded != s {
			t.Errorf("UTF16 round-trip failed: %q → %q", s, decoded)
		}
	}
}
