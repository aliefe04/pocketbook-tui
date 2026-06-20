package tui

import "strings"

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// truncate shortens a string to maxRunes length, adding "..." if truncated.
func truncate(s string, maxRunes int) string {
	if maxRunes <= 3 {
		return strings.Repeat(".", maxInt(0, maxRunes))
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes-3]) + "..."
}
