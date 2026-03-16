package main

import (
	rdp "gopher-rdp"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseConfigFile(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name:    "basic key-value",
			content: "host 10.0.0.1\nport 3389\nuser admin\n",
			want:    []string{"-host", "10.0.0.1", "-port", "3389", "-user", "admin"},
		},
		{
			name:    "full-line comments and blanks",
			content: "# comment\nhost 10.0.0.1\n\n# another\nuser admin\n",
			want:    []string{"-host", "10.0.0.1", "-user", "admin"},
		},
		{
			name:    "inline comments",
			content: "host 10.0.0.1 # work server\nuser admin\t# main account\n",
			want:    []string{"-host", "10.0.0.1", "-user", "admin"},
		},
		{
			name:    "bool-style flags with value",
			content: "gui 1920x1080\naudio-out stereo,hirate,15ms\nlog RDPDR,EGFX\n",
			want:    []string{"-gui=1920x1080", "-audio-out=stereo,hirate,15ms", "-log=RDPDR,EGFX"},
		},
		{
			name:    "bool-style flags bare",
			content: "gui\naudio-out\nlog\n",
			want:    []string{"-gui", "-audio-out", "-log"},
		},
		{
			name:    "toggle true",
			content: "theming true\nfont-smoothing true\nwallpaper\n",
			want:    []string{"-theming", "-font-smoothing", "-wallpaper"},
		},
		{
			name:    "toggle false",
			content: "theming false\nwallpaper false\n",
			want:    nil,
		},
		{
			name:    "bare-bool flags",
			content: "admin\nclipboard-off\ngfx-off false\n",
			want:    []string{"-admin", "-clipboard-off"},
		},
		{
			name:    "repeatable flags",
			content: "drive share:/home/user/dir\ndrive tmp:/tmp:ro\nserial COM3:/dev/ttyUSB0\n",
			want:    []string{"-drive", "share:/home/user/dir", "-drive", "tmp:/tmp:ro", "-serial", "COM3:/dev/ttyUSB0"},
		},
		{
			name:    "tab separated",
			content: "host\t10.0.0.1\nuser\tadmin\n",
			want:    []string{"-host", "10.0.0.1", "-user", "admin"},
		},
		{
			name:    "empty file",
			content: "",
			want:    nil,
		},
		{
			name:    "only comments",
			content: "# comment 1\n# comment 2\n",
			want:    nil,
		},
		{
			name:    "log-file with value",
			content: "log-file session.log\n",
			want:    []string{"-log-file=session.log"},
		},
		{
			name:    "log-file bare",
			content: "log-file\n",
			want:    []string{"-log-file"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "test.conf")
			if err := os.WriteFile(path, []byte(tt.content), 0644); err != nil {
				t.Fatal(err)
			}
			got, err := parseConfigFile(path)
			if err != nil {
				t.Fatalf("parseConfigFile error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got  %v\nwant %v", got, tt.want)
			}
		})
	}
}

func TestParseConfigFileMissing(t *testing.T) {
	_, err := parseConfigFile("/nonexistent/path.conf")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestPrinterFlag(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    rdp.PrinterRedirect
		wantErr bool
	}{
		{
			name:  "dir only",
			input: "MyPrn:/tmp/print",
			want:  rdp.PrinterRedirect{Name: "MyPrn", OutputDir: "/tmp/print"},
		},
		{
			name:  "dir with default",
			input: "MyPrn:/tmp/print:default",
			want:  rdp.PrinterRedirect{Name: "MyPrn", OutputDir: "/tmp/print", IsDefault: true},
		},
		{
			name:  "dir with driver and default",
			input: "MyPrn:/tmp/print:driver=HP LaserJet:default",
			want:  rdp.PrinterRedirect{Name: "MyPrn", OutputDir: "/tmp/print", DriverName: "HP LaserJet", IsDefault: true},
		},
		{
			name:  "ipp only",
			input: "HPNet:ipp=http://cups:631/printers/hp",
			want:  rdp.PrinterRedirect{Name: "HPNet", IPPURL: "http://cups:631/printers/hp"},
		},
		{
			name:  "ipp only with default",
			input: "HPNet:ipp=http://cups:631/printers/hp:default",
			want:  rdp.PrinterRedirect{Name: "HPNet", IPPURL: "http://cups:631/printers/hp", IsDefault: true},
		},
		{
			name:  "ipp only with driver",
			input: "HPNet:ipp=http://cups:631/printers/hp:driver=MyDriver",
			want:  rdp.PrinterRedirect{Name: "HPNet", IPPURL: "http://cups:631/printers/hp", DriverName: "MyDriver"},
		},
		{
			name:  "dir and ipp",
			input: "MyPrn:/tmp/print:ipp=http://cups:631/printers/hp",
			want:  rdp.PrinterRedirect{Name: "MyPrn", OutputDir: "/tmp/print", IPPURL: "http://cups:631/printers/hp"},
		},
		{
			name:  "dir and ipp with default",
			input: "MyPrn:/tmp/print:ipp=http://cups:631/printers/hp:default",
			want:  rdp.PrinterRedirect{Name: "MyPrn", OutputDir: "/tmp/print", IPPURL: "http://cups:631/printers/hp", IsDefault: true},
		},
		{
			name:  "dir with driver before ipp",
			input: "MyPrn:/tmp/print:driver=HP:ipp=http://cups:631/printers/hp:default",
			want:  rdp.PrinterRedirect{Name: "MyPrn", OutputDir: "/tmp/print", DriverName: "HP", IPPURL: "http://cups:631/printers/hp", IsDefault: true},
		},
		{
			name:    "empty",
			input:   "",
			wantErr: true,
		},
		{
			name:    "no dir no ipp",
			input:   "MyPrn:",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var pf printerFlag
			err := pf.Set(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(pf) != 1 {
				t.Fatalf("got %d entries, want 1", len(pf))
			}
			got := rdp.PrinterRedirect(pf[0])
			if got != tt.want {
				t.Errorf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
}
