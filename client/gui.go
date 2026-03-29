//go:build gui

package main

import (
	"fmt"
	"os"

	rdp "github.com/bouncyball-git/gopher-rdp"
	"github.com/bouncyball-git/gopher-rdp/display/gui"
)

const guiAvailable = true

func runGUIChild() {
	if os.Getenv("GOPHER_RDP_CHILD") != "" {
		if err := gui.RunChild(); err != nil {
			fmt.Fprintf(os.Stderr, "child error: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
}

func runGUI(client *rdp.Client, opts *rdp.Options, monitors []rdp.MonitorConfig, w, h int) error {
	if len(monitors) > 1 {
		monInfos := make([]gui.MonitorInfo, len(monitors))
		for i, m := range monitors {
			monInfos[i] = gui.MonitorInfo{
				X: m.X, Y: m.Y,
				Width: m.Width, Height: m.Height,
				Primary: m.Primary,
			}
		}
		return gui.RunMulti(client, opts, monInfos)
	}
	return gui.Run(client, opts, w, h)
}
