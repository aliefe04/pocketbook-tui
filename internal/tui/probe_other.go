//go:build !unix && !darwin && !linux

package tui

import (
	"os"
	"time"
)

type ProbeResult struct {
	KittySupported bool
	CellWidth      int
	CellHeight     int
	BufferedInput  []byte
}

func parseProbeStream(data []byte) (kittySupported bool, cellW int, cellH int, userKeys []byte) {
	return false, 8, 16, data
}

func probeTerminal() ProbeResult {
	return ProbeResult{KittySupported: false, CellWidth: 8, CellHeight: 16}
}

func beginGraphicsInput() (func(), error)                        { return nil, nil }
func waitGraphicsSuffix(file *os.File, delay time.Duration) bool { return false }
