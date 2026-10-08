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
	part                  int
	title                 bool
	photo                 bool
	imageIndex            int
	imageRow              int
}

type flowOptions struct {
	lineGap, paragraphGap int
	justify               bool
	imageMode, theme      string
	imageHeight           int
}

type readerLayout struct {
	content   *reader.BookContent
	width     int
	options   flowOptions
	rows      []visualRow
	addresses []imageAddress
	photos    map[imageAddress][]string
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

func buildReaderLayout(content *reader.BookContent, width int, options flowOptions) *readerLayout {
	l := &readerLayout{content: content, width: max(width, 1), options: options}
	if content == nil {
		return l
	}
	for ci, ch := range content.Chapters {
		for index := range ch.Images {
			l.addresses = append(l.addresses, imageAddress{ci, index})
		}
		if ch.TitleHeight() > 0 {
			title := truncate(cleanBookText(strings.TrimSpace(ch.Title)), max(width-4, 1))
			l.rows = append(l.rows, visualRow{text: title, chapter: ci, line: 0, title: true},
				visualRow{text: strings.Repeat("═", min(ansi.StringWidth(title)+4, width)), chapter: ci, line: 1, title: true},
				visualRow{chapter: ci, line: 2, title: true})
		}
		for li, text := range ch.Lines {
			l.addImages(ch, ci, ch.TitleHeight()+li, false, 0)
			wrapped := wrapSource(text, width)
			for wi, row := range wrapped {
				row.chapter, row.line = ci, ch.TitleHeight()+li
				if options.justify && wi < len(wrapped)-1 {
					row.text = justifyRow(row.text, width)
				}
				l.rows = append(l.rows, row)
				gap := options.lineGap
				if wi == len(wrapped)-1 {
					gap += options.paragraphGap
				}
				if row.text == "" {
					gap = options.paragraphGap
				}
				for part := 1; part <= gap; part++ {
					l.rows = append(l.rows, visualRow{chapter: ci, line: row.line, offset: row.offset, part: part})
				}
			}
			l.addImages(ch, ci, ch.TitleHeight()+li, true, len(strings.TrimSpace(cleanBookText(text))))
		}
	}
	return l
}

func (l *readerLayout) addImages(ch reader.Chapter, chapter, line int, after bool, offset int) {
	for index, photo := range ch.Images {
		if photo.LineOffset != line || photo.After != after {
			continue
		}
		rows := photoFlowRows(photo, l.width, l.options.imageHeight, l.options.imageMode)
		base := -(len(ch.Images)+1)*100 + index*100
		if after {
			base = 1000 + index*100
		}
		for i, text := range rows {
			l.rows = append(l.rows, visualRow{text: text, chapter: chapter, line: line, offset: offset, part: base + i, photo: true, imageIndex: index, imageRow: i})
		}
	}
}

func (l *readerLayout) rowText(row visualRow) string {
	if !row.photo {
		return row.text
	}
	address := imageAddress{row.chapter, row.imageIndex}
	if l.photos == nil {
		l.photos = make(map[imageAddress][]string)
	}
	rows, ok := l.photos[address]
	if !ok {
		photo := l.content.Chapters[address.chapter].Images[address.index]
		actual := imageRows(photo, l.width, l.options.imageHeight, l.options.imageMode, l.options.theme)
		count := len(photoFlowRows(photo, l.width, l.options.imageHeight, l.options.imageMode))
		if len(actual) == count {
			rows = actual
		} else {
			rows = make([]string, count)
			if count > 0 && len(actual) > 0 {
				copy(rows[:count-1], actual[:len(actual)-1])
				rows[count-1] = actual[len(actual)-1]
			}
		}
		if len(l.photos) >= 8 {
			clear(l.photos)
		}
		l.photos[address] = rows
	}
	if row.imageRow < len(rows) {
		return rows[row.imageRow]
	}
	return row.text
}

func (l *readerLayout) index(chapter, line, offset, part int) int {
	if l == nil || len(l.rows) == 0 {
		return 0
	}
	i := sort.Search(len(l.rows), func(i int) bool {
		r := l.rows[i]
		return r.chapter > chapter || (r.chapter == chapter && (r.line > line || (r.line == line && (r.offset > offset || (r.offset == offset && r.part > part)))))
	})
	if part < 0 && i < len(l.rows) && l.rows[i].chapter == chapter && l.rows[i].line == line && l.rows[i].offset == offset {
		if i == 0 || l.rows[i-1].chapter != chapter || l.rows[i-1].line != line {
			return i
		}
	}
	return max(i-1, 0)
}

func (l *readerLayout) chapterStart(chapter int) int {
	return sort.Search(len(l.rows), func(i int) bool { return l.rows[i].chapter >= chapter })
}

func justifyRow(text string, width int) string {
	words := strings.Fields(text)
	if len(words) < 2 {
		return text
	}
	extra := max(width-ansi.StringWidth(text), 0)
	var b strings.Builder
	b.Grow(len(text) + extra)
	for i, word := range words {
		if i > 0 {
			spaces := 1 + extra/(len(words)-1)
			if i <= extra%(len(words)-1) {
				spaces++
			}
			b.WriteString(strings.Repeat(" ", spaces))
		}
		b.WriteString(word)
	}
	return b.String()
}
