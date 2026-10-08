package tui

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/aliefe/pocketbook-tui/internal/reader"
)

type readerModel struct {
	content       *reader.BookContent
	bookHash      string
	bookTitle     string
	chapterIdx    int
	lineOffset    int
	width         int
	height        int
	ready         bool
	statusMsg     string
	statusIsError bool
	showHelp      bool
	// cloud is the book's Cloud link, or nil when the reader has none, as for
	// fixtures and tests. Nothing is sent to Cloud without it.
	cloud        *cloudLink
	phase        syncPhase
	conflict     *nativeProgress // Cloud state that differs from the session's baseline
	syncErr      error
	startChapter int  // where the session began, for the start rules
	startLine    int  //
	startApprox  bool // the start came from a percentage, not an exact location
	startExact   bool // the start is an exact Cloud bookmark, kept unless the reader moves
	// session identifies this reading session. Its requests carry it, so a result
	// from another session of the same book is ignored.
	session uint64
	// gen counts the requests this session has started. Only the result of the
	// current request is applied.
	gen uint64
	// cloudTimeout bounds each native request this session makes.
	cloudTimeout time.Duration
	// refreshing is true while the background Cloud read is in flight.
	refreshing bool
	// cancel ends the background Cloud read. It is nil when no read is in flight.
	cancel context.CancelFunc
	// offer is a Cloud position that differs from the session's baseline. It is
	// not applied until the user reviews it with C.
	offer *nativeProgress
	// note is the Cloud status, or nil when there is none.
	note *cloudStatus
}

// readerSessions numbers reading sessions, so that results can be matched to
// the session that started them.
var readerSessions atomic.Uint64

