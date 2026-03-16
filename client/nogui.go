//go:build !gui

package main

import (
	"fmt"

	rdp "gopher-rdp"
)

const guiAvailable = false

func runGUIChild() {}

func runGUI(_ *rdp.Client, _ *rdp.Options, _ []rdp.MonitorConfig, _, _ int) error {
	return fmt.Errorf("GUI not available (build without -tags gui)")
}
