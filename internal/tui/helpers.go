package tui

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	// minTermWidth and minTermHeight are the smallest terminal every screen
	// supports. Below this, screens show a resize notice instead.
	minTermWidth  = 20
	minTermHeight = 8

	// defaultTermWidth and defaultTermHeight are used before the first
	// WindowSizeMsg arrives.
	defaultTermWidth  = 80
	defaultTermHeight = 24

	hintSeparator = " · "
)

// termSize returns the size a screen lays itself out in.
func termSize(width, height int) (int, int) {
	if width <= 0 {
		width = defaultTermWidth
	}
	if height <= 0 {
		height = defaultTermHeight
	}
	return width, height
}

// tooSmall reports whether a known terminal size is below the minimum.
// An unknown size (zero) is not treated as too small.
func tooSmall(width, height int) bool {
	return width > 0 && height > 0 && (width < minTermWidth || height < minTermHeight)
}

// tooSmallView is the notice shown in place of a screen on a tiny terminal.
func tooSmallView(width, height int) string {
	lines := []string{
		errorStyle.Render(truncate("Terminal too small", width)),
		mutedStyle.Render(truncate(fmt.Sprintf("Resize to at least %dx%d", minTermWidth, minTermHeight), width)),
	}
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center,
		lipgloss.JoinVertical(lipgloss.Center, lines...))
}

// truncate fits plain text into width display cells, ending with "…" when it
// is cut. Control characters, including line breaks, become spaces so the
// result is always one row.
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	if lipgloss.Width(s) <= width {
		return s
	}
	return ansi.Truncate(s, width, "…")
}

// fitTail keeps the end of plain text that fits in width cells, so the most
// recently typed characters stay visible.
func fitTail(s string, width int) string {
	if width <= 0 {
		return ""
	}
	w := lipgloss.Width(s)
	if w <= width {
		return s
	}
	return "…" + ansi.TruncateLeft(s, w-width+1, "")
}

// fitLine clips a rendered row to width cells, keeping ANSI styling intact.
func fitLine(line string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(line, width, "")
}

// wrapText word-wraps plain text to width cells and returns its rows.
func wrapText(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	return strings.Split(lipgloss.NewStyle().Width(width).Render(s), "\n")
}

// frame stacks a screen. Header rows stay on top and footer rows stay at the
// bottom; body rows fill the space between them. Body rows that do not fit
// are dropped, and every row is clipped to width, so nothing wraps or runs
// off the terminal.
func frame(width, height int, header, body, footer []string) string {
	room := max(height-len(header)-len(footer), 0)
	rows := make([]string, 0, height)
	rows = append(rows, header...)
	for i := range room {
		if i < len(body) {
			rows = append(rows, body[i])
		} else {
			rows = append(rows, "")
		}
	}
	rows = append(rows, footer...)
	if len(rows) > height {
		rows = rows[:height]
	}
	for i, row := range rows {
		rows[i] = fitLine(row, width)
	}
	return strings.Join(rows, "\n")
}

// clipRows keeps the first n rows of a rendered block.
func clipRows(block string, n int) string {
	rows := strings.Split(block, "\n")
	if len(rows) > n {
		rows = rows[:max(n, 0)]
	}
	return strings.Join(rows, "\n")
}

// scrollWindow returns the first visible index of a list that shows capacity
// rows at a time. It moves offset as little as needed to keep cursor visible,
// and clamps it so the window never runs past the end of the list. Every
// list view uses this, so rendering and scroll adjustment agree.
func scrollWindow(offset, cursor, total, capacity int) int {
	capacity = max(capacity, 1)
	maxOffset := max(total-capacity, 0)
	offset = min(max(offset, 0), maxOffset)
	if cursor < offset {
		offset = cursor
	}
	if cursor >= offset+capacity {
		offset = cursor - capacity + 1
	}
	return min(max(offset, 0), maxOffset)
}

// keyHint is one key and what it does, shown in a screen's footer.
type keyHint struct {
	key   string
	label string
}

// hintLine renders key hints on one row. Hints that do not fit are dropped
// from the end, so list the most important first.
func hintLine(width int, hints ...keyHint) string {
	out := ""
	for _, h := range hints {
		part := keyStyle.Render(h.key) + " " + mutedStyle.Render(h.label)
		next := part
		if out != "" {
			next = out + mutedStyle.Render(hintSeparator) + part
		}
		if lipgloss.Width(next) > width {
			break
		}
		out = next
	}
	if out == "" && len(hints) > 0 {
		return fitLine(keyStyle.Render(hints[0].key), width)
	}
	return out
}

// titleRow puts left text at the start of a row and right text at its end.
func titleRow(width int, left, right string) string {
	right = truncate(right, width/2)
	gap := 0
	if right != "" {
		gap = 1
	}
	left = truncate(left, width-lipgloss.Width(right)-gap)
	pad := max(width-lipgloss.Width(left)-lipgloss.Width(right), 0)
	return titleStyle.Render(left) + strings.Repeat(" ", pad) + mutedStyle.Render(right)
}

// statusRow renders the status message for a screen's footer. An empty
// message yields a blank row, so the layout does not shift.
func statusRow(width int, msg string, isErr bool) string {
	if msg == "" {
		return ""
	}
	style := successStyle
	if isErr {
		style = errorStyle
	}
	return fitLine(style.Render(truncate(msg, width)), width)
}
