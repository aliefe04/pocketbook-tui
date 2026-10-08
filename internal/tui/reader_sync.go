package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/aliefe/pocketbook-tui/internal/reader"
)

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
	return m.chapterIdx == m.startChapter && m.lineOffset == m.startLine
}

// leave ends the session on q or esc. The place is saved on this device first.
// Only then is it sent to Cloud. When Cloud cannot be updated, the session ends
// with a notice that says why.
func (m readerModel) leave() (tea.Model, tea.Cmd) {
	if !m.saveLocal() {
		return m, nil
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
		return m, confirmCmd(m.cloud)
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
	return m, syncCmd(m.cloud, target, m.cloud.baseline)
}

// onSync applies the result of an attempt to sync. A confirmed sync ends the
// session. A changed Cloud position opens the conflict prompt. A failure opens
// the failed prompt, and the local save stays.
func (m readerModel) onSync(msg syncResultMsg) (tea.Model, tea.Cmd) {
	if m.cloud == nil || msg.bookHash != m.bookHash {
		return m, nil
	}
	m.phase = phaseIdle
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

// onReload loads a Cloud position read again into the reader and makes it the
// baseline. The place is saved on this device. An unlocatable Cloud position is
// loaded as an estimate from its percentage, and is reported as one.
func (m readerModel) onReload(msg cloudReloadMsg) (tea.Model, tea.Cmd) {
	if m.cloud == nil || msg.bookHash != m.bookHash {
		return m, nil
	}
	if msg.err != nil {
		m.phase = phaseConflict
		m.setStatus(fmt.Sprintf("Could not load the Cloud position: %v", msg.err), true)
		return m, nil
	}

	m.phase = phaseIdle
	m.conflict = nil
	m.content = msg.content
	approx := msg.bm.Err != nil
	if approx {
		m.chapterIdx, m.lineOffset = m.content.LocateFraction(msg.cloud.percent)
	} else {
		m.chapterIdx, m.lineOffset = msg.bm.Chapter, msg.bm.LineOffset
	}
	m.clampLineOffset()
	link := *m.cloud
	link.baseline = msg.cloud
	m.cloud = &link
	m.startChapter, m.startLine, m.startApprox = m.chapterIdx, m.lineOffset, approx
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
		if !m.saveLocal() {
			return m, nil, true
		}
		target, ok := m.target()
		if !ok {
			m.setStatus("This passage has no exact EPUB location to send", true)
			return m, nil, true
		}
		m.phase = phaseSyncing
		m.setStatus("Overwriting Cloud with this passage…", false)
		return m, syncCmd(m.cloud, target, *m.conflict), true

	case m.phase == phaseConflict && key == "c" && m.conflict != nil:
		m.phase = phaseSyncing
		m.setStatus("Loading the Cloud position…", false)
		return m, reloadCmd(m.cloud, *m.conflict), true

	case inPrompt && key == "l":
		if !m.saveLocal() {
			return m, nil, true
		}
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
// It is empty when no prompt is open.
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
		return nil
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
