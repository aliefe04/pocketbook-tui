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

	if pos != nil && pos.ChapterIndex < len(content.Chapters) {
		m.chapterIdx = pos.ChapterIndex
		m.lineOffset = pos.LineOffset
	} else {
		m.chapterIdx = 0
		m.lineOffset = 0
	}

	return m
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

	m.lineOffset += lines
	if m.lineOffset >= len(ch.Lines) {
		if m.chapterIdx < len(m.content.Chapters)-1 {
			m.chapterIdx++
			m.lineOffset = 0
		} else {
			m.lineOffset = len(ch.Lines) - 1
		}
	}
}

func (m *readerModel) scrollUp(lines int) {
	m.lineOffset -= lines
	if m.lineOffset < 0 {
		if m.chapterIdx > 0 {
			m.chapterIdx--
			ch := m.currentChapter()
			if ch != nil {
				m.lineOffset = len(ch.Lines) - 1
				if m.lineOffset < 0 {
					m.lineOffset = 0
				}
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
	ch := m.currentChapter()
	if ch != nil {
		m.lineOffset = len(ch.Lines) - m.pageSize()
		if m.lineOffset < 0 {
			m.lineOffset = 0
		}
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
	// Reserve: header(1) + border(1) + content(N) + border(1) + footer(1) = 4 lines
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

	// Calculate exact available space
	pageSize := m.pageSize()
	wrapWidth := m.width - 4
	if wrapWidth < 10 {
		wrapWidth = 10
	}

	// Header
	percent := m.percent()
	percentPart := lipgloss.NewStyle().Foreground(DimColor).Render(fmt.Sprintf("%d%%", percent))
	// Truncate title to leave room for percentage
	maxTitleW := m.width - lipgloss.Width(percentPart) - 6
	if maxTitleW < 5 {
		maxTitleW = 5
	}
	titleText := truncate(m.bookTitle, maxTitleW)
	titlePart := lipgloss.NewStyle().Bold(true).Foreground(PrimaryColor).Render(fmt.Sprintf(">> %s", titleText))
	spacerWidth := m.width - lipgloss.Width(titlePart) - lipgloss.Width(percentPart) - 2
	if spacerWidth < 0 {
		spacerWidth = 0
	}
	header := lipgloss.JoinHorizontal(lipgloss.Left,
		titlePart,
		lipgloss.NewStyle().Width(spacerWidth).Render(""),
		percentPart,
	)

	// Content area - fill entire screen
	var visibleLines []string
	linesAdded := 0
	for i := m.lineOffset; i < len(ch.Lines) && linesAdded < pageSize; i++ {
		line := ch.Lines[i]
		wrapped := reader.WrapLine(line, wrapWidth)
		for _, w := range wrapped {
			if linesAdded >= pageSize {
				break
			}
			visibleLines = append(visibleLines, w)
			linesAdded++
		}
	}

	// Fill remaining space
	for linesAdded < pageSize {
		visibleLines = append(visibleLines, "")
		linesAdded++
	}

	content := strings.Join(visibleLines, "\n")
	if strings.TrimSpace(content) == "" {
		content = strings.Repeat("\n", pageSize-1) + "(End of chapter)"
	}

	// Footer
	chapterInfo := fmt.Sprintf("Ch %d/%d | Line %d/%d | Total %d",
		m.chapterIdx+1, len(m.content.Chapters),
		m.lineOffset, len(ch.Lines),
		m.content.TotalLines())
	if ch.Title != "" {
		chapterTitle := truncate(ch.Title, m.width-40)
		chapterInfo = fmt.Sprintf("%s • %s", chapterTitle, chapterInfo)
	}

	footerText := fmt.Sprintf("%s • ?:help • q:quit", chapterInfo)
	footerStyle := lipgloss.NewStyle().Foreground(DimColor)
	if m.width > 0 {
		footerStyle = footerStyle.MaxWidth(m.width)
	}
	footer := footerStyle.Render(footerText)

	return lipgloss.JoinVertical(lipgloss.Left,
		header,
		lipgloss.NewStyle().Foreground(BorderColor).Render(strings.Repeat("─", m.width)),
		content,
		lipgloss.NewStyle().Foreground(BorderColor).Render(strings.Repeat("─", m.width)),
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
