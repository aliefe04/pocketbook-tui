package tui

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/aliefe/pocketbook-tui/internal/reader"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type readerModel struct {
	content                                  *reader.BookContent
	bookHash, bookTitle                      string
	chapterIdx, lineOffset                   int
	withinLineOffset                         int // Source byte offset, independent of terminal width.
	layout                                   *readerLayout
	pageMode                                 bool
	width, height                            int
	ready                                    bool
	statusMsg                                string
	statusIsError, showHelp                  bool
	cloud                                    *cloudLink
	phase                                    syncPhase
	conflict                                 *nativeProgress
	syncErr                                  error
	startChapter, startLine, startWithinLine int
	startApprox, startExact                  bool
	session, gen                             uint64
	cloudTimeout                             time.Duration
	refreshing                               bool
	cancel                                   context.CancelFunc
	offer                                    *nativeProgress
	note                                     *cloudStatus
}

var readerSessions atomic.Uint64

func newReaderModel(content *reader.BookContent, bookHash, bookTitle string, pos *reader.Position, width, height int) readerModel {
	m := readerModel{content: content, bookHash: bookHash, bookTitle: cleanBookText(bookTitle), pageMode: true, width: width, height: height, ready: width > 0 && height > 0, session: readerSessions.Add(1), cloudTimeout: defaultCloudTimeout}
	if pos != nil && content != nil {
		pos.ApplyMigration(content)
		if pos.ChapterIndex >= 0 && pos.ChapterIndex < len(content.Chapters) {
			m.chapterIdx, m.lineOffset = pos.ChapterIndex, pos.LineOffset
		}
	}
	m.clampLineOffset()
	m.ensureLayout()
	return m
}

func (m *readerModel) currentChapter() *reader.Chapter {
	if m.content != nil && m.chapterIdx >= 0 && m.chapterIdx < len(m.content.Chapters) {
		return &m.content.Chapters[m.chapterIdx]
	}
	return nil
}
func (m *readerModel) chapterVirtualCount() int {
	if ch := m.currentChapter(); ch != nil {
		return ch.RenderedLineCount()
	}
	return 0
}
func (m *readerModel) clampLineOffset() {
	m.lineOffset = min(max(m.lineOffset, 0), max(m.chapterVirtualCount()-1, 0))
}
func (m readerModel) Init() tea.Cmd                      { return nil }
func (m *readerModel) setStatus(text string, isErr bool) { m.statusMsg, m.statusIsError = text, isErr }
func (m readerModel) contentWidth() int {
	w, _ := termSize(m.width, m.height)
	return min(max(w-4, 1), 72)
}
func (m *readerModel) ensureLayout() {
	w := m.contentWidth()
	if m.layout == nil || m.layout.content != m.content || m.layout.width != w {
		m.layout = buildReaderLayout(m.content, w)
	}
}
func (m *readerModel) rowIndex() int {
	m.ensureLayout()
	return m.layout.index(m.chapterIdx, m.lineOffset, m.withinLineOffset)
}
func (m *readerModel) setRow(index int) {
	m.ensureLayout()
	if len(m.layout.rows) == 0 {
		return
	}
	r := m.layout.rows[min(max(index, 0), len(m.layout.rows)-1)]
	m.chapterIdx, m.lineOffset, m.withinLineOffset = r.chapter, r.line, r.offset
}
func (m *readerModel) moveRows(delta int)  { m.setRow(m.rowIndex() + delta) }
func (m *readerModel) scrollDown(rows int) { m.moveRows(max(rows, 0)) }
func (m *readerModel) scrollUp(rows int)   { m.moveRows(-max(rows, 0)) }
func (m *readerModel) pageSize() int       { _, h := termSize(m.width, m.height); return max(h-4, 1) }
func (m *readerModel) textCapacity() int {
	return max(m.pageSize()-len(m.promptRows(m.contentWidth(), m.pageSize())), 1)
}
func (m *readerModel) pageDown() {
	i, n := m.rowIndex(), m.textCapacity()
	if i+n < len(m.layout.rows) {
		m.setRow(i + n)
	}
}
func (m *readerModel) pageUp() { m.moveRows(-m.textCapacity()) }
func (m *readerModel) nextChapter() {
	if m.content != nil && m.chapterIdx < len(m.content.Chapters)-1 {
		m.chapterIdx++
		m.lineOffset, m.withinLineOffset = 0, 0
	}
}
func (m *readerModel) prevChapter() {
	if m.chapterIdx > 0 {
		m.chapterIdx--
		m.lineOffset, m.withinLineOffset = 0, 0
	}
}
func (m *readerModel) goToChapterStart() { m.lineOffset, m.withinLineOffset = 0, 0 }
func (m *readerModel) goToChapterEnd() {
	m.ensureLayout()
	end := m.layout.index(m.chapterIdx, m.chapterVirtualCount(), 0)
	start := m.layout.index(m.chapterIdx, 0, 0)
	m.setRow(max(start, end-m.textCapacity()+1))
}
func (m *readerModel) percent() int {
	return reader.CalculatePercent(m.content, m.chapterIdx, m.lineOffset)
}
func (m *readerModel) position() *reader.Position {
	return &reader.Position{BookHash: m.bookHash, ChapterIndex: m.chapterIdx, LineOffset: m.lineOffset, Percent: m.percent()}
}

