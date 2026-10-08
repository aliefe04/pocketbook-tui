package tui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	pbc "github.com/micronull/pocketbook-cloud-client"

	"github.com/aliefe/pocketbook-tui/internal/api"
	"github.com/aliefe/pocketbook-tui/internal/reader"
)

// fakePost is one write that reached the fake Cloud.
type fakePost struct {
	fastHash  string
	offs      int
	pointer   string
	pointerPb string
}

// fakeUpdated is the update time a fake Cloud reports when its position has none.
var fakeUpdated = utcTime("2026-10-08T10:00:00Z")

// fakeCloud stands in for the native reader endpoints of one account. It keeps
// the position that Cloud holds and records every write. Tests use its switches
// to make a remote change, a request failure, or a write that does not stick.
// It listens on the loopback interface only, so no real endpoint is reached.
type fakeCloud struct {
	client *api.Client

	mu        sync.Mutex
	pointer   string
	pointerPb string
	percent   int
	updated   time.Time
	posts     []fakePost
	down      bool // GET and POST answer 503
	failPost  bool // POST answers 500
	dropPost  bool // POST answers OK but Cloud keeps the old position
}

// newFakeCloud serves a Cloud that holds pos. client is wired to it.
func newFakeCloud(t *testing.T, pos pbc.BookReadPosition) *fakeCloud {
	t.Helper()
	f := &fakeCloud{pointer: pos.Pointer, pointerPb: pos.PointerPb, percent: pos.Percent, updated: pos.Updated}
	if f.pointerPb == "" {
		f.pointerPb = f.pointer
	}
	if f.updated.IsZero() {
		f.updated = fakeUpdated
	}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	f.client = api.NewWithNativeTransport(srv.URL+"/", srv.Client())
	return f
}

