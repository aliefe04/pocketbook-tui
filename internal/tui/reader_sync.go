package tui

import (
	"context"
	"errors"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/aliefe/pocketbook-tui/internal/reader"
)

// defaultCloudTimeout bounds each native request a reading session makes. A new
// reader starts with it.
const defaultCloudTimeout = 30 * time.Second

// cloudNoteRows is how many body rows a Cloud status may take.
const cloudNoteRows = 2

// cloudStatus is a Cloud status for the reader. body is what the body shows.
// brief takes its place when body needs more than cloudNoteRows rows, and help
// is the full text that the help view shows.
type cloudStatus struct {
	body  string
	brief string
	help  string
	isErr bool
}

// syncPhase is where a reading session stands with Cloud.
type syncPhase int

const (
	// phaseIdle means no request is in flight and no prompt is open.
	phaseIdle syncPhase = iota
	// phaseSyncing means a request is in flight. Keys wait, except ctrl+c.
	phaseSyncing
	// phaseConflict means Cloud changed since the session began. The user
	// chooses what happens next.
	phaseConflict
	// phaseFailed means Cloud could not be updated. The local save is kept, and
	// the user retries or leaves it on this device.
	phaseFailed
)

// attach gives the reader its book's Cloud link. start is the place the session
// opens at, or nil when it opens at the beginning. Leaving at that place without
// moving never rewrites Cloud. An exact Cloud bookmark is only confirmed as Cloud
// holds it. An approximate one, estimated from a percentage, is never sent.
func (m *readerModel) attach(link *cloudLink, start *resumePoint) {
	m.cloud = link
	m.startChapter = m.chapterIdx
	m.startLine = m.lineOffset
	m.startWithinLine = m.withinLineOffset
	fromCloud := start != nil && start.source == sourceCloud
	m.startApprox = fromCloud && start.approx
	m.startExact = fromCloud && !start.approx
}

// saveLocal saves the current place on this device. On failure it shows the
// error and returns false. Nothing is then sent to Cloud.
func (m *readerModel) saveLocal() bool {
	if err := reader.SavePosition(m.position()); err != nil {
		m.setStatus(fmt.Sprintf("Position not saved: %v", err), true)
		return false
	}
	return true
}

// target returns the EPUB pointer and percentage for the current place. ok is
// false when the place has no exact EPUB location.
func (m readerModel) target() (cloudTarget, bool) {
	pointer, ok := m.content.PointerAt(m.chapterIdx, m.lineOffset)
	return cloudTarget{pointer: pointer, percent: m.percent()}, ok
}

// unmoved reports whether the reader is still at the place the session opened at.
func (m readerModel) unmoved() bool {
	return m.chapterIdx == m.startChapter && m.lineOffset == m.startLine && m.withinLineOffset == m.startWithinLine
}

// reqID returns the identity of the session's current request.
func (m readerModel) reqID() requestID {
	return requestID{session: m.session, gen: m.gen}
}

// invalidate ends the request in flight, if any. Its context is cancelled, and
// its result is ignored when it arrives. Nothing is written by it.
func (m *readerModel) invalidate() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.gen++
	m.refreshing = false
}

// setNote shows a Cloud status.
func (m *readerModel) setNote(body, brief, help string, isErr bool) {
	m.note = &cloudStatus{body: body, brief: brief, help: help, isErr: isErr}
}

// startRefresh begins the background read of Cloud that a session makes when it
// opens. Only an EPUB with a Cloud link is read, and only while no other read is
// in flight. The result arrives as refreshMsg. The place the reader shows does
// not change, and nothing is written.
func (m readerModel) startRefresh() (readerModel, tea.Cmd) {
	if m.cloud == nil || !m.cloud.epub || m.refreshing || m.phase != phaseIdle {
		return m, nil
	}
	m.invalidate()
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.refreshing = true
	m.setNote("Checking Cloud…", "Checking Cloud…",
		"Checking Cloud. The saved position is shown until Cloud answers.", false)
	return m, refreshCmd(ctx, m.cloud, m.cloudTimeout, m.reqID())
}

