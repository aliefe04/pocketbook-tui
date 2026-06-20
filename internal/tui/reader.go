package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/aliefe/pocketbook-tui/internal/reader"
)

type readerModel struct {
	content      *reader.BookContent
	bookHash     string
	bookTitle    string
	chapterIdx   int
	lineOffset   int
	width        int
	height       int
	ready        bool
	statusMsg    string
	showHelp     bool
}

func newReaderModel(content *reader.BookContent, bookHash, bookTitle string, pos *reader.Position, width, height int) readerModel {
	m := readerModel{
		content:   content,
		bookHash:  bookHash,
		bookTitle: bookTitle,
		width:     width,
		height:    height,
		ready:     width > 0 && height > 0,
	}

	if pos != nil {
		// Apply legacy v0 -> v1 migration if needed (shifts lineOffset by
		// the chapter's title height so it still points at the same body line).
		pos.ApplyMigration(content)
		if pos.ChapterIndex < len(content.Chapters) {
			m.chapterIdx = pos.ChapterIndex
			m.lineOffset = pos.LineOffset
		}
	}

	// Clamp lineOffset into the chapter's virtual flow.
	m.clampLineOffset()

	return m
}

// chapterVirtualCount returns the number of virtual lines in the current
// chapter (title chrome + body).
func (m *readerModel) chapterVirtualCount() int {
	ch := m.currentChapter()
	if ch == nil {
		return 0
	}
	return ch.RenderedLineCount()
}

// clampLineOffset keeps lineOffset within [0, chapterVirtualCount-1].
// Used after navigation and after migration.
func (m *readerModel) clampLineOffset() {
	count := m.chapterVirtualCount()
	if count == 0 {
		m.lineOffset = 0
		return
	}
	if m.lineOffset < 0 {
		m.lineOffset = 0
	}
	if m.lineOffset >= count {
		m.lineOffset = count - 1
	}
}

func (m readerModel) Init() tea.Cmd {
	return nil
}

type savePositionMsg struct{}

func (m readerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			return m, m.savePosition()

		case "?":
			m.showHelp = !m.showHelp
			return m, nil

		case "j", "down":
			m.scrollDown(1)
		case "k", "up":
			m.scrollUp(1)
		case "d", "pgdown":
			m.scrollDown(m.pageSize())
		case "u", "pgup":
			m.scrollUp(m.pageSize())
		case "f", " ":
			m.scrollDown(m.pageSize())
		case "b":
			m.scrollUp(m.pageSize())
		case "n":
			m.nextChapter()
		case "p":
			m.prevChapter()
		case "g":
			m.goToChapterStart()
		case "G":
			m.goToChapterEnd()
		}

	case savePositionMsg:
		return m, func() tea.Msg {
			return BackToLibraryMsg{}
		}
	}

	return m, nil
}

func (m *readerModel) scrollDown(lines int) {
	ch := m.currentChapter()
	if ch == nil {
		return
	}

	count := ch.RenderedLineCount()
	m.lineOffset += lines
	if m.lineOffset >= count {
		if m.chapterIdx < len(m.content.Chapters)-1 {
			// Carry the overflow into the next chapter.
			overflow := m.lineOffset - count
			m.chapterIdx++
			next := m.currentChapter()
			if next != nil {
				m.lineOffset = overflow
				if m.lineOffset >= next.RenderedLineCount() {
					m.lineOffset = next.RenderedLineCount() - 1
				}
			} else {
				m.lineOffset = 0
			}
		} else {
			m.lineOffset = count - 1
		}
	}
}

func (m *readerModel) scrollUp(lines int) {
	m.lineOffset -= lines
	if m.lineOffset < 0 {
		if m.chapterIdx > 0 {
			overflow := -m.lineOffset
			m.chapterIdx--
			prev := m.currentChapter()
			if prev != nil {
				pc := prev.RenderedLineCount()
				m.lineOffset = pc - overflow
				if m.lineOffset < 0 {
					m.lineOffset = 0
				}
			} else {
				m.lineOffset = 0
			}
		} else {
			m.lineOffset = 0
		}
	}
}

func (m *readerModel) nextChapter() {
	if m.chapterIdx < len(m.content.Chapters)-1 {
		m.chapterIdx++
		m.lineOffset = 0
	}
}

func (m *readerModel) prevChapter() {
	if m.chapterIdx > 0 {
		m.chapterIdx--
		m.lineOffset = 0
	}
}

func (m *readerModel) goToChapterStart() {
	m.lineOffset = 0
}

func (m *readerModel) goToChapterEnd() {
	count := m.chapterVirtualCount()
	if count == 0 {
		m.lineOffset = 0
		return
	}
	m.lineOffset = count - m.pageSize()
	if m.lineOffset < 0 {
		m.lineOffset = 0
	}
}

func (m *readerModel) currentChapter() *reader.Chapter {
	if m.chapterIdx >= 0 && m.chapterIdx < len(m.content.Chapters) {
		return &m.content.Chapters[m.chapterIdx]
	}
	return nil
}

