package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	pbc "github.com/micronull/pocketbook-cloud-client"

	"github.com/aliefe/pocketbook-tui/internal/api"
	"github.com/aliefe/pocketbook-tui/internal/reader"
)

// cloudLink is what a reading session needs to keep its book's Cloud position
// in step with the local save. openBook attaches it. baseline is the Cloud state
// the session last confirmed, or was shown. A write is sent only while Cloud
// still holds that state.
type cloudLink struct {
	client   *api.Client
	token    string
	bookHash string
	epub     bool           // only EPUB books have an exact place to write to Cloud
	baseline nativeProgress // Cloud state that the session last confirmed or was shown
	// reparse reads the cached book again and places pointer in it. It is used
	// to load the Cloud position into the reader.
	reparse func(pointer string) (*reader.BookContent, reader.Bookmark, error)
}

// cloudTarget is the position that a session wants Cloud to hold.
type cloudTarget struct {
	pointer string
	percent int
}

// syncOutcome is the result of one attempt to bring Cloud to a target.
type syncOutcome int

const (
	// syncConfirmed means Cloud holds the target, as a read-back shows.
	syncConfirmed syncOutcome = iota
	// syncChanged means Cloud holds another position. Nothing was sent after
	// the check that found it.
	syncChanged
	// syncFailed means a request failed, or the read-back did not match.
	syncFailed
)

// requestID names the request a reading session started. A result is applied
// only while its request is still the session's current one. A late result from
// an earlier session of the same book, or from a request that has since been
// replaced or cancelled, is therefore ignored.
type requestID struct {
	session uint64 // the reading session that started the request
	gen     uint64 // the request within that session
}

// syncResultMsg reports one attempt to bring Cloud to a target.
type syncResultMsg struct {
	req      requestID
	bookHash string
	outcome  syncOutcome
	cloud    nativeProgress // the Cloud state as it was last read
	err      error
}

