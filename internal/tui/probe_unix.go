//go:build unix || darwin || linux

package tui

import (
	"bytes"
	"os"
	"strconv"
	"time"

	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/unix"
)

type ProbeResult struct {
	KittySupported bool
	CellWidth      int
	CellHeight     int
	BufferedInput  []byte
}

func isDA1Sequence(seq []byte) bool {
	if !bytes.HasPrefix(seq, []byte("\x1b[")) || len(seq) < 3 || seq[len(seq)-1] != 'c' {
		return false
	}
	params := seq[2 : len(seq)-1]
	if len(params) > 0 && params[0] == '?' {
		params = params[1:]
	}
	for _, b := range params {
		if (b < '0' || b > '9') && b != ';' {
			return false
		}
	}
	return true
}

func hasCompleteDA1(data []byte) bool {
	for i := range data {
		if data[i] == 0x1b && i+1 < len(data) && data[i+1] == '[' {
			j := i + 2
			if j < len(data) && data[j] == '?' {
				j++
			}
			for j < len(data) && ((data[j] >= '0' && data[j] <= '9') || data[j] == ';') {
				j++
			}
			if j < len(data) && data[j] == 'c' {
				return true
			}
		}
	}
	return false
}

func parseProbeStream(data []byte) (kittySupported bool, cellW int, cellH int, userKeys []byte) {
	cellW, cellH = 0, 0
	for i := 0; i < len(data); {
		if data[i] == 0x1b && i+1 < len(data) {
			if data[i+1] == '_' {
				// APC sequence: \x1b_ ... (\x1b\ or \x07)
				end := -1
				for j := i + 2; j < len(data); j++ {
					if data[j] == 0x07 {
						end = j + 1
						break
					}
					if data[j] == 0x1b && j+1 < len(data) && data[j+1] == '\\' {
						end = j + 2
						break
					}
				}
				if end != -1 {
					payload := data[i:end]
					if bytes.Contains(payload, []byte("i=31")) {
						if bytes.Contains(payload, []byte("OK")) {
							kittySupported = true
						}
						i = end
						continue
					}
					// Preserve user/other APC sequences
					userKeys = append(userKeys, payload...)
					i = end
					continue
				}
			} else if data[i+1] == '[' {
				// CSI sequence
				j := i + 2
				if j < len(data) && (data[j] == '?' || data[j] == '>' || data[j] == '=') {
					j++
				}
				for j < len(data) && (data[j] >= 0x30 && data[j] <= 0x3f) {
					j++
				}
				if j < len(data) && data[j] >= 0x40 && data[j] <= 0x7e {
					finalByte := data[j]
					csiSeq := data[i : j+1]
					if finalByte == 't' && bytes.HasPrefix(csiSeq, []byte("\x1b[6;")) {
						// CSI 16t response: \x1b[6;<height>;<width>t
						params := string(csiSeq[4 : len(csiSeq)-1])
						parts := bytes.Split([]byte(params), []byte(";"))
						if len(parts) >= 2 {
							h, err1 := strconv.Atoi(string(parts[0]))
							w, err2 := strconv.Atoi(string(parts[1]))
							if err1 == nil && err2 == nil && h > 0 && w > 0 {
								cellH = h
								cellW = w
							}
						}
						i = j + 1
						continue
					} else if finalByte == 'c' && isDA1Sequence(csiSeq) {
						// DA1 response: \x1b[?...c or \x1b[...c
						i = j + 1
						continue
					}
					// Other CSI sequence (e.g. arrow keys \x1b[A): preserve for userKeys
					userKeys = append(userKeys, csiSeq...)
					i = j + 1
					continue
				}
			}
		}
		userKeys = append(userKeys, data[i])
		i++
	}
	return kittySupported, cellW, cellH, userKeys
}

func probeTerminal() ProbeResult {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(uintptr(fd)) {
		return ProbeResult{KittySupported: false, CellWidth: 8, CellHeight: 16}
	}

	cellW, cellH := 0, 0
	ws, err := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
	if err == nil && ws != nil && ws.Col > 0 && ws.Row > 0 && ws.Xpixel > 0 && ws.Ypixel > 0 {
		cellW = int(ws.Xpixel / ws.Col)
		cellH = int(ws.Ypixel / ws.Row)
	}

	oldState, err := term.MakeRaw(uintptr(fd))
	if err != nil {
		if cellW <= 0 || cellH <= 0 {
			cellW, cellH = 8, 16
		}
		return ProbeResult{KittySupported: false, CellWidth: cellW, CellHeight: cellH}
	}
	defer term.Restore(uintptr(fd), oldState)

	// Send CSI 16t + Kitty query (i=31) + DA1
	query := []byte("\x1b[16t\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\\x1b[c")
	_, _ = os.Stdout.Write(query)

	deadline := time.Now().Add(300 * time.Millisecond)
	var rawBuf []byte
	readBuf := make([]byte, 1024)

	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		timeoutMs := int(remaining.Milliseconds())
		if timeoutMs <= 0 {
			break
		}
		pfd := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, err := unix.Poll(pfd, timeoutMs)
		if err != nil || n <= 0 {
			break
		}
		if pfd[0].Revents&unix.POLLIN != 0 {
			nr, err := unix.Read(fd, readBuf)
			if err != nil || nr <= 0 {
				break
			}
			rawBuf = append(rawBuf, readBuf[:nr]...)
			if hasCompleteDA1(rawBuf) {
				break
			}
		}
	}

	kittySupported, probedW, probedH, _ := parseProbeStream(rawBuf)
	if probedW > 0 && probedH > 0 {
		cellW = probedW
		cellH = probedH
	}
	if cellW <= 0 || cellH <= 0 {
		cellW, cellH = 8, 16
	}

	return ProbeResult{
		KittySupported: kittySupported,
		CellWidth:      cellW,
		CellHeight:     cellH,
		BufferedInput:  rawBuf,
	}
}

func beginGraphicsInput() (func(), error) {
	fd := os.Stdin.Fd()
	if !term.IsTerminal(fd) {
		return nil, nil
	}
	old, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	return func() { _ = term.Restore(fd, old) }, nil
}

func waitGraphicsSuffix(file *os.File, delay time.Duration) bool {
	fd := []unix.PollFd{{Fd: int32(file.Fd()), Events: unix.POLLIN}}
	n, err := unix.Poll(fd, int(delay.Milliseconds()))
	return err == nil && n > 0 && fd[0].Revents&unix.POLLIN != 0
}