func (m readerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height, m.ready = msg.Width, msg.Height, true
		m.ensureLayout()
	case syncResultMsg:
		return m.onSync(msg)
	case cloudReloadMsg:
		return m.onReload(msg)
	case refreshMsg:
		return m.onRefresh(msg)
	case tea.KeyMsg:
		key := msg.String()
		if m.phase == phaseSyncing && key != "ctrl+c" {
			return m, nil
		}
		if next, cmd, ok := m.promptKey(key); ok {
			return next, cmd
		}
		switch key {
		case "q", "esc":
			return m.leave()
		case "ctrl+c":
			m.invalidate()
			return m, tea.Quit
		case "?":
			m.showHelp = !m.showHelp
		case "C":
			return m.reviewCloud()
		case "j":
			m.scrollDown(1)
		case "k":
			m.scrollUp(1)
		case "down":
			if m.pageMode {
				m.pageDown()
			} else {
				m.scrollDown(1)
			}
		case "up":
			if m.pageMode {
				m.pageUp()
			} else {
				m.scrollUp(1)
			}
		case "d", "pgdown", "f", " ", "right":
			m.pageDown()
		case "u", "pgup", "b", "left":
			m.pageUp()
		case "P":
			m.pageMode = !m.pageMode
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
	w, h := m.width, m.height
	if m.content == nil || len(m.content.Chapters) == 0 {
		return frame(w, h, []string{titleRow(w, "PocketBook Reader", "")}, []string{errorStyle.Render("Book has no readable content")}, []string{hintLine(w, keyHint{"q", "back"})})
	}
	m.ensureLayout()
	column := m.contentWidth()
	margin := max((w-column)/2, 0)
	rows := m.pageSize()
	body := m.promptRows(column, rows)
	start := m.rowIndex()
	for i := start; i < len(m.layout.rows) && len(body) < rows; i++ {
		r := m.layout.rows[i]
		style := readText
		if r.title {
			style = readChapter.Align(lipgloss.Center)
			if r.line == 1 {
				style = readDim.Align(lipgloss.Center)
			}
		}
		body = append(body, style.Width(column).Render(r.text))
	}
	for len(body) < rows {
		body = append(body, strings.Repeat(" ", column))
	}
	for i := range body {
		body[i] = strings.Repeat(" ", margin) + body[i]
	}
	pct := fmt.Sprintf("%d%%", m.percent())
	title := truncate(m.bookTitle, max(w-len(pct)-1, 0))
	header := readDim.Render(title) + strings.Repeat(" ", max(w-lipgloss.Width(title)-len(pct), 0)) + readDim.Render(pct)
	rule := readDim.Render(strings.Repeat("─", w))
	chTitle := ""
	if ch := m.currentChapter(); ch != nil {
		chTitle = cleanBookText(strings.TrimSpace(ch.Title))
	}
	left := fmt.Sprintf("Ch %d/%d", m.chapterIdx+1, len(m.content.Chapters))
	if chTitle != "" {
		left += " · " + chTitle
	}
	mode := "pages"
	if !m.pageMode {
		mode = "scroll"
	}
	right := mode + " · ?:help"
	style := readDim
	if m.statusMsg != "" {
		right = m.statusMsg
		if m.statusIsError {
			style = errorStyle
		}
	}
	left = truncate(left, max(w-12, 1))
	right = truncate(right, max(w-lipgloss.Width(left)-1, 0))
	footer := readDim.Render(left) + strings.Repeat(" ", max(w-lipgloss.Width(left)-lipgloss.Width(right), 0)) + style.Render(right)
	return frame(w, h, []string{header, rule}, body, []string{rule, footer})
}

func (m readerModel) helpView() string {
	w, h := termSize(m.width, m.height)
	inner := max(w-4, 1)
	lines := []string{"READER CONTROLS"}
	if m.note != nil && m.note.help != "" {
		lines = append(lines, wrapText(m.note.help, inner)...)
	}
	lines = append(lines, "j/k            one displayed row", "↓/↑            page or row, following mode", "←/→ PgUp/PgDn  previous / next page", "d/u f/space/b  page down / up", "P              toggle page / scroll mode", "n/p g/G        chapter next/previous, start/end", "?              toggle help", "C              check Cloud or review a changed position", "q/esc          save locally, sync, return to library", "o c l enter    conflict: overwrite, load, local, read", "r l enter      failed sync: retry, local, read", "ctrl+c         quit without saving")
	for i, line := range lines {
		lines[i] = truncate(line, inner)
	}
	box := lipgloss.NewStyle().Foreground(colorText).Border(lipgloss.RoundedBorder()).BorderForeground(colorFaint).Padding(0, 1).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, clipRows(box, h))
}

// OpenBookMsg contains parsed content and the last known paragraph positions.
// Origin identifies the screen to return to on cancellation or failure.
type OpenBookMsg struct {
	Content             *reader.BookContent
	BookHash, BookTitle string
	Position            *reader.Position
	PositionErr         error
	PositionSavedAt     time.Time
	Cloud               *resumePoint
	CloudErr            error
	Sync                *cloudLink
	Err                 error
	Origin              screen
}
