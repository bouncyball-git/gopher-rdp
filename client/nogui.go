//go:build !gui

package main

import (
	"fmt"

	rdp "github.com/bouncyball-git/gopher-rdp"
)

const guiAvailable = false

func guiDPR() float64 { return 1.0 }

func runGUIChild() {}

func runGUI(_ *rdp.Client, _ *rdp.Options, _ []rdp.MonitorConfig, _, _ int) error {
	return fmt.Errorf("GUI not available (build without -tags gui)")
}
