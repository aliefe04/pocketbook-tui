package tui

import (
	"context"
	"fmt"
	"image"
	"strings"
	"sync/atomic"
	"time"

	"github.com/aliefe/pocketbook-tui/internal/config"
	"github.com/aliefe/pocketbook-tui/internal/reader"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type readerModel struct {
	content                                  *reader.BookContent
	bookHash, bookTitle                      string
	chapterIdx, lineOffset                   int
	withinLineOffset                         int // Source byte offset, independent of terminal width.
	withinLinePart                           int
	startWithinPart                          int
	showImage                                bool
	imageSelection, imagePan, imagePanX      int
	imageZoom                                float64
	imageCache                               []string
	imageCacheWidth, imageCacheHeight        int
	imageCacheNative                         bool
	imageDecoded                             image.Image
	imageDecodedAt                           imageAddress
	imageNotice                              string
	prefs                                    config.Preferences
	layout                                   *readerLayout
	pageMode                                 bool
	width, height                            int
	ready                                    bool
	statusMsg                                string
	statusIsError, showHelp                  bool
	helpScroll                               int
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
	m.prefs = config.DefaultPreferences()
	if pos != nil && content != nil {
		pos.ApplyMigration(content)
		if pos.ChapterIndex >= 0 && pos.ChapterIndex < len(content.Chapters) {
			m.chapterIdx, m.lineOffset = pos.ChapterIndex, pos.LineOffset
		}
	}
	m.clampLineOffset()
	m.ensureLayout()
	if m.lineOffset == 0 {
		m.goToChapterStart()
	}
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
	margin := min(m.prefs.HorizontalMargin, max((w-16)/2, 0))
	available := max(w-2*margin, 1)
	width := m.prefs.ReadingWidth
	if width == 0 {
		width = 72
	}
	return min(width, available)
}
func (m *readerModel) ensureLayout() {
	w := m.contentWidth()
	cellW, cellH := currentCellGeometry()
	options := flowOptions{
		lineGap:      m.prefs.LineSpacing,
		paragraphGap: m.prefs.ParagraphSpacing,
		justify:      m.prefs.Alignment == "justify",
		imageMode:    m.prefs.ImageMode,
		imageHeight:  m.prefs.ImageHeight,
		theme:        m.prefs.Theme,
		native:       isNativeGraphicsActive(),
		cellW:        cellW,
		cellH:        cellH,
	}
	if m.layout == nil || m.layout.content != m.content || m.layout.width != w || m.layout.options != options {
		m.layout = buildReaderLayout(m.content, w, options)
	}
}
func (m *readerModel) rowIndex() int {
	m.ensureLayout()
	return m.layout.index(m.chapterIdx, m.lineOffset, m.withinLineOffset, m.withinLinePart)
}
func (m *readerModel) setRow(index int) {
	m.ensureLayout()
	if len(m.layout.rows) == 0 {
		return
	}
	r := m.layout.rows[min(max(index, 0), len(m.layout.rows)-1)]
	m.chapterIdx, m.lineOffset, m.withinLineOffset = r.chapter, r.line, r.offset
	m.withinLinePart = r.part
}
func (m *readerModel) moveRows(delta int)  { m.setRow(m.rowIndex() + delta) }
func (m *readerModel) scrollDown(rows int) { m.moveRows(max(rows, 0)) }
func (m *readerModel) scrollUp(rows int)   { m.moveRows(-max(rows, 0)) }
func (m *readerModel) bodyRows() int {
	_, h := termSize(m.width, m.height)
	chrome := 0
	if m.prefs.ShowHeader {
		chrome += 2
	}
	if m.prefs.ShowFooter {
		chrome += 2
	}
	if m.statusMsg != "" {
		chrome++
	}
	return max(h-chrome, 1)
}
func (m *readerModel) verticalPadding() int {
	rows := m.bodyRows()
	prompt := len(m.promptRows(m.contentWidth(), rows))
	return min(m.prefs.VerticalMargin, max((rows-prompt-1)/2, 0))
}
func (m *readerModel) pageSize() int { return max(m.bodyRows()-2*m.verticalPadding(), 1) }
func (m *readerModel) textCapacity() int {
	return max(m.pageSize()-len(m.promptRows(m.contentWidth(), m.pageSize())), 1)
}
func (m *readerModel) pageDown() {
	i, n := m.rowIndex(), m.textCapacity()
	if i+n < len(m.layout.rows) {
		m.setRow(i + max(n-min(m.prefs.PageOverlap, n-1), 1))
	}
}
func (m *readerModel) pageUp() {
	n := m.textCapacity()
	m.moveRows(-max(n-min(m.prefs.PageOverlap, n-1), 1))
}
func (m *readerModel) jumpChapter(direction int) {
	m.ensureLayout()
	if m.content == nil {
		return
	}
	for chapter := m.chapterIdx + direction; chapter >= 0 && chapter < len(m.content.Chapters); chapter += direction {
		index := m.layout.chapterStart(chapter)
		if index < len(m.layout.rows) && m.layout.rows[index].chapter == chapter {
			m.setRow(index)
			return
		}
	}
}
func (m *readerModel) nextChapter() { m.jumpChapter(1) }
func (m *readerModel) prevChapter() { m.jumpChapter(-1) }
func (m *readerModel) goToChapterStart() {
	m.ensureLayout()
	m.setRow(m.layout.chapterStart(m.chapterIdx))
}
func (m *readerModel) goToChapterEnd() {
	m.ensureLayout()
	end := m.layout.index(m.chapterIdx, m.chapterVirtualCount(), 0, 0)
	start := m.layout.chapterStart(m.chapterIdx)
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
		if m.showImage {
			m.ensureImageCache()
		}
	case syncResultMsg:
		return m.onSync(msg)
	case cloudReloadMsg:
		return m.onReload(msg)
	case refreshMsg:
		return m.onRefresh(msg)
	case openOriginalResultMsg:
		if msg.session != m.session {
			return m, nil
		}
		if msg.err != nil {
			m.setStatus(fmt.Sprintf("Cannot open image: %v", msg.err), true)
			m.imageNotice = "Cannot open original"
		}
		if msg.err == nil {
			m.imageNotice = "Original opened"
		}
		return m, nil
	case tea.MouseMsg:
		if m.prefs.Mouse && m.phase != phaseSyncing {
			if m.showHelp {
				if msg.Button == tea.MouseButtonWheelDown {
					m.helpScroll++
				}
				if msg.Button == tea.MouseButtonWheelUp {
					m.helpScroll = max(m.helpScroll-1, 0)
				}
				return m, nil
			}
			if m.showImage {
				if msg.Button == tea.MouseButtonWheelDown {
					return m.handleImageKey("down")
				}
				if msg.Button == tea.MouseButtonWheelUp {
					return m.handleImageKey("up")
				}
				return m, nil
			}
			if msg.Button == tea.MouseButtonWheelDown {
				if m.pageMode {
					m.pageDown()
				} else {
					m.scrollDown(1)
				}
			}
			if msg.Button == tea.MouseButtonWheelUp {
				if m.pageMode {
					m.pageUp()
				} else {
					m.scrollUp(1)
				}
			}
		}
	case tea.KeyMsg:
		key := msg.String()
		if m.phase == phaseSyncing && key != "ctrl+c" {
			return m, nil
		}
		if m.showImage {
			return m.handleImageKey(key)
		}
		if m.showHelp {
			switch key {
			case "q", "esc", "?":
				m.showHelp = false
			case "down", "j":
				m.helpScroll++
			case "up", "k":
				m.helpScroll = max(m.helpScroll-1, 0)
			case "pgdown", " ":
				m.helpScroll += max(m.height-2, 1)
			case "pgup":
				m.helpScroll = max(m.helpScroll-max(m.height-2, 1), 0)
			case "ctrl+c":
				m.invalidate()
				return m, tea.Quit
			}
			return m, nil
		}
		if next, cmd, ok := m.promptKey(key); ok {
			return next, cmd
		}
		nav := m.prefs.NavigationKeys
		switch config.KeyName(key) {
		case nav.NextPage:
			m.pageDown()
			return m, nil
		case nav.PrevPage:
			m.pageUp()
			return m, nil
		case nav.LineDown:
			m.scrollDown(1)
			return m, nil
		case nav.LineUp:
			m.scrollUp(1)
			return m, nil
		}
		if key == "I" {
			m.openImage()
			return m, nil
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
		case "d", "pgdown", "f", "right":
			m.pageDown()
		case "u", "pgup", "left":
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
	if m.showImage && !tooSmall(m.width, m.height) {
		return m.imageView()
	}
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
		body = append(body, style.Width(column).Render(m.layout.rowText(r)))
	}
	for len(body) < rows {
		body = append(body, strings.Repeat(" ", column))
	}
	padding := m.verticalPadding()
	outer := make([]string, 0, len(body)+2*padding)
	for range padding {
		outer = append(outer, "")
	}
	outer = append(outer, body...)
	for range padding {
		outer = append(outer, "")
	}
	body = outer
	for i := range body {
		body[i] = strings.Repeat(" ", margin) + body[i]
	}
	pct := m.progressText()
	headerRight := pct
	if !m.prefs.ShowFooter {
		settingsHint := "S:settings"
		withSettings := settingsHint
		if pct != "" {
			withSettings = settingsHint + " · " + pct
		}
		if lipgloss.Width(withSettings)+1 <= w {
			headerRight = withSettings
		}
	}
	title := truncate(m.bookTitle, max(w-lipgloss.Width(headerRight)-1, 0))
	header := readDim.Render(title) + strings.Repeat(" ", max(w-lipgloss.Width(title)-lipgloss.Width(headerRight), 0)) + readDim.Render(headerRight)
	rule := readDim.Render(strings.Repeat("─", w))
	chTitle := ""
	if ch := m.currentChapter(); ch != nil {
		chTitle = cleanBookText(strings.TrimSpace(ch.Title))
	}
	chCount := fmt.Sprintf("Ch %d/%d", m.chapterIdx+1, len(m.content.Chapters))
	mode := "pages"
	if !m.pageMode {
		mode = "scroll"
	}
	cand1 := mode + " · S:settings · ?:help"
	cand2 := mode + " · S:settings"
	cand3 := "S:settings"

	var right string
	switch {
	case lipgloss.Width(chCount)+1+lipgloss.Width(cand1) <= w:
		right = cand1
	case lipgloss.Width(chCount)+1+lipgloss.Width(cand2) <= w:
		right = cand2
	default:
		right = truncate(cand3, max(w-lipgloss.Width(chCount)-1, 0))
	}

	leftRoom := max(w-lipgloss.Width(right)-1, 0)
	var left string
	if chTitle != "" && leftRoom > lipgloss.Width(chCount)+3 {
		titleRoom := leftRoom - lipgloss.Width(chCount) - 3
		left = chCount + " · " + truncate(chTitle, titleRoom)
	} else {
		left = truncate(chCount, leftRoom)
	}

	controls := readDim.Render(left) + strings.Repeat(" ", max(w-lipgloss.Width(left)-lipgloss.Width(right), 0)) + readDim.Render(right)
	var headers, footers []string
	if m.prefs.ShowHeader {
		headers = []string{header, rule}
	}
	if m.prefs.ShowFooter {
		if m.statusMsg != "" {
			footers = []string{rule, statusRow(w, m.statusMsg, m.statusIsError), controls}
		} else {
			footers = []string{rule, controls}
		}
	} else if m.statusMsg != "" {
		footers = []string{statusRow(w, m.statusMsg, m.statusIsError)}
	}
	return frame(w, h, headers, body, footers)
}

func (m readerModel) helpView() string {
	w, h := termSize(m.width, m.height)
	inner := max(w-2, 1)
	var lines []string
	if m.statusMsg != "" {
		lines = append(lines, wrapText(m.statusMsg, inner)...)
	}
	if m.note != nil && m.note.help != "" && m.note.help != m.statusMsg {
		lines = append(lines, wrapText(m.note.help, inner)...)
	}
	lines = append(lines,
		fmt.Sprintf("%s/%s  one displayed row", m.prefs.NavigationKeys.LineDown, m.prefs.NavigationKeys.LineUp),
		fmt.Sprintf("%s/%s  next / previous page", m.prefs.NavigationKeys.NextPage, m.prefs.NavigationKeys.PrevPage),
		"↓/↑   follow page/scroll mode", "←/→ PgUp/PgDn  previous / next page",
		"P  toggle page/scroll mode", "S  persistent settings", "I  photo viewer",
		"n/p g/G  chapter next/previous, start/end", "?  toggle help",
		"C  check Cloud or review a changed position", "q/esc  save, sync, return",
		"o c l enter  conflict: overwrite, load, local, read",
		"r l enter  failed: retry, local, read", "ctrl+c  quit without saving")
	var wrapped []string
	for _, line := range lines {
		wrapped = append(wrapped, wrapText(line, inner)...)
	}
	capacity := max(h-2, 1)
	start := min(max(m.helpScroll, 0), max(len(wrapped)-capacity, 0))
	return frame(w, h, []string{titleRow(w, "Reader help", "")}, wrapped[start:min(start+capacity, len(wrapped))], []string{truncate("↑↓ scroll · ?:back", w)})
}

func (m *readerModel) progressText() string {
	percent := fmt.Sprintf("%d%%", m.percent())
	capacity := m.textCapacity()
	pages := fmt.Sprintf("p%d/%d", m.rowIndex()/capacity+1, max((len(m.layout.rows)+capacity-1)/capacity, 1))
	switch m.prefs.Progress {
	case "pages":
		return pages
	case "both":
		return percent + " · " + pages
	case "none":
		return ""
	default:
		return percent
	}
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
