package tui

import (
	"bytes"
	"io"
	"os"
	"strings"
	"time"
)

// Probe replies can arrive after the startup deadline. Keep them out of login
// and filter text while replaying real keys, including control sequences.
type graphicsInput struct {
	source          io.Reader
	queued, pending []byte
	readBuf         [4096]byte
}

func newGraphicsInput(initial []byte, source io.Reader) *graphicsInput {
	r := &graphicsInput{source: source}
	r.queued, r.pending = splitGraphicsReplies(initial, false)
	return r
}
func (r *graphicsInput) Read(p []byte) (int, error) {
	for {
		if len(r.queued) > 0 {
			n := copy(p, r.queued)
			r.queued = r.queued[n:]
			return n, nil
		}
		if len(r.pending) > 0 && !isDefiniteProbeReply(r.pending) {
			if file, ok := r.source.(*os.File); ok && !waitGraphicsSuffix(file, 20*time.Millisecond) {
				r.queued, r.pending = splitGraphicsReplies(r.pending, true)
				if len(r.queued) > 0 {
					continue
				}
			}
		}
		n, err := r.source.Read(r.readBuf[:])
		data := make([]byte, 0, len(r.pending)+n)
		data = append(data, r.pending...)
		data = append(data, r.readBuf[:n]...)
		r.queued, r.pending = splitGraphicsReplies(data, err != nil)
		if len(r.pending) > 4096 {
			r.pending = nil
		}
		if len(r.queued) > 0 {
			n := copy(p, r.queued)
			r.queued = r.queued[n:]
			return n, nil
		}
		if err != nil {
			return 0, err
		}
	}
}
func isDefiniteProbeReply(p []byte) bool {
	return bytes.HasPrefix(p, []byte("\x1b_Gi=31;")) || bytes.HasPrefix(p, []byte("\x1b[6;")) || bytes.HasPrefix(p, []byte("\x1b[?"))
}
func splitGraphicsReplies(data []byte, final bool) (keys, pending []byte) {
	prefixes := []string{"\x1b_Gi=31;", "\x1b[6;", "\x1b[?"}
	for i := 0; i < len(data); {
		rest := data[i:]
		possible := false
		for _, prefix := range prefixes {
			if strings.HasPrefix(prefix, string(rest)) || bytes.HasPrefix(rest, []byte(prefix)) {
				possible = true
				break
			}
		}
		if possible {
			end := -1
			if bytes.HasPrefix(rest, []byte("\x1b_")) {
				if j := bytes.Index(rest, []byte("\x1b\\")); j >= 0 {
					end = j + 2
				}
			} else if bytes.HasPrefix(rest, []byte("\x1b[")) {
				for j := 2; j < len(rest); j++ {
					if rest[j] >= 0x40 && rest[j] <= 0x7e {
						end = j + 1
						break
					}
				}
			}
			if end < 0 {
				if !final {
					return keys, append([]byte(nil), rest...)
				}
				if isDefiniteProbeReply(rest) {
					return keys, nil
				}
			} else {
				seq := rest[:end]
				own := bytes.HasPrefix(seq, []byte("\x1b_Gi=31;")) || (bytes.HasPrefix(seq, []byte("\x1b[6;")) && seq[len(seq)-1] == 't') || isGraphicsDA1(seq)
				if !own {
					keys = append(keys, seq...)
				}
				i += end
				continue
			}
		}
		keys = append(keys, data[i])
		i++
	}
	return keys, nil
}
func isGraphicsDA1(p []byte) bool {
	if !bytes.HasPrefix(p, []byte("\x1b[?")) || len(p) < 5 || p[len(p)-1] != 'c' {
		return false
	}
	for _, b := range p[3 : len(p)-1] {
		if (b < '0' || b > '9') && b != ';' {
			return false
		}
	}
	return true
}
