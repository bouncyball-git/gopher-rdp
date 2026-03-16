package rdpdr

import (
	"encoding/binary"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestPrinterDeviceInterface(t *testing.T) {
	dev := NewPrinterDevice(10, "TestPrinter", "MS Publisher Imagesetter", t.TempDir(), "", false, slog.New(slog.DiscardHandler))
	var d Device = dev // verify interface compliance

	if d.ID() != 10 {
		t.Errorf("ID() = %d, want 10", d.ID())
	}
	if d.Type() != DeviceTypePrinter {
		t.Errorf("Type() = 0x%08X, want 0x%08X", d.Type(), DeviceTypePrinter)
	}
	if d.Name() != "TestPrinter" {
		t.Errorf("Name() = %q, want TestPrinter", d.Name())
	}
}

func TestPrinterDeviceData(t *testing.T) {
	tests := []struct {
		name       string
		printerName string
		driverName  string
		isDefault   bool
		wantFlags   uint32
	}{
		{
			name:        "non-default",
			printerName: "Test",
			driverName:  "MyDriver",
			isDefault:   false,
			wantFlags:   0x00000000, // no flags
		},
		{
			name:        "default",
			printerName: "Test",
			driverName:  "MyDriver",
			isDefault:   true,
			wantFlags:   0x00000002, // DEFAULTPRINTER
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dev := NewPrinterDevice(1, tt.printerName, tt.driverName, t.TempDir(), "", tt.isDefault, slog.New(slog.DiscardHandler))
			data := dev.DeviceData()

			if len(data) < 24 {
				t.Fatalf("DeviceData too short: %d bytes", len(data))
			}

			flags := binary.LittleEndian.Uint32(data[0:4])
			if flags != tt.wantFlags {
				t.Errorf("flags = 0x%08X, want 0x%08X", flags, tt.wantFlags)
			}

			codePage := binary.LittleEndian.Uint32(data[4:8])
			if codePage != 0 {
				t.Errorf("codePage = %d, want 0", codePage)
			}

			pnpNameLen := binary.LittleEndian.Uint32(data[8:12])
			if pnpNameLen != 0 {
				t.Errorf("pnpNameLen = %d, want 0", pnpNameLen)
			}

			driverNameLen := binary.LittleEndian.Uint32(data[12:16])
			printerNameLen := binary.LittleEndian.Uint32(data[16:20])
			cachedFieldsLen := binary.LittleEndian.Uint32(data[20:24])
			if cachedFieldsLen != 0 {
				t.Errorf("cachedFieldsLen = %d, want 0", cachedFieldsLen)
			}

			expectedLen := 24 + int(driverNameLen) + int(printerNameLen)
			if len(data) != expectedLen {
				t.Errorf("total length = %d, want %d", len(data), expectedLen)
			}

			// Verify driver name is null-terminated UTF-16LE
			driverBytes := data[24 : 24+driverNameLen]
			if driverBytes[len(driverBytes)-1] != 0 || driverBytes[len(driverBytes)-2] != 0 {
				t.Error("driver name not null-terminated")
			}

			got := decodeUTF16LE(driverBytes)
			if got != tt.driverName {
				t.Errorf("driver name = %q, want %q", got, tt.driverName)
			}

			// Verify printer name
			printerBytes := data[24+driverNameLen:]
			gotPrinter := decodeUTF16LE(printerBytes)
			if gotPrinter != tt.printerName {
				t.Errorf("printer name = %q, want %q", gotPrinter, tt.printerName)
			}
		})
	}
}

func TestPrinterDefaultDriver(t *testing.T) {
	dev := NewPrinterDevice(1, "Test", "", t.TempDir(), "", false, slog.New(slog.DiscardHandler))
	if dev.driverName != "MS Publisher Imagesetter" {
		t.Errorf("default driver = %q, want %q", dev.driverName, "MS Publisher Imagesetter")
	}
}

