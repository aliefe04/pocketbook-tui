package tui

import (
	"sort"
	"strings"

	"github.com/aliefe/pocketbook-tui/internal/reader"
	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"
)

// visualRow retains a width-independent source anchor. Byte offsets are in the
// sanitized semantic line, never persisted or used as EPUB character offsets.
type visualRow struct {
	text                  string
	chapter, line, offset int
	title                 bool
}

type readerLayout struct {
	content *reader.BookContent
	width   int
	rows    []visualRow
}

func cleanBookText(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' {
			return ' '
		}
		if r < 32 || r == 127 || (r >= 128 && r < 160) {
			return -1
		}
		return r
	}, ansi.Strip(s))
}

// wrapSource wraps by grapheme-cell width and returns each row's source byte
// offset. Whitespace is the only content dropped at a wrap boundary.
func wrapSource(s string, width int) []visualRow {
	s = strings.TrimSpace(cleanBookText(s))
	if s == "" {
		return []visualRow{{}}
	}
	width = max(width, 1)
	type cluster struct {
		start, end, width int
		blank             bool
	}
	var clusters []cluster
	g := uniseg.NewGraphemes(s)
	for g.Next() {
		a, b := g.Positions()
		clusters = append(clusters, cluster{a, b, g.Width(), strings.TrimSpace(g.Str()) == ""})
	}
	var rows []visualRow
	for start := 0; start < len(clusters); {
		for start < len(clusters) && clusters[start].blank {
			start++
		}
		if start == len(clusters) {
			break
		}
		end, cells, lastBlank := start, 0, -1
		for end < len(clusters) {
			c := clusters[end]
			if cells+c.width > width && end > start {
				break
			}
			cells += c.width
			if c.blank {
				lastBlank = end
			}
			end++
			if cells >= width {
				break
			}
		}
		cut := end
		if end < len(clusters) && !clusters[end].blank && lastBlank > start {
			cut = lastBlank
		}
		if cut <= start {
			cut = start + 1
		}
		text := strings.TrimRight(s[clusters[start].start:clusters[cut-1].end], " \t")
		rows = append(rows, visualRow{text: text, offset: clusters[start].start})
		start = cut
	}
	return rows
}

func buildReaderLayout(content *reader.BookContent, width int) *readerLayout {
	l := &readerLayout{content: content, width: max(width, 1)}
	if content == nil {
		return l
	}
	for ci, ch := range content.Chapters {
		if ch.TitleHeight() > 0 {
			title := truncate(cleanBookText(strings.TrimSpace(ch.Title)), max(width-4, 1))
			l.rows = append(l.rows, visualRow{text: title, chapter: ci, line: 0, title: true},
				visualRow{text: strings.Repeat("═", min(ansi.StringWidth(title)+4, width)), chapter: ci, line: 1, title: true},
				visualRow{chapter: ci, line: 2, title: true})
		}
		for li, text := range ch.Lines {
			for _, row := range wrapSource(text, width) {
				row.chapter, row.line = ci, ch.TitleHeight()+li
				l.rows = append(l.rows, row)
			}
		}
	}
	return l
}

func (l *readerLayout) index(chapter, line, offset int) int {
	if l == nil || len(l.rows) == 0 {
		return 0
	}
	i := sort.Search(len(l.rows), func(i int) bool {
		r := l.rows[i]
		return r.chapter > chapter || (r.chapter == chapter && (r.line > line || (r.line == line && r.offset > offset)))
	})
	return max(i-1, 0)
}