func newReaderModel(content *reader.BookContent, bookHash, bookTitle string, pos *reader.Position, width, height int) readerModel {
	m := readerModel{
		content:      content,
		bookHash:     bookHash,
		bookTitle:    bookTitle,
		width:        width,
		height:       height,
		ready:        width > 0 && height > 0,
		session:      readerSessions.Add(1),
		cloudTimeout: defaultCloudTimeout,
	}

	if pos != nil {
		// Apply legacy v0 -> v1 migration if needed (shifts lineOffset by
		// the chapter's title height so it still points at the same body
		// line).
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

func (m *readerModel) setStatus(msg string, isErr bool) {
	m.statusMsg = msg
	m.statusIsError = isErr
}

func (m readerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		return m, nil

	case syncResultMsg:
		return m.onSync(msg)

	case cloudReloadMsg:
		return m.onReload(msg)

	case refreshMsg:
		return m.onRefresh(msg)

	case tea.KeyMsg:
		// While a request is in flight, only ctrl+c is accepted.
		if m.phase == phaseSyncing && msg.String() != "ctrl+c" {
			return m, nil
		}
		if next, cmd, ok := m.promptKey(msg.String()); ok {
			return next, cmd
		}
		switch msg.String() {
		case "q", "esc":
			return m.leave()

		case "ctrl+c":
			// Explicit escape hatch for when saving keeps failing: quit
			// without saving the position.
			return m, tea.Quit

		case "?":
			m.showHelp = !m.showHelp
			return m, nil
		case "C":
			return m.reviewCloud()

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
	// The body holds exactly the rows left over, as frame shows them. Chapter
	// title chrome lives inside the virtual flow, so no special casing is needed.
	return max(m.height-4, 1)
}

func (m *readerModel) percent() int {
	return reader.CalculatePercent(m.content, m.chapterIdx, m.lineOffset)
}

// position is the reading position to save: the current chapter and line.
func (m *readerModel) position() *reader.Position {
	return &reader.Position{
		BookHash:     m.bookHash,
		ChapterIndex: m.chapterIdx,
		LineOffset:   m.lineOffset,
		Percent:      m.percent(),
	}
}

func (m readerModel) View() string {
	if m.showHelp {
		return m.helpView()
	}

	if !m.ready {
		return mutedStyle.Render("Loading…")
	}

	if tooSmall(m.width, m.height) {
		return tooSmallView(m.width, m.height)
	}

	W := m.width
	H := m.height

	if m.content == nil || len(m.content.Chapters) == 0 {
		header := []string{titleRow(W, "PocketBook Reader", "")}
		body := []string{
			"",
			errorStyle.Render("  Error: Book has no readable content"),
		}
		for _, line := range wrapText("The book could not be parsed. It may be DRM-protected or in an unsupported format.", W-2) {
			body = append(body, mutedStyle.Render("  "+line))
		}
		footer := []string{"", hintLine(W, keyHint{"q", "quit"})}
		return frame(W, H, header, body, footer)
	}

	ch := m.currentChapter()
	if ch == nil {
		return mutedStyle.Render("No content available")
	}

	// Reading column: cap at a comfortable width, center it.
	contentW := min(max(W-4, 10), 72)
	leftMargin := max((W-contentW)/2, 0)

	// Body area = terminal minus header(1) + top rule(1) + bottom rule(1) + footer(1)
	pageSize := m.pageSize()

	// ── Header: book title (left) + percent (right), dim & calm ─────────────
	pct := fmt.Sprintf("%d%%", m.percent())
	title := truncate(m.bookTitle, max(W-len(pct)-1, 0))
	header := readDim.Render(title) +
		strings.Repeat(" ", max(W-lipgloss.Width(title)-len(pct), 0)) +
		readDim.Render(pct)
	rule := readDim.Render(strings.Repeat("─", W))

	// ── Body: virtual flow render ───────────────────────────────────────────
	// The virtual flow is: [title chrome (titleHeight lines)] [body lines].
	// lineOffset indexes this flow. We render `pageSize` lines starting at
	// lineOffset, mixing title chrome and body lines as needed.
	titleH := ch.TitleHeight()
	bodyCount := len(ch.Lines)
	blankRow := lipgloss.NewStyle().Width(contentW).Render("")

	var pieces []string
	added := 0
	emit := func(s string) {
		if added >= pageSize {
			return
		}
		pieces = append(pieces, s)
		added++
	}
	prompt := m.promptRows(contentW, pageSize)
	for _, row := range prompt {
		emit(row)
	}
	promptLen := len(pieces)

	// Title chrome — shown when the viewport overlaps the title region.
	if titleH > 0 {
		// Title line
		if m.lineOffset <= 0 {
			titleText := truncate(strings.TrimSpace(ch.Title), contentW-4)
			emit(readChapter.Width(contentW).Align(lipgloss.Center).Render(titleText))
		}

		// Decorative rule under the title
		if m.lineOffset <= 1 && added < pageSize {
			runeCount := min(lipgloss.Width(strings.TrimSpace(ch.Title))+4, contentW)
			emit(readDim.Width(contentW).Align(lipgloss.Center).Render(strings.Repeat("═", runeCount)))
		}

		// Blank separator
		if m.lineOffset <= 2 && added < pageSize {
			emit(blankRow)
		}
	}

	// Body lines — start at max(0, lineOffset - titleH) in body index.
	bodyStart := max(m.lineOffset-titleH, 0)
	prevBlank := false
	emptyPage := titleH == 0 // if title shows, page isn't "empty"
	for i := bodyStart; i < bodyCount && added < pageSize; i++ {
		line := strings.TrimSpace(ch.Lines[i])
		if line == "" {
			if prevBlank {
				continue
			}
			prevBlank = true
			emit(blankRow)
			continue
		}
		prevBlank = false
		emptyPage = false
		for _, w := range reader.WrapLine(line, contentW) {
			if added >= pageSize {
				break
			}
			emit(readText.Width(contentW).Render(w))
		}
	}

	// End markers
	if emptyPage && len(pieces) > promptLen && bodyStart >= bodyCount-1 {
		label := "(end of chapter)"
		if m.chapterIdx >= len(m.content.Chapters)-1 {
			label = "(end of book)"
		}
		pieces[len(pieces)-1] = readDim.Width(contentW).Align(lipgloss.Center).Render(label)
	}

	// Fill remaining space
	for added < pageSize {
		pieces = append(pieces, blankRow)
		added++
	}

	body := lipgloss.JoinVertical(lipgloss.Left, pieces...)
	body = lipgloss.NewStyle().MarginLeft(leftMargin).Render(body)

	// ── Footer: chapter info (left) + status or help hint (right), dim ──────
	chTitle := strings.TrimSpace(ch.Title)
	if chTitle != "" {
		chTitle = truncate(chTitle, max(contentW-16, 5))
	}
	leftInfo := fmt.Sprintf("Ch %d/%d", m.chapterIdx+1, len(m.content.Chapters))
	if chTitle != "" {
		leftInfo += " · " + chTitle
	}
	leftInfo = truncate(leftInfo, W)

	rightInfo := "?:help · q:quit"
	rightStyle := readDim
	if m.statusMsg != "" {
		rightInfo = m.statusMsg
		if m.statusIsError {
			rightStyle = errorStyle
		}
	}
	rightInfo = truncate(rightInfo, max(W-lipgloss.Width(leftInfo)-1, 0))
	gap := max(W-lipgloss.Width(leftInfo)-lipgloss.Width(rightInfo), 0)
	footer := readDim.Render(leftInfo) + strings.Repeat(" ", gap) + rightStyle.Render(rightInfo)

	return frame(W, H,
		[]string{header, rule},
		strings.Split(body, "\n"),
		[]string{rule, footer},
	)
}

func (m readerModel) helpView() string {
	w, h := termSize(m.width, m.height)
	inner := max(w-4, 1)
	lines := []string{"READER CONTROLS"}
	if m.note != nil && m.note.help != "" {
		lines = append(lines, wrapText(m.note.help, inner)...)
	}
	lines = append(lines,
		"j/k ↓/↑        line down / up",
		"d/u PgDn/PgUp  page down / up",
		"f/space        next page",
		"b              previous page",
		"n / p          next / previous chapter",
		"g / G          chapter start / end",
		"?              toggle this help",
		"q / esc        save here, sync to Cloud, back to library",
		"o c l enter    Cloud conflict: overwrite, load Cloud, keep here, keep reading",
		"r l enter      failed sync: retry, keep here, keep reading",
		"C              Cloud: review a changed position, or check again",
		"ctrl+c         quit without saving",
	)
	for i, line := range lines {
		lines[i] = truncate(line, inner)
	}
	box := lipgloss.NewStyle().
		Foreground(colorText).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorFaint).
		Padding(0, 1).
		Render(strings.Join(lines, "\n"))
	box = clipRows(box, h)
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, box)
}

// OpenBookMsg is sent to open a book in the reader. Err is set when the book
// could not be opened. Origin is the screen that started the open. A failed
// open goes back to that screen.
//
// Position is the local saved position, with any legacy migration applied.
// PositionErr is set when that position exists but cannot be read. Cloud is the
// account's reading position placed in Content, or nil when there is none or it
// cannot be placed at all. CloudErr says why Cloud is approximate or missing.
type OpenBookMsg struct {
	Content         *reader.BookContent
	BookHash        string
	BookTitle       string
	Position        *reader.Position
	PositionErr     error
	PositionSavedAt time.Time
	Cloud           *resumePoint
	CloudErr        error
	// Sync is the book's Cloud link, or nil when the book has none.
	Sync   *cloudLink
	Err    error
	Origin screen
}
