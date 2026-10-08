package tui

import (
	"context"
	"errors"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// shortCloudTimeout is the deadline for a Cloud read against a stalled fixture.
// The fixture never answers, so the read ends only when this deadline passes.
const shortCloudTimeout = 50 * time.Millisecond

// openStalled opens a book through an app whose window is 100x30. Its Cloud
// read is started but not run, so a test controls when it completes. Callers
// stall Cloud with cloud.stall before calling it and release the stall
// themselves.
func openStalled(t *testing.T, cloud *fakeCloud, open tea.Msg) (*App, tea.Cmd) {
	t.Helper()
	app := newTestApp(t)
	app.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	_, refresh := app.Update(open)
	if app.screen != screenReader {
		t.Fatalf("screen = %v, want the reader", app.screen)
	}
	return app, refresh
}

func TestCachedBookOpensWhileCloudReadStalls(t *testing.T) {
	isolateHome(t)
	book := cacheDune(t, cloudAt())
	cloud := newFakeCloud(t, book.ReadPosition)
	release := cloud.stall()
	t.Cleanup(release)

	opened := make(chan OpenBookMsg, 1)
	go func() { opened <- openBook(cloud.client, "token", book) }()
	var msg OpenBookMsg
	select {
	case msg = <-opened:
	case <-time.After(5 * time.Second):
		t.Fatal("opening the cached book waited for the stalled Cloud read")
	}
	if msg.Err != nil {
		t.Fatalf("openBook: %v", msg.Err)
	}

	msg.Origin = screenLibrary
	app, refresh := openStalled(t, cloud, msg)
	if refresh == nil {
		t.Fatal("the reader did not start a Cloud check")
	}
	assertContains(t, app.View(), "Checking Cloud")

	// The reader takes keys while the read is pending.
	before := readerOf(app).chapterIdx
	app.Update(runeKey("p"))
	if got := readerOf(app).chapterIdx; got != before-1 {
		t.Fatalf("chapter = %d while Cloud is pending, want %d", got, before-1)
	}

	// Once the read finds the same position, the reader keeps its place.
	result := make(chan tea.Msg, 1)
	go func() { result <- refresh() }()
	release()
	app.Update(<-result)
	r := readerOf(app)
	if r.refreshing || r.note != nil {
		t.Fatalf("Cloud check still pending after an unchanged read: note %+v", r.note)
	}
	if r.chapterIdx != before-1 {
		t.Fatalf("chapter = %d after an unchanged read, want the reader's %d", r.chapterIdx, before-1)
	}
	if cloud.postCount() != 0 {
		t.Fatalf("writes = %d after a refresh, want none", cloud.postCount())
	}
}

func TestCloudTimeoutLeavesTheReaderUsable(t *testing.T) {
	isolateHome(t)
	book := cacheDune(t, cloudAt())
	cloud := newFakeCloud(t, book.ReadPosition)
	release := cloud.stall()
	t.Cleanup(release)

	msg := openBook(cloud.client, "token", book)
	if msg.Err != nil {
		t.Fatalf("openBook: %v", msg.Err)
	}
	msg.Origin = screenLibrary
	app := newTestApp(t)
	app.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	r := newReaderSession(msg, msg.Cloud, 100, 30)
	r.cloudTimeout = shortCloudTimeout
	_, cmd := app.showReader(r)
	drive(app, cmd)

	if app.screen != screenReader || readerOf(app).phase != phaseIdle {
		t.Fatalf("screen = %v phase = %v after a timeout, want idle reader", app.screen, readerOf(app).phase)
	}
	assertContains(t, app.View(), "Cloud timed out. C:retry")
	assertNotContains(t, app.View(), "deadline")

	// The full status is in help.
	app.Update(runeKey("?"))
	assertContains(t, app.View(), "Cloud timed out. Reading saved position. C:retry")
	app.Update(runeKey("?"))

	// Reading continues on the saved place.
	before := readerOf(app).chapterIdx
	app.Update(runeKey("p"))
	if got := readerOf(app).chapterIdx; got != before-1 {
		t.Fatalf("chapter = %d after a timeout, want %d", got, before-1)
	}
	if cloud.postCount() != 0 {
		t.Fatalf("writes = %d after a timeout, want none", cloud.postCount())
	}

	// The recovery action stays in view at each supported size.
	for _, size := range []struct{ w, h int }{{20, 8}, {40, 14}, {80, 24}} {
		app.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
		assertFits(t, app.View(), size.w, size.h)
		assertContains(t, app.View(), "C:retry")
	}
}

func TestCheckingCloudAgainRecoversAfterATimeout(t *testing.T) {
	isolateHome(t)
	book := cacheDune(t, cloudAt())
	cloud := newFakeCloud(t, book.ReadPosition)
	release := cloud.stall()
	t.Cleanup(release)

	msg := openBook(cloud.client, "token", book)
	if msg.Err != nil {
		t.Fatalf("openBook: %v", msg.Err)
	}
	msg.Origin = screenLibrary
	app := newTestApp(t)
	app.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	r := newReaderSession(msg, msg.Cloud, 100, 30)
	r.cloudTimeout = shortCloudTimeout
	_, cmd := app.showReader(r)
	drive(app, cmd)
	assertContains(t, app.View(), "C:retry")

	// Cloud answers again, and C checks it.
	release()
	_, cmd = app.Update(runeKey("C"))
	if cmd == nil {
		t.Fatal("C did not start a Cloud check after a timeout")
	}
	drive(app, cmd)

	if readerOf(app).note != nil {
		t.Fatalf("status still shows a failure after a successful check: %+v", readerOf(app).note)
	}
	assertContains(t, app.View(), "Cloud checked")
	if cloud.postCount() != 0 {
		t.Fatalf("writes = %d after a check, want none", cloud.postCount())
	}
}

func TestChangedCloudPositionIsOfferedAndNotApplied(t *testing.T) {
	isolateHome(t)
	book := cacheDune(t, cloudAt())
	cloud := newFakeCloud(t, book.ReadPosition)
	release := cloud.stall()
	t.Cleanup(release)

	msg := openBook(cloud.client, "token", book)
	if msg.Err != nil {
		t.Fatalf("openBook: %v", msg.Err)
	}
	msg.Origin = screenLibrary
	app, refresh := openStalled(t, cloud, msg)

	// The reader moves on while the read is pending. Cloud then changes.
	app.Update(runeKey("p"))
	moved := readerOf(app).chapterIdx
	cloud.set(cloudChapter3, 5)
	result := make(chan tea.Msg, 1)
	go func() { result <- refresh() }()
	release()
	app.Update(<-result)

	r := readerOf(app)
	if r.chapterIdx != moved {
		t.Fatalf("chapter = %d after a changed Cloud position, want the reader's %d", r.chapterIdx, moved)
	}
	if r.offer == nil || r.offer.percent != 5 {
		t.Fatalf("offer = %+v, want Cloud's 5%%", r.offer)
	}
	if r.cloud.baseline.percent != 31 {
		t.Fatalf("baseline = %d%%, want the saved 31%% until the user chooses", r.cloud.baseline.percent)
	}
	assertContains(t, app.View(), "Cloud has another position. C:review")

	// C opens the review, which sends nothing.
	app.Update(runeKey("C"))
	if readerOf(app).phase != phaseConflict {
		t.Fatalf("phase = %v after C, want the conflict prompt", readerOf(app).phase)
	}
	assertContains(t, app.View(), "Cloud now holds 5%")

	// Loading the offered position moves the reader there, once asked.
	_, cmd := app.Update(runeKey("c"))
	drive(app, cmd)
	if got := readerOf(app).chapterIdx; got != 2 {
		t.Fatalf("chapter = %d after loading Cloud, want chapter index 2", got)
	}
	if cloud.postCount() != 0 {
		t.Fatalf("writes = %d after loading Cloud, want none", cloud.postCount())
	}
}

func TestLateResultFromAnEarlierSessionIsIgnored(t *testing.T) {
	isolateHome(t)
	book := cacheDune(t, cloudAt())
	cloud := newFakeCloud(t, book.ReadPosition)
	release := cloud.stall()
	t.Cleanup(release)

	msg := openBook(cloud.client, "token", book)
	if msg.Err != nil {
		t.Fatalf("openBook: %v", msg.Err)
	}
	msg.Origin = screenLibrary
	app, firstRefresh := openStalled(t, cloud, msg)

	// The same book is opened again. That replaces the first session.
	_, secondRefresh := app.showReader(newReaderSession(msg, msg.Cloud, 100, 30))

	// The first session's read was cancelled, and its late result changes nothing.
	cloud.set(cloudChapter3, 5)
	stale, ok := firstRefresh().(refreshMsg)
	if !ok || !errors.Is(stale.err, context.Canceled) {
		t.Fatalf("first session's read = %+v, want it cancelled", stale)
	}
	app.Update(stale)
	if r := readerOf(app); r.offer != nil || r.note == nil || r.note.body != "Checking Cloud…" {
		t.Fatalf("a stale result changed the current session: offer %+v note %+v", r.offer, r.note)
	}

	// The current session's read is applied.
	release()
	app.Update(secondRefresh())
	if r := readerOf(app); r.offer == nil || r.offer.percent != 5 {
		t.Fatalf("offer = %+v, want the current session's changed position", r.offer)
	}
	if cloud.postCount() != 0 {
		t.Fatalf("writes = %d, want none", cloud.postCount())
	}
}

func TestResultFromAReplacedRequestIsIgnored(t *testing.T) {
	isolateHome(t)
	book := cacheDune(t, cloudAt())
	cloud := newFakeCloud(t, book.ReadPosition)
	release := cloud.stall()
	t.Cleanup(release)

	msg := openBook(cloud.client, "token", book)
	if msg.Err != nil {
		t.Fatalf("openBook: %v", msg.Err)
	}
	msg.Origin = screenLibrary
	app, refresh := openStalled(t, cloud, msg)

	// Leaving at the exact Cloud bookmark starts a confirming read. Cloud changes.
	cloud.set(cloudChapter3, 5)
	_, confirm := app.Update(runeKey("q"))

	// The read the reader started before leaving is cancelled. Its result must
	// not reach the confirming request.
	stale, ok := refresh().(refreshMsg)
	if !ok || !errors.Is(stale.err, context.Canceled) {
		t.Fatalf("earlier read = %+v, want it cancelled by leaving", stale)
	}
	app.Update(stale)
	if r := readerOf(app); r.phase != phaseSyncing || r.offer != nil {
		t.Fatalf("phase = %v offer = %+v after a stale result, want the confirming read still pending", r.phase, r.offer)
	}

	// The confirming read finds a change, and the prompt opens without a write.
	release()
	drive(app, confirm)
	if readerOf(app).phase != phaseConflict {
		t.Fatalf("phase = %v after the confirming read, want the conflict prompt", readerOf(app).phase)
	}
	assertContains(t, app.View(), "Cloud now holds 5%")
	if cloud.postCount() != 0 {
		t.Fatalf("writes = %d, want none", cloud.postCount())
	}
}

func TestConfirmsTreatsAMissingPointerPbAsUnknownNotAsAChange(t *testing.T) {
	const pointer = "#epubcfi(/6/24!/4/4/1)"
	const otherPb = "#epubcfi(/6/24!/4/4/1:7)"
	cases := []struct {
		name  string
		fresh nativeProgress
		base  nativeProgress
		want  bool
	}{
		{
			name:  "cached without pointer_pb, fresh consistent",
			fresh: nativeProgress{pointer: pointer, pointerPb: pointer, percent: 31},
			base:  nativeProgress{pointer: pointer, percent: 31},
			want:  true,
		},
		{
			name:  "cached without pointer_pb, fresh pointer_pb differs from its pointer",
			fresh: nativeProgress{pointer: pointer, pointerPb: otherPb, percent: 31},
			base:  nativeProgress{pointer: pointer, percent: 31},
			want:  false,
		},
		{
			name:  "cached without pointer_pb, fresh percentage differs",
			fresh: nativeProgress{pointer: pointer, pointerPb: pointer, percent: 32},
			base:  nativeProgress{pointer: pointer, percent: 31},
			want:  false,
		},
		{
			name:  "cached without pointer_pb, fresh pointer differs",
			fresh: nativeProgress{pointer: otherPb, pointerPb: otherPb, percent: 31},
			base:  nativeProgress{pointer: pointer, percent: 31},
			want:  false,
		},
		{
			name:  "complete baseline, same pointer_pb",
			fresh: nativeProgress{pointer: pointer, pointerPb: pointer, percent: 31},
			base:  nativeProgress{pointer: pointer, pointerPb: pointer, percent: 31},
			want:  true,
		},
		{
			name:  "complete baseline, pointer_pb-only change is a change",
			fresh: nativeProgress{pointer: pointer, pointerPb: otherPb, percent: 31},
			base:  nativeProgress{pointer: pointer, pointerPb: pointer, percent: 31},
			want:  false,
		},
	}
	for _, c := range cases {
		if got := c.fresh.confirms(c.base); got != c.want {
			t.Errorf("%s: confirms = %v, want %v", c.name, got, c.want)
		}
	}
}
