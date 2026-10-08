package tui

import (
	"errors"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	pbc "github.com/micronull/pocketbook-cloud-client"

	"github.com/aliefe/pocketbook-tui/internal/reader"
)

// resumeSource says where a reading position came from.
type resumeSource int

const (
	sourceCloud resumeSource = iota
	sourceLocal
)

// resumePoint is a place in a book where the reader can start. chapter and
// lineOffset use the reader's virtual lines. percent is the percentage the
// source recorded: the Cloud percentage, or the local save's percentage. approx
// marks a Cloud place estimated from its percentage. when is when the source
// recorded the position, and it may be zero.
type resumePoint struct {
	source     resumeSource
	chapter    int
	lineOffset int
	percent    int
	approx     bool
	when       time.Time
	isImage    bool
	imageIndex int
}

// sameSpot reports whether two points start the reader on the same line.
func (p resumePoint) sameSpot(q resumePoint) bool {
	return p.chapter == q.chapter && p.lineOffset == q.lineOffset
}

// position converts the point to the form the reader starts from.
func (p resumePoint) position(bookHash string) *reader.Position {
	return &reader.Position{
		BookHash:     bookHash,
		ChapterIndex: p.chapter,
		LineOffset:   p.lineOffset,
		Percent:      p.percent,
	}
}

// nativeProgress is the reading position that the PocketBook Cloud account
// holds for a book.
type nativeProgress struct {
	pointer   string
	pointerPb string
	percent   int
	updated   time.Time
}

func (p nativeProgress) hasProgress() bool {
	return p.pointer != "" || p.percent > 0
}

// latestNativeProgress picks the account's newest reading position for a book
// from Position and ReadPosition. The newer Updated wins, and a tie goes to
// Position. A book whose status is "unread" has no reading position, whatever
// its timestamps say.
func latestNativeProgress(book pbc.Book) (nativeProgress, bool) {
	if book.ReadStatus == "unread" {
		return nativeProgress{}, false
	}
	position := nativeProgress{
		pointer:   book.Position.Pointer,
		pointerPb: book.Position.PointerPb,
		percent:   book.Position.Percent,
		updated:   book.Position.Updated,
	}
	readPosition := nativeProgress{
		pointer:   book.ReadPosition.Pointer,
		pointerPb: book.ReadPosition.PointerPb,
		percent:   book.ReadPosition.Percent,
		updated:   book.ReadPosition.Updated,
	}
	switch {
	case position.hasProgress() && readPosition.hasProgress():
		if readPosition.updated.After(position.updated) {
			return readPosition, true
		}
		return position, true
	case position.hasProgress():
		return position, true
	case readPosition.hasProgress():
		return readPosition, true
	}
	return nativeProgress{}, false
}

// cloudBookmark places the Cloud reading position in content. An EPUB pointer
// that resolves gives an exact place. Otherwise the Cloud percentage gives an
// approximate place. When the place is approximate or missing, the returned
// error says why. A bookmark with no pointer and no percentage is missing.
func cloudBookmark(native nativeProgress, ok bool, content *reader.BookContent, bm *reader.Bookmark) (*resumePoint, error) {
	if !ok {
		return nil, nil
	}
	if bm != nil && bm.Err == nil {
		return &resumePoint{
			source:     sourceCloud,
			chapter:    bm.Chapter,
			lineOffset: bm.LineOffset,
			percent:    native.percent,
			when:       native.updated,
			isImage:    bm.IsImage,
			imageIndex: bm.ImageIndex,
		}, nil
	}

	reason := errors.New("pointers are read only for EPUB books")
	if bm != nil {
		reason = bm.Err
	}
	if native.percent <= 0 {
		return nil, reason
	}
	chapter, line := content.LocateFraction(native.percent)
	return &resumePoint{
		source:     sourceCloud,
		chapter:    chapter,
		lineOffset: line,
		percent:    native.percent,
		approx:     true,
		when:       native.updated,
	}, reason
}

// localResumePoint places a saved local position where the reader will show
// it. The position must already have its legacy migration applied. It returns
// nil when the position names no chapter of the book.
func localResumePoint(pos *reader.Position, content *reader.BookContent, savedAt time.Time) *resumePoint {
	if pos == nil || pos.ChapterIndex < 0 || pos.ChapterIndex >= len(content.Chapters) {
		return nil
	}
	count := content.Chapters[pos.ChapterIndex].RenderedLineCount()
	line := min(max(pos.LineOffset, 0), max(count-1, 0))
	return &resumePoint{
		source:     sourceLocal,
		chapter:    pos.ChapterIndex,
		lineOffset: line,
		percent:    pos.Percent,
		when:       savedAt,
	}
}