func (f *fakeCloud) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer token" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if f.down {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/reader/documentInformation":
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","hash":"opaque-resource","position":{"pointer":%q,"pointer_pb":%q,"percent":%d,"updated":%q}}`,
			f.pointer, f.pointerPb, f.percent, f.updated.UTC().Format(time.RFC3339))
	case r.Method == http.MethodPost && r.URL.Path == "/books/read-position":
		var body struct {
			Offs      int    `json:"offs"`
			Pointer   string `json:"pointer"`
			PointerPb string `json:"pointer_pb"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		f.posts = append(f.posts, fakePost{
			fastHash:  r.URL.Query().Get("fast_hash"),
			offs:      body.Offs,
			pointer:   body.Pointer,
			pointerPb: body.PointerPb,
		})
		if f.failPost {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		if !f.dropPost {
			f.pointer, f.pointerPb, f.percent = body.Pointer, body.PointerPb, body.Offs
		}
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "OK")
	default:
		http.NotFound(w, r)
	}
}

// set changes what Cloud holds, as another device would.
func (f *fakeCloud) set(pointer string, percent int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pointer, f.pointerPb, f.percent = pointer, pointer, percent
}

// current returns what Cloud holds.
func (f *fakeCloud) current() (string, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pointer, f.percent
}

// setPointerPb changes only pointer_pb, as a device that rewrites that field alone would.
func (f *fakeCloud) setPointerPb(pointerPb string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pointerPb = pointerPb
}

// state returns the whole native position that Cloud holds.
func (f *fakeCloud) state() pbc.BookReadPosition {
	f.mu.Lock()
	defer f.mu.Unlock()
	return pbc.BookReadPosition{Pointer: f.pointer, PointerPb: f.pointerPb, Percent: f.percent, Updated: f.updated}
}

// postCount returns how many writes reached Cloud.
func (f *fakeCloud) postCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.posts)
}

// lastPost returns the most recent write.
func (f *fakeCloud) lastPost(t *testing.T) fakePost {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.posts) == 0 {
		t.Fatalf("no write reached Cloud")
	}
	return f.posts[len(f.posts)-1]
}

// setFailures sets the POST switches.
func (f *fakeCloud) setFailures(failPost, dropPost bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failPost, f.dropPost = failPost, dropPost
}

// setDown makes every request answer 503, or lets them through again.
func (f *fakeCloud) setDown(down bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.down = down
}

// drive runs each command and feeds its message back to the app, as the
// Bubble Tea runtime does, until no command is left.
func drive(app *App, cmd tea.Cmd) {
	for range 20 {
		if cmd == nil {
			return
		}
		_, cmd = app.Update(cmd())
	}
}

// openInApp opens a book through the app, as the library does. Opening runs the
// fresh Cloud read. The book is listed in the library, so progress can be seen.
func openInApp(t *testing.T, cloud *fakeCloud, book pbc.Book) *App {
	t.Helper()
	app := newTestApp(t)
	app.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	app.Update(booksLoadedMsg{books: pbc.Books{Books: []pbc.Book{book}}})
	msg := openBook(cloud.client, "token", book)
	if msg.Err != nil {
		t.Fatalf("openBook: %v", msg.Err)
	}
	msg.Origin = screenLibrary
	app.Update(msg)
	return app
}

func readerOf(app *App) readerModel {
	return app.reader.(readerModel)
}

// cloudChapter3 and cloudChapter8 are native pointers into cacheDune's books.
const (
	cloudChapter3 = "#epubcfi(/6/6!/4/4/1)"
	cloudChapter8 = "#epubcfi(/6/16!/4/4/1)"
)

func TestOpeningReadsTheCloudPositionFresh(t *testing.T) {
	isolateHome(t)
	// The library snapshot says chapter twelve, but another device has since
	// moved the account to chapter three.
	book := cacheDune(t, cloudAt())
	cloud := newFakeCloud(t, pbc.BookReadPosition{Pointer: cloudChapter3, Percent: 5})

	msg := openBook(cloud.client, "token", book)
	if msg.Err != nil {
		t.Fatalf("openBook: %v", msg.Err)
	}
	if msg.RefreshErr != nil {
		t.Fatalf("fresh read failed: %v", msg.RefreshErr)
	}
	if msg.Cloud == nil || msg.Cloud.chapter != 2 || msg.Cloud.percent != 5 {
		t.Fatalf("Cloud = %+v, want chapter index 2 at 5%%", msg.Cloud)
	}
}

func TestOpeningKeepsTheLastKnownPositionWhenCloudIsUnreachable(t *testing.T) {
	isolateHome(t)
	book := cacheDune(t, cloudAt())
	cloud := newFakeCloud(t, book.ReadPosition)
	cloud.setDown(true)

	app := openInApp(t, cloud, book)
	if app.screen != screenReader {
		t.Fatalf("screen = %v with Cloud unreachable, want reader on the last known place", app.screen)
	}
	if got := readerOf(app).chapterIdx; got != 11 {
		t.Fatalf("chapter = %d, want the last known Cloud chapter 11", got)
	}
	assertContains(t, app.View(), "not refreshed")
}

func TestLeavingSavesLocallyThenSendsAndConfirmsCloud(t *testing.T) {
	isolateHome(t)
	book := cacheDune(t, cloudAt())
	cloud := newFakeCloud(t, book.ReadPosition)
	app := openInApp(t, cloud, book)

	// Move back one chapter, to the heading that starts chapter eleven.
	app.Update(runeKey("p"))
	_, cmd := app.Update(runeKey("q"))
	drive(app, cmd)

	if cloud.postCount() != 1 {
		t.Fatalf("writes = %d, want exactly one", cloud.postCount())
	}
	post := cloud.lastPost(t)
	if post.pointer != "#epubcfi(/6/22!/4/2/1:0)" || post.pointerPb != post.pointer {
		t.Fatalf("posted pointer %q / %q, want the chapter eleven heading CFI in both fields", post.pointer, post.pointerPb)
	}
	if app.screen != screenLibrary {
		t.Fatalf("screen = %v after a confirmed sync, want library", app.screen)
	}
	saved, err := reader.LoadPosition(book.FastHash)
	if err != nil || saved == nil || saved.ChapterIndex != 10 {
		t.Fatalf("local save = %+v, %v; want chapter index 10", saved, err)
	}
	assertContains(t, app.View(), "Synced to Cloud")
	if got := app.library.(libraryModel).fetched[0].ReadPercent; got != post.offs {
		t.Fatalf("library progress = %d%%, want the confirmed %d%%", got, post.offs)
	}
}

func TestLocalSaveFailureSendsNothingToCloud(t *testing.T) {
	home := isolateHome(t)
	book := cacheDune(t, cloudAt())
	cloud := newFakeCloud(t, book.ReadPosition)
	app := openInApp(t, cloud, book)

	// A file where the config directory should be makes the local save fail.
	blocker := filepath.Join(home, ".config", "pocketbook-tui")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	_, cmd := app.Update(runeKey("q"))
	drive(app, cmd)

	if app.screen != screenReader {
		t.Fatalf("screen = %v after a failed local save, want reader", app.screen)
	}
	if cloud.postCount() != 0 {
		t.Fatalf("writes = %d after a failed local save, want none", cloud.postCount())
	}
}

func TestRemoteChangeAfterOpenPreventsOverwriteUntilChosen(t *testing.T) {
	isolateHome(t)
	book := cacheDune(t, cloudAt())
	cloud := newFakeCloud(t, book.ReadPosition)
	app := openInApp(t, cloud, book)

	cloud.set(cloudChapter3, 5)
	_, cmd := app.Update(runeKey("q"))
	drive(app, cmd)

	if cloud.postCount() != 0 {
		t.Fatalf("writes = %d after a remote change, want none", cloud.postCount())
	}
	if readerOf(app).phase != phaseConflict {
		t.Fatalf("phase = %v, want the conflict prompt", readerOf(app).phase)
	}
	assertContains(t, app.View(), "Cloud now holds 5%")

	_, cmd = app.Update(runeKey("o"))
	drive(app, cmd)
	if cloud.postCount() != 1 {
		t.Fatalf("writes = %d after choosing overwrite, want one", cloud.postCount())
	}
	if got := cloud.lastPost(t).pointer; got != "#epubcfi(/6/24!/4/4/1:0)" {
		t.Fatalf("overwrite sent %q, want the current passage", got)
	}
	if app.screen != screenLibrary {
		t.Fatalf("screen = %v after a confirmed overwrite, want library", app.screen)
	}
}

func TestOverwriteRechecksCloudBeforeSending(t *testing.T) {
	isolateHome(t)
	book := cacheDune(t, cloudAt())
	cloud := newFakeCloud(t, book.ReadPosition)
	app := openInApp(t, cloud, book)

	cloud.set(cloudChapter3, 5)
	_, cmd := app.Update(runeKey("q"))
	drive(app, cmd)

	// Another change lands between the prompt and the overwrite.
	cloud.set(cloudChapter8, 9)
	_, cmd = app.Update(runeKey("o"))
	drive(app, cmd)

	if cloud.postCount() != 0 {
		t.Fatalf("writes = %d, want none while Cloud changed again", cloud.postCount())
	}
	if readerOf(app).phase != phaseConflict {
		t.Fatalf("phase = %v, want the conflict prompt again", readerOf(app).phase)
	}
	assertContains(t, app.View(), "Cloud now holds 9%")
}

func TestConflictActionsStayVisibleAtMinimumSize(t *testing.T) {
	isolateHome(t)
	book := cacheDune(t, cloudAt())
	cloud := newFakeCloud(t, book.ReadPosition)
	app := openInApp(t, cloud, book)

	cloud.set(cloudChapter3, 5)
	_, cmd := app.Update(runeKey("q"))
	drive(app, cmd)

	app.Update(tea.WindowSizeMsg{Width: 20, Height: 8})
	assertFits(t, app.View(), 20, 8)
	for _, action := range []string{"o send", "c load", "l local", "↵ read"} {
		assertContains(t, app.View(), action)
	}
}

func TestFailureActionsStayVisibleAtMinimumSize(t *testing.T) {
	isolateHome(t)
	book := cacheDune(t, cloudAt())
	cloud := newFakeCloud(t, book.ReadPosition)
	cloud.setFailures(true, false)
	app := openInApp(t, cloud, book)

	app.Update(runeKey("p"))
	_, cmd := app.Update(runeKey("q"))
	drive(app, cmd)

	app.Update(tea.WindowSizeMsg{Width: 20, Height: 8})
	assertFits(t, app.View(), 20, 8)
	for _, action := range []string{"Sync failed", "r retry", "l local", "↵ keep"} {
		assertContains(t, app.View(), action)
	}
}

func TestLoadingTheCloudPositionSendsNothing(t *testing.T) {
	isolateHome(t)
	book := cacheDune(t, cloudAt())
	cloud := newFakeCloud(t, book.ReadPosition)
	app := openInApp(t, cloud, book)

	cloud.set(cloudChapter3, 5)
	_, cmd := app.Update(runeKey("q"))
	drive(app, cmd)

	_, cmd = app.Update(runeKey("c"))
	drive(app, cmd)

	if cloud.postCount() != 0 {
		t.Fatalf("writes = %d after loading Cloud, want none", cloud.postCount())
	}
	if app.screen != screenReader || readerOf(app).chapterIdx != 2 {
		t.Fatalf("screen = %v chapter = %d, want reader at chapter index 2", app.screen, readerOf(app).chapterIdx)
	}
	saved, err := reader.LoadPosition(book.FastHash)
	if err != nil || saved == nil || saved.ChapterIndex != 2 {
		t.Fatalf("local save = %+v, %v; want the loaded Cloud chapter", saved, err)
	}
}

func TestCloudFailureKeepsLocalSaveAndRetryConfirms(t *testing.T) {
	isolateHome(t)
	book := cacheDune(t, cloudAt())
	cloud := newFakeCloud(t, book.ReadPosition)
	cloud.setFailures(true, false)
	app := openInApp(t, cloud, book)

	app.Update(runeKey("p"))
	_, cmd := app.Update(runeKey("q"))
	drive(app, cmd)

	if app.screen != screenReader || readerOf(app).phase != phaseFailed {
		t.Fatalf("screen = %v phase = %v after a failed write, want reader with the failure prompt", app.screen, readerOf(app).phase)
	}
	assertContains(t, app.View(), "Cloud sync failed")
	saved, err := reader.LoadPosition(book.FastHash)
	if err != nil || saved == nil || saved.ChapterIndex != 10 {
		t.Fatalf("local save = %+v, %v; want the place kept after the failed sync", saved, err)
	}

	cloud.setFailures(false, false)
	_, cmd = app.Update(runeKey("r"))
	drive(app, cmd)
	if app.screen != screenLibrary {
		t.Fatalf("screen = %v after a successful retry, want library", app.screen)
	}
	if pointer, _ := cloud.current(); pointer != "#epubcfi(/6/22!/4/2/1:0)" {
		t.Fatalf("Cloud holds %q after retry, want the saved place", pointer)
	}
}

func TestLeavingLocalOnlyAfterCloudFailureDoesNotRetry(t *testing.T) {
	isolateHome(t)
	book := cacheDune(t, cloudAt())
	cloud := newFakeCloud(t, book.ReadPosition)
	cloud.setFailures(true, false)
	app := openInApp(t, cloud, book)

	// Moving off the Cloud bookmark makes the close write, and that write fails.
	app.Update(runeKey("p"))
	_, cmd := app.Update(runeKey("q"))
	drive(app, cmd)
	_, cmd = app.Update(runeKey("l"))
	drive(app, cmd)

	if app.screen != screenLibrary {
		t.Fatalf("screen = %v after keeping the place locally, want library", app.screen)
	}
	if cloud.postCount() != 1 {
		t.Fatalf("writes = %d, want only the failed attempt", cloud.postCount())
	}
	assertContains(t, app.View(), "Saved on this device only")
}

func TestReadBackMismatchIsNotReportedAsSynced(t *testing.T) {
	isolateHome(t)
	book := cacheDune(t, cloudAt())
	cloud := newFakeCloud(t, book.ReadPosition)
	cloud.setFailures(false, true)
	app := openInApp(t, cloud, book)

	app.Update(runeKey("p"))
	_, cmd := app.Update(runeKey("q"))
	drive(app, cmd)

	if app.screen != screenReader || readerOf(app).phase != phaseFailed {
		t.Fatalf("screen = %v phase = %v after a write Cloud did not keep, want reader with the failure prompt",
			app.screen, readerOf(app).phase)
	}
	assertNotContains(t, app.View(), "Synced to Cloud")
}

func TestChosenLocalBackwardPositionIsSynced(t *testing.T) {
	isolateHome(t)
	book := cacheDune(t, cloudAt())
	saveLocal(t, reader.Position{BookHash: book.FastHash, ChapterIndex: 0, LineOffset: 0, Percent: 1}, time.Date(2026, 10, 8, 1, 0, 0, 0, time.UTC))
	cloud := newFakeCloud(t, book.ReadPosition)

	app := newTestApp(t)
	app.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	msg := openBook(cloud.client, "token", book)
	msg.Origin = screenLibrary
	app.Update(msg)
	if app.screen != screenResume {
		t.Fatalf("screen = %v with differing positions, want the source choice", app.screen)
	}
	app.Update(downKey)
	_, cmd := app.Update(enterKey)
	drive(app, cmd)

	_, cmd = app.Update(runeKey("q"))
	drive(app, cmd)
	if cloud.postCount() != 1 {
		t.Fatalf("writes = %d, want the chosen earlier place sent", cloud.postCount())
	}
	if got := cloud.lastPost(t).pointer; got != "#epubcfi(/6/2!/4/2/1:0)" {
		t.Fatalf("sent %q, want the chapter one heading CFI", got)
	}
}

func TestTXTBooksStayLocal(t *testing.T) {
	isolateHome(t)
	book := pbc.Book{Title: "Notes", Name: "notes.txt", Format: "txt", FastHash: "h-txt"}
	if err := os.MkdirAll(filepath.Dir(bookCachePath(book)), 0o755); err != nil {
		t.Fatalf("create cache dir: %v", err)
	}
	if err := os.WriteFile(bookCachePath(book), []byte("First line\n\nSecond line."), 0o600); err != nil {
		t.Fatalf("write cached book: %v", err)
	}
	cloud := newFakeCloud(t, pbc.BookReadPosition{})
	app := openInApp(t, cloud, book)

	_, cmd := app.Update(runeKey("q"))
	drive(app, cmd)

	if cloud.postCount() != 0 {
		t.Fatalf("writes = %d for a TXT book, want none", cloud.postCount())
	}
	assertContains(t, app.View(), "Saved on this device only")
}

func TestApproximateStartIsNotWrittenWhenUnmoved(t *testing.T) {
	isolateHome(t)
	// A percentage without a pointer places the reader approximately.
	book := cacheDune(t, pbc.BookReadPosition{Percent: 50})
	cloud := newFakeCloud(t, book.ReadPosition)
	app := openInApp(t, cloud, book)
	if app.screen != screenReader {
		t.Fatalf("screen = %v, want reader at the approximate Cloud place", app.screen)
	}

	_, cmd := app.Update(runeKey("q"))
	drive(app, cmd)
	if cloud.postCount() != 0 {
		t.Fatalf("writes = %d for an unmoved approximate start, want none", cloud.postCount())
	}
	assertContains(t, app.View(), "not located exactly")
}

// exactPointer and exactPointerPb are a native bookmark with a character offset,
// and with pointer_pb different from pointer, so a rewrite of either one shows.
const (
	exactPointer   = "#epubcfi(/6/24!/4/4/1:9)"
	exactPointerPb = "#epubcfi(/6/24!/4/4/1:3)"
)

// exactSeed is the account's position for the exact bookmark tests: chapter
// twelve at 81%, last updated before the session began.
func exactSeed() pbc.BookReadPosition {
	return pbc.BookReadPosition{
		Pointer:   exactPointer,
		PointerPb: exactPointerPb,
		Percent:   81,
		Updated:   utcTime("2026-09-29T21:43:49Z"),
	}
}

// assertCloudHolds checks each field of the native position that Cloud holds.
func assertCloudHolds(t *testing.T, cloud *fakeCloud, want pbc.BookReadPosition) {
	t.Helper()
	got := cloud.state()
	if got.Pointer != want.Pointer || got.PointerPb != want.PointerPb ||
		got.Percent != want.Percent || !got.Updated.Equal(want.Updated) {
		t.Fatalf("Cloud holds %+v, want %+v", got, want)
	}
}

// assertLibraryHolds checks the position the library shows for the first book.
func assertLibraryHolds(t *testing.T, app *App, want pbc.BookReadPosition) {
	t.Helper()
	got := app.library.(libraryModel).fetched[0].Position
	if got.Pointer != want.Pointer || got.PointerPb != want.PointerPb ||
		got.Percent != want.Percent || !got.Updated.Equal(want.Updated) {
		t.Fatalf("library holds %+v, want %+v", got, want)
	}
}

func TestExactCloudBookmarkSurvivesOpenAndClose(t *testing.T) {
	isolateHome(t)
	seed := exactSeed()
	book := cacheDune(t, seed)
	cloud := newFakeCloud(t, seed)
	app := openInApp(t, cloud, book)
	if app.screen != screenReader || readerOf(app).chapterIdx != 11 {
		t.Fatalf("screen = %v chapter = %d, want reader at chapter twelve", app.screen, readerOf(app).chapterIdx)
	}

	_, cmd := app.Update(runeKey("q"))
	drive(app, cmd)

	if cloud.postCount() != 0 {
		t.Fatalf("writes = %d for an unmoved Cloud bookmark, want none", cloud.postCount())
	}
	if app.screen != screenLibrary {
		t.Fatalf("screen = %v after an unmoved close, want library", app.screen)
	}
	assertCloudHolds(t, cloud, seed)
	assertLibraryHolds(t, app, seed)
	saved, err := reader.LoadPosition(book.FastHash)
	if err != nil || saved == nil || saved.ChapterIndex != 11 {
		t.Fatalf("local save = %+v, %v; want chapter index 11", saved, err)
	}
}

func TestPointerPbChangeWhileOpenIsAConflict(t *testing.T) {
	isolateHome(t)
	seed := exactSeed()
	book := cacheDune(t, seed)
	cloud := newFakeCloud(t, seed)
	app := openInApp(t, cloud, book)

	// Another device rewrites only pointer_pb. The pointer and percentage match.
	cloud.setPointerPb("#epubcfi(/6/24!/4/4/1:7)")
	_, cmd := app.Update(runeKey("q"))
	drive(app, cmd)

	if cloud.postCount() != 0 {
		t.Fatalf("writes = %d after a pointer_pb change, want none", cloud.postCount())
	}
	if readerOf(app).phase != phaseConflict {
		t.Fatalf("phase = %v, want the conflict prompt", readerOf(app).phase)
	}
	assertContains(t, app.View(), "Cloud now holds 81%")
}

func TestMovedReaderDetectsPointerPbChangeWithoutWriting(t *testing.T) {
	isolateHome(t)
	seed := exactSeed()
	book := cacheDune(t, seed)
	cloud := newFakeCloud(t, seed)
	app := openInApp(t, cloud, book)

	// The reader moves on. Another device then rewrites only pointer_pb.
	app.Update(runeKey("n"))
	const remotePointerPb = "#epubcfi(/6/24!/4/4/1:7)"
	cloud.setPointerPb(remotePointerPb)
	_, cmd := app.Update(runeKey("q"))
	drive(app, cmd)

	if cloud.postCount() != 0 {
		t.Fatalf("writes = %d after a pointer_pb change while the reader moved, want none", cloud.postCount())
	}
	if readerOf(app).phase != phaseConflict {
		t.Fatalf("phase = %v, want the conflict prompt", readerOf(app).phase)
	}
	want := seed
	want.PointerPb = remotePointerPb
	assertCloudHolds(t, cloud, want)
	if c := readerOf(app).conflict; c == nil || c.pointerPb != remotePointerPb || c.percent != seed.Percent {
		t.Fatalf("conflict = %+v, want the full Cloud state with the new pointer_pb", c)
	}
	assertContains(t, app.View(), "Cloud now holds 81%")

	// Choosing to overwrite still sends the moved place, once.
	_, cmd = app.Update(runeKey("o"))
	drive(app, cmd)
	if cloud.postCount() != 1 {
		t.Fatalf("writes = %d after choosing overwrite, want one", cloud.postCount())
	}
	if got := cloud.lastPost(t).pointer; got == exactPointer {
		t.Fatalf("overwrite sent the Cloud pointer %q, want the moved passage", got)
	}
}

func TestStaleOverwriteRefusesLaterPointerPbChange(t *testing.T) {
	isolateHome(t)
	seed := exactSeed()
	book := cacheDune(t, seed)
	cloud := newFakeCloud(t, seed)
	app := openInApp(t, cloud, book)

	app.Update(runeKey("n"))
	cloud.setPointerPb("#epubcfi(/6/24!/4/4/1:7)")
	_, cmd := app.Update(runeKey("q"))
	drive(app, cmd)
	if readerOf(app).phase != phaseConflict {
		t.Fatalf("phase = %v, want the conflict prompt", readerOf(app).phase)
	}

	// A further pointer_pb change lands after the prompt. The overwrite was
	// chosen against the earlier state, so it must not be sent.
	const laterPointerPb = "#epubcfi(/6/24!/4/4/1:8)"
	cloud.setPointerPb(laterPointerPb)
	_, cmd = app.Update(runeKey("o"))
	drive(app, cmd)

	if cloud.postCount() != 0 {
		t.Fatalf("writes = %d after a later pointer_pb change, want none", cloud.postCount())
	}
	if readerOf(app).phase != phaseConflict {
		t.Fatalf("phase = %v, want the conflict prompt again", readerOf(app).phase)
	}
	want := seed
	want.PointerPb = laterPointerPb
	assertCloudHolds(t, cloud, want)
	assertContains(t, app.View(), "Cloud now holds 81%")
}

func TestMatchingLocalSaveKeepsTheCloudBookmark(t *testing.T) {
	isolateHome(t)
	seed := exactSeed()
	book := cacheDune(t, seed)
	cloud := newFakeCloud(t, seed)

	// The local save sits on the same line as the Cloud bookmark.
	first := openBook(cloud.client, "token", book)
	if first.Err != nil || first.Cloud == nil {
		t.Fatalf("openBook: err %v, cloud %v", first.Err, first.Cloud)
	}
	saveLocal(t, reader.Position{BookHash: book.FastHash, ChapterIndex: first.Cloud.chapter, LineOffset: first.Cloud.lineOffset, Percent: 81}, utcTime("2026-10-08T01:00:00Z"))

	app := openInApp(t, cloud, book)
	if app.screen != screenReader {
		t.Fatalf("screen = %v for matching positions, want reader without a choice", app.screen)
	}
	_, cmd := app.Update(runeKey("q"))
	drive(app, cmd)

	if cloud.postCount() != 0 {
		t.Fatalf("writes = %d for matching positions, want none", cloud.postCount())
	}
	assertCloudHolds(t, cloud, seed)
}

func TestLoadingTheCloudBookmarkThenLeavingKeepsIt(t *testing.T) {
	isolateHome(t)
	seed := exactSeed()
	book := cacheDune(t, seed)
	cloud := newFakeCloud(t, seed)
	app := openInApp(t, cloud, book)

	// Another device moves the account to chapter three. Leaving shows the conflict.
	cloud.set(cloudChapter3, 5)
	_, cmd := app.Update(runeKey("q"))
	drive(app, cmd)
	_, cmd = app.Update(runeKey("c"))
	drive(app, cmd)
	if app.screen != screenReader || readerOf(app).chapterIdx != 2 {
		t.Fatalf("screen = %v chapter = %d, want reader at chapter index 2", app.screen, readerOf(app).chapterIdx)
	}

	_, cmd = app.Update(runeKey("q"))
	drive(app, cmd)
	if cloud.postCount() != 0 {
		t.Fatalf("writes = %d after loading and leaving the Cloud bookmark, want none", cloud.postCount())
	}
	if app.screen != screenLibrary {
		t.Fatalf("screen = %v after an unmoved close, want library", app.screen)
	}
	want := pbc.BookReadPosition{Pointer: cloudChapter3, PointerPb: cloudChapter3, Percent: 5, Updated: seed.Updated}
	assertCloudHolds(t, cloud, want)
	assertLibraryHolds(t, app, want)
}