// runSync brings Cloud to target. It reads Cloud first. When Cloud already
// holds the target, nothing is sent. When Cloud holds neither the target nor
// expect, it changed elsewhere, and nothing is sent. Otherwise it sends the
// target and reads it back. Only a read-back that matches counts as synced. The
// check narrows the race between devices, but the API offers no compare-and-set,
// so the race is not closed. Each native request is bounded by timeout. There is
// no retry loop.
func runSync(client *api.Client, token, bookHash string, target cloudTarget, expect nativeProgress, timeout time.Duration) syncResultMsg {
	fail := func(err error) syncResultMsg {
		return syncResultMsg{bookHash: bookHash, outcome: syncFailed, err: err}
	}
	changed := func(cur nativeProgress) syncResultMsg {
		return syncResultMsg{bookHash: bookHash, outcome: syncChanged, cloud: cur}
	}
	confirmed := func(cur nativeProgress) syncResultMsg {
		return syncResultMsg{bookHash: bookHash, outcome: syncConfirmed, cloud: cur}
	}

	cur, err := readCloud(context.Background(), timeout, client, token, bookHash)
	if err != nil {
		return fail(err)
	}
	switch {
	case cur.holds(target):
		return confirmed(cur)
	case !cur.confirms(expect):
		return changed(cur)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	err = client.SaveNativePosition(ctx, token, bookHash, target.percent, target.pointer)
	cancel()
	if err != nil {
		return fail(err)
	}
	got, err := readCloud(context.Background(), timeout, client, token, bookHash)
	if err != nil {
		return fail(fmt.Errorf("could not confirm the Cloud position: %w", err))
	}
	switch {
	case got.holds(target):
		return confirmed(got)
	case got.confirms(expect):
		return fail(errors.New("Cloud did not keep the position"))
	}
	return changed(got)
}

// readCloud reads the account's current position for a book. The read is
// bounded by timeout, and ends early when parent is cancelled.
func readCloud(parent context.Context, timeout time.Duration, client *api.Client, token, bookHash string) (nativeProgress, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	pos, err := client.NativePosition(ctx, token, bookHash)
	if err != nil {
		return nativeProgress{}, err
	}
	return progressFromNative(pos), nil
}

// progressFromNative converts a native read into the snapshot the UI compares.
func progressFromNative(p api.NativePosition) nativeProgress {
	return nativeProgress{
		pointer:   p.Pointer,
		pointerPb: p.PointerPb,
		percent:   p.Percent,
		updated:   p.Updated,
	}
}

// normalizePointer drops the leading "#" that the native reader uses, so that
// the same CFI compares equal whether or not it carries it.
func normalizePointer(s string) string {
	return strings.TrimPrefix(strings.TrimSpace(s), "#")
}

// holds reports whether the snapshot is exactly the target, in both pointers and
// in the percentage.
func (p nativeProgress) holds(t cloudTarget) bool {
	return normalizePointer(p.pointer) == normalizePointer(t.pointer) &&
		normalizePointer(p.pointerPb) == normalizePointer(t.pointer) &&
		p.percent == t.percent
}

// sameAs reports whether the snapshot is the same bookmark as other: the same
// pointer, the same pointer_pb, and the same percentage. The timestamp is not
// compared, because the account changes it on every write, including writes
// that leave the position unchanged.
func (p nativeProgress) sameAs(other nativeProgress) bool {
	return normalizePointer(p.pointer) == normalizePointer(other.pointer) &&
		normalizePointer(p.pointerPb) == normalizePointer(other.pointerPb) &&
		p.percent == other.percent
}

// confirms reports whether p, a fresh read of Cloud, is the state base describes.
// A complete base, one that carries pointer_pb, needs sameAs. A base without
// pointer_pb was not read from a full snapshot, so that field cannot be compared.
// Such a base is confirmed when the pointer and percentage match it and p's own
// pointer_pb is the same CFI as p's pointer. A pointer_pb that differs from the
// pointer is still a change.
func (p nativeProgress) confirms(base nativeProgress) bool {
	if base.pointerPb != "" {
		return p.sameAs(base)
	}
	return normalizePointer(p.pointer) == normalizePointer(base.pointer) &&
		p.percent == base.percent &&
		normalizePointer(p.pointerPb) == normalizePointer(p.pointer)
}

// syncCmd runs runSync for a link in the background. The result carries req.
func syncCmd(link *cloudLink, target cloudTarget, expect nativeProgress, timeout time.Duration, req requestID) tea.Cmd {
	client, token, hash := link.client, link.token, link.bookHash
	return func() tea.Msg {
		msg := runSync(client, token, hash, target, expect, timeout)
		msg.req = req
		return msg
	}
}

// confirmCmd reads Cloud once, in the background, for a session that leaves its
// exact Cloud bookmark without moving. Nothing is sent. See runConfirm. The
// result carries req.
func confirmCmd(link *cloudLink, timeout time.Duration, req requestID) tea.Cmd {
	client, token, hash, base := link.client, link.token, link.bookHash, link.baseline
	return func() tea.Msg {
		msg := runConfirm(client, token, hash, base, timeout)
		msg.req = req
		return msg
	}
}

// runConfirm checks, without writing, that Cloud still holds base. Its pointers,
// percentage, and timestamp are kept as base has them, so the bookmark is never
// regenerated from the reader's line. When Cloud holds anything else, nothing is
// sent, and the change is reported as a conflict.
func runConfirm(client *api.Client, token, bookHash string, base nativeProgress, timeout time.Duration) syncResultMsg {
	cur, err := readCloud(context.Background(), timeout, client, token, bookHash)
	switch {
	case err != nil:
		return syncResultMsg{bookHash: bookHash, outcome: syncFailed, err: err}
	case cur.confirms(base):
		// A base without pointer_pb takes the pointer_pb that Cloud just verified.
		if base.pointerPb == "" {
			base.pointerPb = cur.pointerPb
		}
		return syncResultMsg{bookHash: bookHash, outcome: syncConfirmed, cloud: base}
	}
	return syncResultMsg{bookHash: bookHash, outcome: syncChanged, cloud: cur}
}

// cloudReloadMsg carries the Cloud position read again into the book, for the
// reader to load. req identifies the request that asked for it.
type cloudReloadMsg struct {
	req      requestID
	bookHash string
	cloud    nativeProgress
	content  *reader.BookContent
	bm       reader.Bookmark
	err      error
}

// reloadCmd places the Cloud position in the cached book, in the background.
func reloadCmd(link *cloudLink, cloud nativeProgress, req requestID) tea.Cmd {
	reparse, hash := link.reparse, link.bookHash
	return func() tea.Msg {
		content, bm, err := reparse(cloud.pointer)
		return cloudReloadMsg{req: req, bookHash: hash, cloud: cloud, content: content, bm: bm, err: err}
	}
}

// refreshMsg reports the background read of Cloud that a reading session starts
// when it opens. err is set when the read failed; otherwise cloud is the position
// Cloud holds now.
type refreshMsg struct {
	req   requestID
	cloud nativeProgress
	err   error
}

// refreshCmd reads Cloud once, in the background. ctx belongs to the session, so
// leaving the session, or starting another request, cancels the read. The read
// is bounded by timeout. Nothing is written.
func refreshCmd(ctx context.Context, link *cloudLink, timeout time.Duration, req requestID) tea.Cmd {
	client, token, hash := link.client, link.token, link.bookHash
	return func() tea.Msg {
		cloud, err := readCloud(ctx, timeout, client, token, hash)
		return refreshMsg{req: req, cloud: cloud, err: err}
	}
}

// readerLeftMsg ends a reading session and returns to the library. confirmed is
// the Cloud state that the session confirmed, or nil when Cloud was not
// changed. notice says how the book was saved, and noticeErr marks a warning.
type readerLeftMsg struct {
	bookHash  string
	confirmed *nativeProgress
	notice    string
	noticeErr bool
}

// leaveCmd sends the readerLeftMsg for a session that is ending.
func leaveCmd(bookHash string, confirmed *nativeProgress, notice string, noticeErr bool) tea.Cmd {
	return func() tea.Msg {
		return readerLeftMsg{bookHash: bookHash, confirmed: confirmed, notice: notice, noticeErr: noticeErr}
	}
}

// applyProgress records a confirmed Cloud state on a book. The position fields
// are set together, so the newer one is the confirmed state. The percentage
// shown in the library and details is updated too.
func applyProgress(book *pbc.Book, p nativeProgress) {
	book.Position = pbc.BookPosition{
		Pointer:   p.pointer,
		PointerPb: p.pointerPb,
		Percent:   p.percent,
		Updated:   p.updated,
	}
	book.ReadPosition = pbc.BookReadPosition{
		Pointer:   p.pointer,
		PointerPb: p.pointerPb,
		Percent:   p.percent,
		Updated:   p.updated,
	}
	book.ReadPercent = p.percent
	if book.ReadStatus == "unread" {
		book.ReadStatus = "reading"
	}
}

// applyConfirmed records a confirmed Cloud state on the listed book with the
// given hash.
func (m *libraryModel) applyConfirmed(bookHash string, p nativeProgress) {
	for i := range m.fetched {
		if m.fetched[i].FastHash == bookHash {
			applyProgress(&m.fetched[i], p)
		}
	}
}
