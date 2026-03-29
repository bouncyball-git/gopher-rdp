//go:build gui

package gui

import "github.com/bouncyball-git/gopher-rdp/display"

func readClipboardImpl() (string, error) {
	return display.ReadClipboard()
}

func writeClipboardImpl(text string) error {
	return display.WriteClipboard(text)
}

func readClipboardImageImpl() ([]byte, error) {
	return display.ReadClipboardImage()
}

func writeClipboardImageImpl(pngData []byte) error {
	return display.WriteClipboardImage(pngData)
}
