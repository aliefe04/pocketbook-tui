package reader

import "strings"

// Chapter represents a single chapter of a book.
type Chapter struct {
	Title string
	Lines []string
}

// BookContent holds all chapters of a parsed book.
type BookContent struct {
	Chapters []Chapter
}

// TotalLines returns the total number of lines across all chapters.
func (bc *BookContent) TotalLines() int {
	total := 0
	for _, ch := range bc.Chapters {
		total += len(ch.Lines)
	}
	return total
}

// ChapterStartLine returns the global line index where a chapter starts.
func (bc *BookContent) ChapterStartLine(chapterIdx int) int {
	start := 0
	for i := 0; i < chapterIdx; i++ {
		start += len(bc.Chapters[i].Lines)
	}
	return start
}

// PositionToChapter converts a global line position to chapter and line within chapter.
func (bc *BookContent) PositionToChapter(globalLine int) (chapterIdx int, lineInChapter int) {
	current := 0
	for i, ch := range bc.Chapters {
		if globalLine < current+len(ch.Lines) {
			return i, globalLine - current
		}
		current += len(ch.Lines)
	}
	// Fallback to last position
	if len(bc.Chapters) > 0 {
		lastIdx := len(bc.Chapters) - 1
		return lastIdx, len(bc.Chapters[lastIdx].Lines) - 1
	}
	return 0, 0
}

// WrapLine breaks a long line into multiple lines of maxWidth (rune-based,
// Unicode-safe). Words are kept whole; breaks happen at the last space before
// maxWidth. If no space is found, the line is force-broken at maxWidth.
func WrapLine(line string, maxWidth int) []string {
	line = strings.TrimSpace(line)
	if line == "" {
		return []string{""}
	}
	if maxWidth <= 0 {
		return []string{line}
	}

	runes := []rune(line)
	if len(runes) <= maxWidth {
		return []string{string(runes)}
	}

	var lines []string
	for len(runes) > maxWidth {
		idx := maxWidth
		// Find last space at or before maxWidth
		for i := maxWidth; i > 0; i-- {
			if runes[i] == ' ' {
				idx = i
				break
			}
		}
		// No space found — force break at maxWidth
		if idx == 0 || (idx == maxWidth && runes[maxWidth] != ' ') {
			idx = maxWidth
		}
		lines = append(lines, string(runes[:idx]))
		runes = runes[idx:]
		// Trim leading spaces on the remainder
		for len(runes) > 0 && runes[0] == ' ' {
			runes = runes[1:]
		}
	}
	if len(runes) > 0 {
		lines = append(lines, string(runes))
	}
	return lines
}