func TestPrinterCreateWriteClose(t *testing.T) {
	outDir := t.TempDir()
	var sent [][]byte
	sendFn := func(data []byte) error {
		cp := make([]byte, len(data))
		copy(cp, data)
		sent = append(sent, cp)
		return nil
	}

	h := NewHandler(sendFn, slog.New(slog.DiscardHandler), "PC")
	dev := NewPrinterDevice(1, "TestPrn", "Driver", outDir, "", false, slog.New(slog.DiscardHandler))

	// CREATE
	dev.HandleIRP(h, &IORequest{
		DeviceID:     1,
		CompletionID: 100,
		MajorFn:      IrpCreate,
	})

	if len(sent) != 1 {
		t.Fatalf("expected 1 response after CREATE, got %d", len(sent))
	}
	status := binary.LittleEndian.Uint32(sent[0][12:16])
	if status != StatusSuccess {
		t.Fatalf("CREATE status = 0x%08X, want SUCCESS", status)
	}
	// Extract fileID from output data (after 16-byte header)
	fileID := binary.LittleEndian.Uint32(sent[0][16:20])

	// WRITE
	payload := make([]byte, 64)
	binary.LittleEndian.PutUint32(payload[0:4], 5) // length = 5
	copy(payload[32:], []byte("Hello"))

	sent = sent[:0]
	dev.HandleIRP(h, &IORequest{
		DeviceID:     1,
		FileID:       fileID,
		CompletionID: 101,
		MajorFn:      IrpWrite,
		Payload:      payload,
	})

	if len(sent) != 1 {
		t.Fatalf("expected 1 response after WRITE, got %d", len(sent))
	}
	status = binary.LittleEndian.Uint32(sent[0][12:16])
	if status != StatusSuccess {
		t.Fatalf("WRITE status = 0x%08X, want SUCCESS", status)
	}
	written := binary.LittleEndian.Uint32(sent[0][16:20])
	if written != 5 {
		t.Errorf("written = %d, want 5", written)
	}

	// CLOSE
	sent = sent[:0]
	dev.HandleIRP(h, &IORequest{
		DeviceID:     1,
		FileID:       fileID,
		CompletionID: 102,
		MajorFn:      IrpClose,
	})

	if len(sent) != 1 {
		t.Fatalf("expected 1 response after CLOSE, got %d", len(sent))
	}
	status = binary.LittleEndian.Uint32(sent[0][12:16])
	if status != StatusSuccess {
		t.Fatalf("CLOSE status = 0x%08X, want SUCCESS", status)
	}

	// Verify file was written
	matches, err := filepath.Glob(filepath.Join(outDir, "TestPrn_*.prn"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 .prn file, got %d", len(matches))
	}

	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "Hello" {
		t.Errorf("file content = %q, want %q", string(data), "Hello")
	}
}

func TestPrinterUnsupportedIRP(t *testing.T) {
	var sent [][]byte
	sendFn := func(data []byte) error {
		cp := make([]byte, len(data))
		copy(cp, data)
		sent = append(sent, cp)
		return nil
	}

	h := NewHandler(sendFn, slog.New(slog.DiscardHandler), "PC")
	dev := NewPrinterDevice(1, "Test", "Driver", t.TempDir(), "", false, slog.New(slog.DiscardHandler))

	dev.HandleIRP(h, &IORequest{
		DeviceID:     1,
		CompletionID: 1,
		MajorFn:      IrpRead,
	})

	if len(sent) != 1 {
		t.Fatalf("expected 1 response, got %d", len(sent))
	}
	status := binary.LittleEndian.Uint32(sent[0][12:16])
	if status != StatusNotSupported {
		t.Errorf("status = 0x%08X, want STATUS_NOT_SUPPORTED (0x%08X)", status, StatusNotSupported)
	}
}

func TestPrinterWriteInvalidFileID(t *testing.T) {
	var sent [][]byte
	sendFn := func(data []byte) error {
		cp := make([]byte, len(data))
		copy(cp, data)
		sent = append(sent, cp)
		return nil
	}

	h := NewHandler(sendFn, slog.New(slog.DiscardHandler), "PC")
	dev := NewPrinterDevice(1, "Test", "Driver", t.TempDir(), "", false, slog.New(slog.DiscardHandler))

	payload := make([]byte, 64)
	binary.LittleEndian.PutUint32(payload[0:4], 3)
	copy(payload[32:], []byte("abc"))

	dev.HandleIRP(h, &IORequest{
		DeviceID:     1,
		FileID:       999, // non-existent
		CompletionID: 1,
		MajorFn:      IrpWrite,
		Payload:      payload,
	})

	if len(sent) != 1 {
		t.Fatalf("expected 1 response, got %d", len(sent))
	}
	status := binary.LittleEndian.Uint32(sent[0][12:16])
	if status != StatusInvalidDeviceRequest {
		t.Errorf("status = 0x%08X, want STATUS_INVALID_DEVICE_REQUEST (0x%08X)", status, StatusInvalidDeviceRequest)
	}
}

// runPrintJob drives CREATE → WRITE → CLOSE on a PrinterDevice and returns all sent PDUs.
func runPrintJob(t *testing.T, dev *PrinterDevice, docData []byte) [][]byte {
	t.Helper()
	var sent [][]byte
	sendFn := func(data []byte) error {
		cp := make([]byte, len(data))
		copy(cp, data)
		sent = append(sent, cp)
		return nil
	}
	h := NewHandler(sendFn, slog.New(slog.DiscardHandler), "PC")

	// CREATE
	dev.HandleIRP(h, &IORequest{DeviceID: 1, CompletionID: 100, MajorFn: IrpCreate})
	if len(sent) != 1 {
		t.Fatalf("expected 1 response after CREATE, got %d", len(sent))
	}
	fileID := binary.LittleEndian.Uint32(sent[0][16:20])

	// WRITE
	payload := make([]byte, 32+len(docData))
	binary.LittleEndian.PutUint32(payload[0:4], uint32(len(docData)))
	copy(payload[32:], docData)
	sent = sent[:0]
	dev.HandleIRP(h, &IORequest{DeviceID: 1, FileID: fileID, CompletionID: 101, MajorFn: IrpWrite, Payload: payload})

	// CLOSE
	sent = sent[:0]
	dev.HandleIRP(h, &IORequest{DeviceID: 1, FileID: fileID, CompletionID: 102, MajorFn: IrpClose})
	return sent
}

func TestPrinterIPPSubmit(t *testing.T) {
	var gotBody []byte
	var gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		var err error
		gotBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	dev := NewPrinterDevice(1, "TestPrn", "Driver", "", srv.URL, false, slog.New(slog.DiscardHandler))
	docData := []byte("PostScript data here")
	runPrintJob(t, dev, docData)

	if gotContentType != "application/ipp" {
		t.Errorf("Content-Type = %q, want application/ipp", gotContentType)
	}
	if len(gotBody) == 0 {
		t.Fatal("IPP server received no body")
	}

	// Verify IPP header: version 2.0, operation Print-Job (0x0002)
	if gotBody[0] != 0x02 || gotBody[1] != 0x00 {
		t.Errorf("IPP version = %d.%d, want 2.0", gotBody[0], gotBody[1])
	}
	opID := binary.BigEndian.Uint16(gotBody[2:4])
	if opID != 0x0002 {
		t.Errorf("IPP operation = 0x%04X, want 0x0002 (Print-Job)", opID)
	}

	// Find end-of-attributes tag (0x03) and verify document data follows
	eoa := -1
	for i := 8; i < len(gotBody); i++ {
		if gotBody[i] == 0x03 {
			eoa = i
			break
		}
	}
	if eoa < 0 {
		t.Fatal("end-of-attributes tag (0x03) not found")
	}
	got := gotBody[eoa+1:]
	if string(got) != string(docData) {
		t.Errorf("document data = %q, want %q", got, docData)
	}
}

func TestPrinterIPPAndFile(t *testing.T) {
	outDir := t.TempDir()
	var ippReceived bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ippReceived = true
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	dev := NewPrinterDevice(1, "TestPrn", "Driver", outDir, srv.URL, false, slog.New(slog.DiscardHandler))
	runPrintJob(t, dev, []byte("dual output"))

	// Verify file was written
	matches, err := filepath.Glob(filepath.Join(outDir, "TestPrn_*.prn"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 .prn file, got %d", len(matches))
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "dual output" {
		t.Errorf("file content = %q, want %q", data, "dual output")
	}

	// Verify IPP was also called
	if !ippReceived {
		t.Error("IPP server was not called")
	}
}

func TestPrinterIPPOnly(t *testing.T) {
	outDir := t.TempDir() // we'll verify nothing is written here
	var ippReceived bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ippReceived = true
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	// outputDir is empty — IPP only
	dev := NewPrinterDevice(1, "TestPrn", "Driver", "", srv.URL, false, slog.New(slog.DiscardHandler))
	runPrintJob(t, dev, []byte("ipp only"))

	// No file should be created in outDir
	matches, err := filepath.Glob(filepath.Join(outDir, "*.prn"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Errorf("expected 0 .prn files, got %d", len(matches))
	}

	if !ippReceived {
		t.Error("IPP server was not called")
	}
}