// reviewCloud is the C key. With a changed Cloud position waiting, it opens the
// conflict prompt, so the user chooses what happens. Otherwise it checks Cloud
// again. It does nothing while a request is in flight or a prompt is open.
func (m readerModel) reviewCloud() (tea.Model, tea.Cmd) {
	if m.phase != phaseIdle || m.cloud == nil || !m.cloud.epub {
		return m, nil
	}
	if m.offer == nil {
		return m.startRefresh()
	}
	cur := *m.offer
	m.offer = nil
	m.conflict = &cur
	m.phase = phaseConflict
	m.note = nil
	m.setStatus(fmt.Sprintf("Cloud changed elsewhere (now %d%%); nothing sent", cur.percent), true)
	return m, nil
}

// leave ends the session on q or esc. The place is saved on this device first.
// Only then is it sent to Cloud. Any Cloud read still in flight is ended first,
// so its result cannot change the session. When Cloud cannot be updated, the
// session ends with a notice that says why.
func (m readerModel) leave() (tea.Model, tea.Cmd) {
	if !m.saveLocal() {
		return m, nil
	}
	m.invalidate()
	m.note = nil
	if !m.prefs.CloudSync {
		return m, leaveCmd(m.bookHash, nil, "Saved locally; Cloud sync is disabled in settings", false)
	}
	if m.cloud == nil {
		return m, leaveCmd(m.bookHash, nil, "", false)
	}
	if !m.cloud.epub {
		return m, leaveCmd(m.bookHash, nil, "Saved on this device only; TXT books are not synced to Cloud", false)
	}
	if m.startExact && m.unmoved() {
		// The place is still the exact Cloud bookmark. Nothing is written. Cloud is
		// read again only to confirm that it still holds that bookmark.
		m.phase = phaseSyncing
		m.setStatus("Checking Cloud…", false)
		return m, confirmCmd(m.cloud, m.cloudTimeout, m.reqID())
	}
	target, ok := m.target()
	switch {
	case !ok:
		return m, leaveCmd(m.bookHash, nil, "Saved on this device only; this passage has no exact EPUB location", true)
	case m.startApprox && m.unmoved():
		return m, leaveCmd(m.bookHash, nil, "Saved on this device only; the Cloud bookmark was not located exactly", true)
	}
	m.phase = phaseSyncing
	m.setStatus("Syncing to Cloud…", false)
	return m, syncCmd(m.cloud, target, m.cloud.baseline, m.cloudTimeout, m.reqID())
}

// onSync applies the result of an attempt to sync. A confirmed sync ends the
// session. A changed Cloud position opens the conflict prompt. A failure opens
// the failed prompt, and the local save stays.
func (m readerModel) onSync(msg syncResultMsg) (tea.Model, tea.Cmd) {
	if m.cloud == nil || msg.bookHash != m.bookHash || msg.req != m.reqID() {
		return m, nil
	}
	m.phase = phaseIdle
	m.note = nil
	m.offer = nil
	switch msg.outcome {
	case syncConfirmed:
		link := *m.cloud
		link.baseline = msg.cloud
		m.cloud = &link
		cloud := msg.cloud
		return m, leaveCmd(m.bookHash, &cloud, fmt.Sprintf("Synced to Cloud: %d%%", cloud.percent), false)
	case syncChanged:
		cur := msg.cloud
		m.conflict = &cur
		m.phase = phaseConflict
		m.setStatus(fmt.Sprintf("Cloud changed elsewhere (now %d%%); nothing sent", cur.percent), true)
	default:
		m.syncErr = msg.err
		m.phase = phaseFailed
		m.setStatus(fmt.Sprintf("Not synced to Cloud: %v", msg.err), true)
	}
	return m, nil
}