// resumeNotice explains a start that is not an exact resume, or any read error
// the user must see. An exact Cloud resume gets a short note with the Cloud
// percentage, so it is not mistaken for the header's line-based percentage. It
// returns "" when nothing needs saying. isErr marks a problem rather than a note.
func resumeNotice(msg OpenBookMsg, chosen *resumePoint) (string, bool) {
	switch {
	case chosen == nil && msg.PositionErr != nil:
		return fmt.Sprintf("Saved position unreadable, starting at the beginning: %v", msg.PositionErr), true
	case chosen == nil && msg.CloudErr != nil:
		return fmt.Sprintf("Cloud bookmark not located, starting at the beginning: %v", msg.CloudErr), true
	case chosen == nil:
		return "", false
	case msg.PositionErr != nil && chosen.source == sourceCloud:
		return fmt.Sprintf("Saved position unreadable, using the Cloud bookmark: %v", msg.PositionErr), true
	case chosen.approx:
		return fmt.Sprintf("Cloud bookmark not located exactly, showing about %d%% of the book", chosen.percent), false
	case msg.CloudErr != nil && chosen.source == sourceLocal:
		return fmt.Sprintf("Cloud bookmark not located, using the saved position: %v", msg.CloudErr), true
	case chosen.source == sourceCloud && chosen.percent > 0:
		return fmt.Sprintf("Cloud bookmark: %d%%", chosen.percent), false
	}
	return "", false
}

// resumeDecisionMsg ends the source choice. A cancelled choice opens nothing.
type resumeDecisionMsg struct {
	open      OpenBookMsg
	chosen    resumePoint
	cancelled bool
}

// openCancelledMsg tells the library and details screens that an open was
// cancelled, so the "Opening" status is dropped. An error is kept.
type openCancelledMsg struct{}

// resumeModel asks which of two differing positions to open a book at. Cloud
// is selected first.
type resumeModel struct {
	open   OpenBookMsg
	cloud  resumePoint
	local  resumePoint
	choice resumeSource
	width  int
	height int
}

func newResumeModel(open OpenBookMsg, local, cloud resumePoint) resumeModel {
	return resumeModel{open: open, cloud: cloud, local: local, choice: sourceCloud}
}

func (m resumeModel) Init() tea.Cmd {
	return nil
}

func (m resumeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			m.choice = sourceCloud
		case "down", "j":
			m.choice = sourceLocal
		case "enter":
			return m, m.decide(false)
		case "esc", "q":
			return m, m.decide(true)
		case "ctrl+c":
			return m, tea.Quit
		}
	}
	return m, nil
}

// decide ends the choice. Nothing is saved here.
func (m resumeModel) decide(cancelled bool) tea.Cmd {
	chosen := m.cloud
	if m.choice == sourceLocal {
		chosen = m.local
	}
	decision := resumeDecisionMsg{open: m.open, chosen: chosen, cancelled: cancelled}
	return func() tea.Msg {
		return decision
	}
}

func (m resumeModel) View() string {
	if tooSmall(m.width, m.height) {
		return tooSmallView(m.width, m.height)
	}
	w, h := termSize(m.width, m.height)

	header := []string{titleRow(w, "Resume reading", displayTitle(m.open.BookTitle))}
	body := []string{
		"",
		resumeRow(w, m.cloud, m.choice == sourceCloud),
		resumeRow(w, m.local, m.choice == sourceLocal),
		"",
		mutedStyle.Render(truncate("  The two positions differ.", w)),
	}
	footer := []string{
		hintLine(w,
			keyHint{"enter", "open"},
			keyHint{"↑/↓", "choose"},
			keyHint{"esc", "cancel"},
		),
	}
	return frame(w, h, header, body, footer)
}

// resumeRow draws one source with its percentage, and its time when there is
// room for it.
func resumeRow(width int, p resumePoint, selected bool) string {
	name := "Cloud"
	if p.source == sourceLocal {
		name = "Local"
	}
	pct := fmt.Sprintf("%d%%", p.percent)
	if p.approx {
		pct = "~" + pct
	}
	avail := max(width-2, 1)

	text := fmt.Sprintf("%-6s %4s", name, pct)
	if !p.when.IsZero() {
		when := p.when.Local().Format("2006-01-02 15:04")
		if len(text)+2+len(when) <= avail {
			text += "  " + when
		}
	}
	text = truncate(text, avail)

	if selected {
		return selectedMark.Render("› ") + selectedStyle.Render(text)
	}
	return "  " + mutedStyle.Render(text)
}
