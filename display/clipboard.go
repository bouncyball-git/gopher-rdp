package display

import (
	"bytes"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

// clipMethod identifies which OS tool to use for clipboard access.
type clipMethod int

const (
	clipNone clipMethod = iota
	clipWlCopy
	clipXClip
	clipXSel
	clipPbCopy
	clipPowershell
)

var (
	detectedMethod clipMethod
	detectOnce     sync.Once
)

func detectClipboard() clipMethod {
	switch runtime.GOOS {
	case "darwin":
		return clipPbCopy
	case "windows":
		return clipPowershell
	case "linux", "freebsd", "openbsd", "netbsd":
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			if _, err := exec.LookPath("wl-copy"); err == nil {
				return clipWlCopy
			}
		}
		if _, err := exec.LookPath("xclip"); err == nil {
			return clipXClip
		}
		if _, err := exec.LookPath("xsel"); err == nil {
			return clipXSel
		}
		return clipNone
	}
	return clipNone
}

func getMethod() clipMethod {
	detectOnce.Do(func() {
		detectedMethod = detectClipboard()
	})
	return detectedMethod
}

// ReadClipboard reads text from the system clipboard.
func ReadClipboard() (string, error) {
	var cmd *exec.Cmd
	switch getMethod() {
	case clipWlCopy:
		cmd = exec.Command("wl-paste", "--no-newline")
	case clipXClip:
		cmd = exec.Command("xclip", "-selection", "clipboard", "-o")
	case clipXSel:
		cmd = exec.Command("xsel", "--clipboard", "--output")
	case clipPbCopy:
		cmd = exec.Command("pbpaste")
	case clipPowershell:
		cmd = exec.Command("powershell.exe", "-command", "Get-Clipboard")
	default:
		return "", nil
	}
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}

// WriteClipboard writes text to the system clipboard.
func WriteClipboard(text string) error {
	var cmd *exec.Cmd
	switch getMethod() {
	case clipWlCopy:
		cmd = exec.Command("wl-copy")
	case clipXClip:
		cmd = exec.Command("xclip", "-selection", "clipboard")
	case clipXSel:
		cmd = exec.Command("xsel", "--clipboard", "--input")
	case clipPbCopy:
		cmd = exec.Command("pbcopy")
	case clipPowershell:
		cmd = exec.Command("clip.exe")
	default:
		return nil
	}
	cmd.Stdin = bytes.NewReader([]byte(text))
	return cmd.Run()
}

// ReadClipboardImage reads a PNG image from the system clipboard.
// Returns nil, nil if no image is available or the platform is unsupported.
func ReadClipboardImage() ([]byte, error) {
	var cmd *exec.Cmd
	switch getMethod() {
	case clipWlCopy:
		cmd = exec.Command("wl-paste", "--type", "image/png", "--no-newline")
	case clipXClip:
		cmd = exec.Command("xclip", "-selection", "clipboard", "-t", "image/png", "-o")
	case clipPbCopy:
		cmd = exec.Command("osascript", "-e",
			`set pngData to (the clipboard as «class PNGf»)
set theFile to (open for access POSIX file "/dev/stdout" with write permission)
write pngData to theFile
close access theFile`)
	case clipPowershell:
		cmd = exec.Command("powershell.exe", "-command",
			`$img = Get-Clipboard -Format Image; if ($img) { $ms = New-Object System.IO.MemoryStream; $img.Save($ms, [System.Drawing.Imaging.ImageFormat]::Png); [System.Console]::OpenStandardOutput().Write($ms.ToArray(), 0, $ms.Length) }`)
	default:
		return nil, nil
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, nil // no image available
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// WriteClipboardImage writes a PNG image to the system clipboard.
func WriteClipboardImage(pngData []byte) error {
	var cmd *exec.Cmd
	switch getMethod() {
	case clipWlCopy:
		cmd = exec.Command("wl-copy", "--type", "image/png")
	case clipXClip:
		cmd = exec.Command("xclip", "-selection", "clipboard", "-t", "image/png")
	case clipPbCopy:
		// macOS: write PNG to temp file, then set clipboard via osascript.
		f, err := os.CreateTemp("", "gopher-rdp-clip-*.png")
		if err != nil {
			return err
		}
		tmpPath := f.Name()
		defer os.Remove(tmpPath)
		if _, err := f.Write(pngData); err != nil {
			f.Close()
			return err
		}
		f.Close()
		return exec.Command("osascript", "-e",
			`set the clipboard to (read (POSIX file "`+tmpPath+`") as «class PNGf»)`).Run()
	case clipPowershell:
		f, err := os.CreateTemp("", "gopher-rdp-clip-*.png")
		if err != nil {
			return err
		}
		tmpPath := f.Name()
		defer os.Remove(tmpPath)
		if _, err := f.Write(pngData); err != nil {
			f.Close()
			return err
		}
		f.Close()
		return exec.Command("powershell.exe", "-command",
			`Add-Type -AssemblyName System.Windows.Forms; [System.Windows.Forms.Clipboard]::SetImage([System.Drawing.Image]::FromFile('`+tmpPath+`'))`).Run()
	default:
		return nil
	}
	cmd.Stdin = bytes.NewReader(pngData)
	return cmd.Run()
}