func (m *readerModel) pageSize() int {
	if !m.ready || m.height == 0 {
		return 20 // Default until we know terminal size
	}
	// header(1) + top rule(1) + bottom rule(1) + footer(1) = 4 chrome lines.
	// Body area holds the rest. Chapter title chrome lives inside the virtual
	// flow, so no special casing is needed here.
	ps := m.height - 4
	if ps < 5 {
		ps = 5
	}
	return ps
}

// minReaderWidth is the minimum terminal width for a usable reading experience.
const minReaderWidth = 20

func (m *readerModel) percent() int {
	return reader.CalculatePercent(m.content, m.chapterIdx, m.lineOffset)
}

func (m *readerModel) savePosition() tea.Cmd {
	pos := &reader.Position{
		BookHash:     m.bookHash,
		ChapterIndex: m.chapterIdx,
		LineOffset:   m.lineOffset,
		Percent:      m.percent(),
	}
	if err := reader.SavePosition(pos); err != nil {
		m.statusMsg = fmt.Sprintf("Save error: %v", err)
	}
	return func() tea.Msg {
		return savePositionMsg{}
	}
}

func (m readerModel) View() string {
	if m.showHelp {
		return m.helpView()
	}

	if !m.ready {
		return "Loading..."
	}

	// Terminal too small
	if m.width < minReaderWidth || m.height < 8 {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			ErrorStyle.Render("Terminal too small. Please resize to at least 20x8."))
	}

	if m.content == nil || len(m.content.Chapters) == 0 {
		return lipgloss.JoinVertical(lipgloss.Left,
			TitleStyle.Render("PocketBook Reader"),
			BoxStyle.Render(
				lipgloss.JoinVertical(lipgloss.Left,
					ErrorStyle.Render("Error: Book has no readable content"),
					"The book could not be parsed. It may be DRM-protected or in an unsupported format.",
				),
			),
			HelpStyle.Render("q: quit"),
		)
	}

	ch := m.currentChapter()
	if ch == nil {
		return "No content available"
	}

	W := m.width
	H := m.height

	// Reading column: cap at a comfortable width, center it.
	contentW := W - 4
	if contentW > 72 {
		contentW = 72
	}
	if contentW < 10 {
		contentW = 10
	}
	leftMargin := (W - contentW) / 2
	if leftMargin < 0 {
		leftMargin = 0
	}

	// Body area = terminal minus header(1) + top rule(1) + bottom rule(1) + footer(1)
	pageSize := H - 4
	if pageSize < 5 {
		pageSize = 5
	}

	// ── Header: book title (left) + percent (right), dim & calm ─────────────
	percent := m.percent()
	percentStr := fmt.Sprintf("%d%%", percent)
	bookTitle := truncate(m.bookTitle, contentW)
	headerLeft := lipgloss.NewStyle().Foreground(DimColor).Render(bookTitle)
	headerRight := lipgloss.NewStyle().Foreground(DimColor).Render(percentStr)
	spacerW := W - lipgloss.Width(headerLeft) - lipgloss.Width(headerRight)
	if spacerW < 0 {
		spacerW = 0
	}
	header := lipgloss.JoinHorizontal(lipgloss.Left,
		headerLeft,
		lipgloss.NewStyle().Width(spacerW).Render(""),
		headerRight,
	)

	// ── Body: virtual flow render ───────────────────────────────────────────
	// The virtual flow is: [title chrome (titleHeight lines)] [body lines].
	// lineOffset indexes this flow. We render `pageSize` lines starting at
	// lineOffset, mixing title chrome and body lines as needed.
	titleH := ch.TitleHeight()
	bodyCount := len(ch.Lines)
	virtualCount := titleH + bodyCount

	var pieces []string
	added := 0
	emit := func(s string) {
		if added >= pageSize {
			return
		}
		pieces = append(pieces, s)
		added++
	}

	// Title chrome — shown when the viewport overlaps the title region.
	if titleH > 0 {
		// Title line
		if m.lineOffset <= 0 {
			titleText := truncate(strings.TrimSpace(ch.Title), contentW-4)
			emit(lipgloss.NewStyle().
				Width(contentW).
				Align(lipgloss.Center).
				Bold(true).
				Foreground(ChapterColor).
				Render(titleText))
		} else {
			// Skip past this virtual line
		}

		// Decorative rule under the title
		if m.lineOffset <= 1 && added < pageSize {
			runeCount := len([]rune(strings.TrimSpace(ch.Title))) + 4
			if runeCount > contentW {
				runeCount = contentW
			}
			emit(lipgloss.NewStyle().
				Width(contentW).
				Align(lipgloss.Center).
				Foreground(ReadingDimColor).
				Render(strings.Repeat("═", runeCount)))
		}

		// Blank separator
		if m.lineOffset <= 2 && added < pageSize {
			emit(lipgloss.NewStyle().Width(contentW).Render(""))
		}
	}

	// Body lines — start at max(0, lineOffset - titleH) in body index.
	bodyStart := m.lineOffset - titleH
	if bodyStart < 0 {
		bodyStart = 0
	}
	prevBlank := false
	emptyPage := titleH == 0 // if title shows, page isn't "empty"
	for i := bodyStart; i < bodyCount && added < pageSize; i++ {
		line := strings.TrimSpace(ch.Lines[i])
		if line == "" {
			if prevBlank {
				continue
			}
			prevBlank = true
			emit(lipgloss.NewStyle().Width(contentW).Render(""))
			continue
		}
		prevBlank = false
		emptyPage = false
		wrapped := reader.WrapLine(line, contentW)
		for _, w := range wrapped {
			if added >= pageSize {
				break
			}
			emit(lipgloss.NewStyle().
				Width(contentW).
				Foreground(ReadingColor).
				Render(w))
		}
	}

	// End markers
	if emptyPage && added > 0 && bodyStart >= bodyCount-1 {
		isLast := m.chapterIdx >= len(m.content.Chapters)-1
		label := "(end of chapter)"
		if isLast {
			label = "(end of book)"
		}
		pieces[len(pieces)-1] = lipgloss.NewStyle().
			Width(contentW).
			Align(lipgloss.Center).
			Foreground(ReadingDimColor).
			Render(label)
	}

	// Fill remaining space
	for added < pageSize {
		pieces = append(pieces, lipgloss.NewStyle().Width(contentW).Render(""))
		added++
	}

	body := lipgloss.JoinVertical(lipgloss.Left, pieces...)
	body = lipgloss.NewStyle().MarginLeft(leftMargin).Render(body)

	_ = virtualCount // currently only used implicitly via added/bodyStart

	// ── Footer: chapter info (left) + help hint (right), dim ────────────────
	chTitle := strings.TrimSpace(ch.Title)
	maxChTitleW := contentW - 16
	if maxChTitleW < 5 {
		maxChTitleW = 5
	}
	if chTitle != "" {
		chTitle = truncate(chTitle, maxChTitleW)
	}
	var leftInfo string
	if chTitle != "" {
		leftInfo = fmt.Sprintf("Ch %d/%d · %s", m.chapterIdx+1, len(m.content.Chapters), chTitle)
	} else {
		leftInfo = fmt.Sprintf("Ch %d/%d", m.chapterIdx+1, len(m.content.Chapters))
	}
	footLeft := lipgloss.NewStyle().Foreground(ReadingDimColor).Render(leftInfo)
	footRightStr := "?:help · q:quit"
	spacerF := W - lipgloss.Width(footLeft) - len([]rune(footRightStr))
	// If no room for spacer, truncate the help hint
	if spacerF < 0 {
		avail := W - lipgloss.Width(footLeft) - 2
		if avail < 0 {
			avail = 0
		}
		footRightStr = truncate(footRightStr, avail)
		spacerF = W - lipgloss.Width(footLeft) - len([]rune(footRightStr))
		if spacerF < 0 {
			spacerF = 0
		}
	}
	footRight := lipgloss.NewStyle().Foreground(ReadingDimColor).Render(footRightStr)
	footer := lipgloss.JoinHorizontal(lipgloss.Left,
		footLeft,
		lipgloss.NewStyle().Width(spacerF).Render(""),
		footRight,
	)

	// Thin sepia rules between chrome and body
	rule := lipgloss.NewStyle().Foreground(ReadingDimColor).Render(strings.Repeat("─", W))

	return lipgloss.JoinVertical(lipgloss.Left,
		header,
		rule,
		body,
		rule,
		footer,
	)
}

func (m readerModel) helpView() string {
	help := []string{
		"",
		"  READER CONTROLS",
		"",
		"  j/↓     Scroll down 1 line",
		"  k/↑     Scroll up 1 line",
		"  d/PgDn  Scroll down 1 page",
		"  u/PgUp  Scroll up 1 page",
		"  f/space Next page",
		"  b       Previous page",
		"  n       Next chapter",
		"  p       Previous chapter",
		"  g       Go to chapter start",
		"  G       Go to chapter end",
		"  ?       Toggle this help",
		"  q/esc   Quit and save position",
		"",
	}

	helpW := m.width - 4
	if helpW < 30 {
		helpW = 30
	}

	helpContent := strings.Join(help, "\n")

	if m.width > 0 && m.height > 0 {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			lipgloss.NewStyle().
				Width(helpW).
				Border(lipgloss.RoundedBorder()).
				BorderForeground(BorderColor).
				Render(helpContent))
	}

	return lipgloss.NewStyle().
		Width(helpW).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(BorderColor).
		Render(helpContent)
}

// OpenBookMsg is sent to open a book in the reader.
type OpenBookMsg struct {
	Content   *reader.BookContent
	BookHash  string
	BookTitle string
	Position  *reader.Position
	Err       error
}
