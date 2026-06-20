package reader

import "strings"

// Chapter represents a single chapter of a book.
type Chapter struct {
	Title string
	Lines []string
}

// titleHeight is the number of virtual lines a chapter title occupies when
// rendered: title line, decorative rule, and a blank separator. Only present
// when the chapter has a non-empty title.
const titleHeight = 3

// TitleHeight returns the number of virtual lines the title chrome occupies
// (title + rule + blank) when the chapter has a non-empty title, else 0.
// This is constant — independent of terminal width — so lineOffset mapping
// stays stable across resizes. Titles are truncated to a single line in the
// reader view to keep this guarantee.
func (c Chapter) TitleHeight() int {
	if strings.TrimSpace(c.Title) == "" {
		return 0
	}
	return titleHeight
}

// RenderedLineCount returns the total number of virtual lines the chapter
// occupies in the reader's virtual flow: title chrome (if any) + body lines.
func (c Chapter) RenderedLineCount() int {
	return c.TitleHeight() + len(c.Lines)
}

// BookContent holds all chapters of a parsed book.
type BookContent struct {
	Chapters []Chapter
}

// TotalLines returns the total number of virtual lines across all chapters,
// including title chrome.
func (bc *BookContent) TotalLines() int {
	total := 0
	for _, ch := range bc.Chapters {
		total += ch.RenderedLineCount()
	}
	return total
}

// ChapterStartLine returns the virtual line index where a chapter starts
// (i.e. its title line, or first body line if untitled).
func (bc *BookContent) ChapterStartLine(chapterIdx int) int {
	start := 0
	for i := 0; i < chapterIdx; i++ {
		start += bc.Chapters[i].RenderedLineCount()
	}
	return start
}

// PositionToChapter converts a global virtual line position to chapter index
// and line within that chapter.
func (bc *BookContent) PositionToChapter(globalLine int) (chapterIdx int, lineInChapter int) {
	current := 0
	for i, ch := range bc.Chapters {
		rendered := ch.RenderedLineCount()
		if globalLine < current+rendered {
			return i, globalLine - current
		}
		current += rendered
	}
	// Fallback to last position
	if len(bc.Chapters) > 0 {
		lastIdx := len(bc.Chapters) - 1
		return lastIdx, bc.Chapters[lastIdx].RenderedLineCount() - 1
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