// onRefresh applies the background read of Cloud. Only the current request is
// applied, so a read that was cancelled or replaced is ignored. A position that
// matches the baseline confirms it, and the reader keeps its place. A different
// position is only offered: the reader does not move, and the baseline stays as
// it was, so a later write is still checked against Cloud.
func (m readerModel) onRefresh(msg refreshMsg) (tea.Model, tea.Cmd) {
	if m.cloud == nil || msg.req != m.reqID() {
		return m, nil
	}
	m.invalidate()
	if msg.err != nil {
		body, brief, help := refreshFailure(msg.err)
		m.setNote(body, brief, help, true)
		return m, nil
	}
	fresh := msg.cloud
	if fresh.confirms(m.cloud.baseline) {
		link := *m.cloud
		link.baseline = fresh
		m.cloud = &link
		m.offer = nil
		m.note = nil
		m.setStatus("Cloud checked: position unchanged", false)
		return m, nil
	}
	m.offer = &fresh
	m.setNote("Cloud has another position. C:review", "Cloud moved. C:review",
		"Cloud has another position. Nothing was moved or sent. C:review", false)
	return m, nil
}

// refreshFailure says, in short and in full, why Cloud could not be read when a
// session opened. Both say the saved position is still shown, and both name the
// key that retries.
func refreshFailure(err error) (body, brief, help string) {
	if isTimeout(err) {
		return "Cloud timed out. C:retry", "Cloud timed out. C:retry",
			"Cloud timed out. Reading saved position. C:retry"
	}
	return "Cloud unavailable. C:retry", "Cloud error. C:retry",
		fmt.Sprintf("Cloud unavailable (%v). Reading saved position. C:retry", err)
}

// isTimeout reports whether err is a request that ran out of time.
func isTimeout(err error) bool {
	var timeout interface{ Timeout() bool }
	return errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout())
}

// onReload loads a Cloud position read again into the reader and makes it the
// baseline. The place is saved on this device. An unlocatable Cloud position is
// loaded as an estimate from its percentage, and is reported as one.
func (m readerModel) onReload(msg cloudReloadMsg) (tea.Model, tea.Cmd) {
	if m.cloud == nil || msg.bookHash != m.bookHash || msg.req != m.reqID() {
		return m, nil
	}
	if msg.err != nil {
		m.phase = phaseConflict
		m.setStatus(fmt.Sprintf("Could not load the Cloud position: %v", msg.err), true)
		return m, nil
	}

	m.phase = phaseIdle
	m.conflict = nil
	m.offer = nil
	m.note = nil
	m.content = msg.content
	approx := msg.bm.Err != nil
	if approx {
		m.chapterIdx, m.lineOffset = m.content.LocateFraction(msg.cloud.percent)
	} else {
		m.chapterIdx, m.lineOffset = msg.bm.Chapter, msg.bm.LineOffset
	}
	m.clampLineOffset()
	m.withinLineOffset = 0
	m.withinLinePart = 0
	m.ensureLayout()
	link := *m.cloud
	link.baseline = msg.cloud
	m.cloud = &link
	m.startChapter, m.startLine, m.startApprox = m.chapterIdx, m.lineOffset, approx
	m.startWithinLine = m.withinLineOffset
	m.startExact = !approx

	if !m.saveLocal() {
		return m, nil
	}
	if approx {
		m.setStatus(fmt.Sprintf("Cloud bookmark not located exactly; showing about %d%%", msg.cloud.percent), false)
	} else {
		m.setStatus(fmt.Sprintf("Loaded the Cloud position (%d%%)", m.percent()), false)
	}
	return m, nil
}

// promptKey handles the keys of the open Cloud prompt. ok is false when the key
// is not one of them, so the reader handles it as usual. Scrolling keys are
// never prompt keys.
//
//	o  overwrite Cloud with this passage (conflict only; re-checked before send)
//	c  load the Cloud position (conflict only)
//	l  keep the place on this device only, and leave
//	r  retry the sync (failed only)
//	enter  keep reading without sending
func (m readerModel) promptKey(key string) (readerModel, tea.Cmd, bool) {
	inPrompt := m.phase == phaseConflict || m.phase == phaseFailed
	switch {
	case m.phase == phaseConflict && key == "o" && m.conflict != nil:
		if !m.prefs.CloudSync {
			m.setStatus("Cloud sync is disabled. l saves locally", true)
			return m, nil, true
		}
		if !m.saveLocal() {
			return m, nil, true
		}
		target, ok := m.target()
		if !ok {
			m.setStatus("This passage has no exact EPUB location to send", true)
			return m, nil, true
		}
		m.invalidate()
		m.phase = phaseSyncing
		m.setStatus("Overwriting Cloud with this passage…", false)
		return m, syncCmd(m.cloud, target, *m.conflict, m.cloudTimeout, m.reqID()), true

	case m.phase == phaseConflict && key == "c" && m.conflict != nil:
		m.invalidate()
		m.phase = phaseSyncing
		m.setStatus("Loading the Cloud position…", false)
		return m, reloadCmd(m.cloud, *m.conflict, m.reqID()), true

	case inPrompt && key == "l":
		if !m.saveLocal() {
			return m, nil, true
		}
		m.invalidate()
		return m, leaveCmd(m.bookHash, nil, "Saved on this device only; Cloud was not updated", true), true

	case m.phase == phaseFailed && key == "r":
		next, cmd := m.leave()
		return next.(readerModel), cmd, true

	case inPrompt && key == "enter":
		m.phase = phaseIdle
		m.conflict = nil
		m.syncErr = nil
		m.setStatus("Not synced to Cloud; q syncs again", true)
		return m, nil, true
	}
	return m, nil, false
}

// fullPromptWidth is the narrowest reading column that shows the full prompt
// descriptions, the column a 40-column terminal gives.
const fullPromptWidth = 36

// promptTextRows is how many body rows the full prompt leaves for the text.
const promptTextRows = 2

// promptRows lists the open Cloud prompt within width and rows. The full
// prompt is used when the body has room for it plus promptTextRows of text.
// Otherwise a compact prompt keeps every action in view, with no blank row.
// With no prompt open, it lists the Cloud status instead. It is empty when there
// is neither.
func (m readerModel) promptRows(width, rows int) []string {
	var full, compact []string
	switch m.phase {
	case phaseConflict:
		if m.conflict == nil {
			return nil
		}
		full = []string{
			"",
			fmt.Sprintf("Cloud now holds %d%% from another device. Nothing was sent.", m.conflict.percent),
			"  o  overwrite Cloud with this passage",
			"  c  load the Cloud position",
			"  l  keep this place on this device only, then leave",
			"  enter  keep reading without sending",
		}
		compact = []string{
			fmt.Sprintf("Not sent: %d%%", m.conflict.percent),
			"o send c load",
			"l local ↵ read",
		}
	case phaseFailed:
		full = []string{
			"",
			fmt.Sprintf("Cloud sync failed: %v", m.syncErr),
			"  r  retry",
			"  l  keep this place on this device only, then leave",
			"  enter  keep reading without sending",
		}
		compact = []string{
			"Sync failed",
			"r retry l local",
			"↵ keep reading",
		}
	default:
		return m.cloudRows(width)
	}
	lines := compact
	if width >= fullPromptWidth && len(full)+promptTextRows <= rows {
		lines = full
	}
	out := make([]string, 0, len(lines))
	for _, row := range lines {
		out = append(out, mutedStyle.Render(truncate(row, width)))
	}
	return out
}

// cloudRows lists the Cloud status while no prompt is open. The body text is
// used when it fits in cloudNoteRows rows, and the brief text otherwise, so the
// key that acts on the status stays in view.
func (m readerModel) cloudRows(width int) []string {
	if m.note == nil {
		return nil
	}
	rows := wrapText(m.note.body, width)
	if len(rows) > cloudNoteRows {
		rows = wrapText(m.note.brief, width)
	}
	if len(rows) > cloudNoteRows {
		rows = rows[:cloudNoteRows]
	}
	render := mutedStyle.Render
	if m.note.isErr {
		render = errorStyle.Render
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, render(truncate(row, width)))
	}
	return out
}
