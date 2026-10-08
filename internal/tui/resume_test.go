package tui

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	pbc "github.com/micronull/pocketbook-cloud-client"

	"github.com/aliefe/pocketbook-tui/internal/api"
	"github.com/aliefe/pocketbook-tui/internal/config"
	"github.com/aliefe/pocketbook-tui/internal/reader"
)

// utcTime parses an RFC 3339 timestamp for a fixture.
func utcTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

// makeEPUB builds a minimal EPUB whose spine lists the idrefs in order. An idref
// with no doc is in the spine but not the manifest, so the reader skips it.
func makeEPUB(t *testing.T, docs map[string]string, spine []string) []byte {
	t.Helper()
	files := map[string]string{
		"META-INF/container.xml": `<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`,
	}
	var manifest, spineXML strings.Builder
	for id, doc := range docs {
		files["OEBPS/"+id+".xhtml"] = doc
		fmt.Fprintf(&manifest, `<item id="%s" href="%s.xhtml" media-type="application/xhtml+xml"/>`, id, id)
	}
	for _, idref := range spine {
		fmt.Fprintf(&spineXML, `<itemref idref="%s"/>`, idref)
	}
	files["OEBPS/content.opf"] = `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0"><metadata/><manifest>` +
		manifest.String() + `</manifest><spine>` + spineXML.String() + `</spine></package>`

	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

// cacheDune caches a twelve-chapter EPUB in the isolated book cache and returns
// the book with the given Cloud read position. Each chapter has a heading and
// one paragraph, "Paragraph N.", as its second body element.
func cacheDune(t *testing.T, readPosition pbc.BookReadPosition) pbc.Book {
	t.Helper()
	docs := map[string]string{}
	var spine []string
	for n := 1; n <= 12; n++ {
		id := fmt.Sprintf("c%02d", n)
		docs[id] = fmt.Sprintf(`<html><head><title>Chapter %d</title></head><body><h1>Chapter %d</h1><p>Paragraph %d.</p></body></html>`, n, n, n)
		spine = append(spine, id)
	}
	book := pbc.Book{
		Title:        "Dune",
		Name:         "dune.epub",
		Format:       "epub",
		FastHash:     "h-dune",
		ReadStatus:   "reading",
		ReadPosition: readPosition,
	}
	path := bookCachePath(book)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create cache dir: %v", err)
	}
	if err := os.WriteFile(path, makeEPUB(t, docs, spine), 0o600); err != nil {
		t.Fatalf("write cached book: %v", err)
	}
	return book
}

// cloudAt is the Cloud read position used by most tests: 31%, at the paragraph
// of chapter twelve.
func cloudAt() pbc.BookReadPosition {
	return pbc.BookReadPosition{
		Pointer: "#epubcfi(/6/24!/4/4/1)",
		Percent: 31,
		Updated: utcTime("2026-09-29T21:43:49Z"),
	}
}

// saveLocal saves a local position and sets its file time, so recency and the
// source choice see the save time the test chose. It returns the file path.
func saveLocal(t *testing.T, pos reader.Position, savedAt time.Time) string {
	t.Helper()
	if err := reader.SavePosition(&pos); err != nil {
		t.Fatalf("save position: %v", err)
	}
	path := filepath.Join(reader.PositionDir(), pos.BookHash+".json")
	if err := os.Chtimes(path, savedAt, savedAt); err != nil {
		t.Fatalf("set position time: %v", err)
	}
	return path
}

// assertRowOrder checks that each title appears in view, one below the other,
// in the given order.
func assertRowOrder(t *testing.T, view string, titles ...string) {
	t.Helper()
	rows := plainRows(view)
	last := -1
	for _, title := range titles {
		idx := -1
		for i, row := range rows {
			if strings.Contains(row, title) {
				idx = i
				break
			}
		}
		if idx < 0 {
			t.Fatalf("view does not show %q:\n%s", title, strings.Join(rows, "\n"))
		}
		if idx <= last {
			t.Fatalf("%q is not below the previous title:\n%s", title, strings.Join(rows, "\n"))
		}
		last = idx
	}
}

// newTestApp returns an app on the library at 120x24 with a token, ready to
// open books.
func newTestApp(t *testing.T) *App {
	t.Helper()
	cfg := &config.Config{Token: "token"}
	client := api.New()
	app := &App{screen: screenLibrary, client: client, cfg: cfg}
	app.library = newLibraryModel(client, cfg)
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	return app
}

func TestLibraryOrdersByRecentReadingNotAPIOrder(t *testing.T) {
	isolateHome(t)
	books := []pbc.Book{
		{Title: "Never opened", Format: "epub", FastHash: "h-never", ReadStatus: "unread"},
		{
			// Unread: its initialized timestamp is not reading activity.
			Title: "Fresh upload", Format: "epub", FastHash: "h-fresh", ReadStatus: "unread",
			Position: pbc.BookPosition{Updated: utcTime("2026-10-07T09:00:00Z")},
		},
		{
			Title: "Latest read", Format: "epub", FastHash: "h-latest", ReadStatus: "read",
			ReadPosition: pbc.BookReadPosition{Pointer: "#epubcfi(/6/24!/4/4/1)", Percent: 31, Updated: utcTime("2026-09-29T21:43:49Z")},
		},
		{
			Title: "Older read", Format: "epub", FastHash: "h-older", ReadStatus: "reading",
			Position: pbc.BookPosition{Percent: 40, Updated: utcTime("2026-03-01T00:00:00Z")},
		},
	}
	// The local save of "Older read" is newer than the Cloud read of "Latest read".
	saveLocal(t, reader.Position{BookHash: "h-older", ChapterIndex: 0, LineOffset: 1, Percent: 10}, utcTime("2026-09-30T12:00:00Z"))

	m := send(newLibraryModel(api.New(), &config.Config{Token: "token"}),
		tea.WindowSizeMsg{Width: 80, Height: 24},
		booksLoadedMsg{books: pbc.Books{Books: books}},
	).(libraryModel)

	assertRowOrder(t, m.View(), "Older read", "Latest read", "Never opened", "Fresh upload")
}

func TestRefreshKeepsSelectedBook(t *testing.T) {
	isolateHome(t)
	m := send(newLibraryModel(api.New(), &config.Config{Token: "token"}),
		tea.WindowSizeMsg{Width: 80, Height: 24},
		booksLoadedMsg{books: pbc.Books{Books: testBooks("Alpha", "Beta", "Gamma")}},
		downKey,
	).(libraryModel)
	assertSelected(t, m.View(), "Beta")

	// A new book at the top moves Beta down; the selection must follow it.
	m = send(m, booksLoadedMsg{books: pbc.Books{Books: testBooks("New", "Alpha", "Beta", "Gamma")}}).(libraryModel)
	assertSelected(t, m.View(), "Beta")
}

func TestReturningFromReaderReordersAfterLocalSave(t *testing.T) {
	isolateHome(t)
	m := send(newLibraryModel(api.New(), &config.Config{Token: "token"}),
		tea.WindowSizeMsg{Width: 80, Height: 24},
		booksLoadedMsg{books: pbc.Books{Books: testBooks("Alpha", "Beta")}},
		downKey,
	).(libraryModel)
	assertRowOrder(t, m.View(), "Alpha", "Beta")

	saveLocal(t, reader.Position{BookHash: "hash-Beta", ChapterIndex: 0, LineOffset: 1, Percent: 10}, utcTime("2026-10-08T01:00:00Z"))
	m = send(m, BackToLibraryMsg{}).(libraryModel)

	assertRowOrder(t, m.View(), "Beta", "Alpha")
	assertSelected(t, m.View(), "Beta")
}

func TestCloudBookmarkOpensAtContainingParagraph(t *testing.T) {
	isolateHome(t)
	book := cacheDune(t, cloudAt())

	msg := openBook(newFakeCloud(t, book.ReadPosition).client, "token", book)
	if msg.Err != nil {
		t.Fatalf("openBook: %v", msg.Err)
	}
	app := newTestApp(t)
	app.Update(msg)

	if app.screen != screenReader {
		t.Fatalf("screen = %v with no local save, want reader", app.screen)
	}
	assertContains(t, app.View(), "Ch 12/12")
	assertContains(t, app.View(), "Paragraph 12.")
	assertFits(t, app.View(), 120, 24)
}

func TestCloudBookmarkFallbacks(t *testing.T) {
	cases := []struct {
		name string
		read pbc.BookReadPosition
		want string
	}{
		{
			name: "approximate from percent when the pointer does not resolve",
			read: pbc.BookReadPosition{Pointer: "#epubcfi(/6/24!/4/98)", Percent: 31, Updated: utcTime("2026-09-29T21:43:49Z")},
			want: "not located exactly",
		},
		{
			name: "unlocatable without a percent is shown instead of starting silently",
			read: pbc.BookReadPosition{Pointer: "#epubcfi(/6/24!/4/98)", Updated: utcTime("2026-09-29T21:43:49Z")},
			want: "starting at the beginning",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateHome(t)
			book := cacheDune(t, tc.read)
			msg := openBook(newFakeCloud(t, book.ReadPosition).client, "token", book)
			if msg.Err != nil {
				t.Fatalf("openBook: %v", msg.Err)
			}
			app := newTestApp(t)
			app.Update(msg)
			if app.screen != screenReader {
				t.Fatalf("screen = %v, want reader", app.screen)
			}
			assertContains(t, app.View(), tc.want)
		})
	}
}

func TestDifferingPositionsAskWithCloudSelected(t *testing.T) {
	isolateHome(t)
	book := cacheDune(t, cloudAt())
	path := saveLocal(t, reader.Position{BookHash: book.FastHash, ChapterIndex: 0, LineOffset: 0, Percent: 7}, utcTime("2026-10-08T01:00:00Z"))
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read local position: %v", err)
	}

	msg := openBook(newFakeCloud(t, book.ReadPosition).client, "token", book)
	msg.Origin = screenLibrary
	app := newTestApp(t)
	app.Update(msg)
	if app.screen != screenResume {
		t.Fatalf("screen = %v with differing positions, want the source choice", app.screen)
	}
	assertContains(t, app.View(), "31%")
	assertContains(t, app.View(), "7%")
	assertSelected(t, app.View(), "Cloud")

	// Enter opens the selected Cloud position.
	_, cmd := app.Update(enterKey)
	runCmd(app, cmd)
	if app.screen != screenReader {
		t.Fatalf("screen = %v after choosing Cloud, want reader", app.screen)
	}
	assertContains(t, app.View(), "Paragraph 12.")

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read local position: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("opening the book changed the local position file")
	}
}

func TestChoosingLocalKeepsBackwardsReading(t *testing.T) {
	isolateHome(t)
	book := cacheDune(t, cloudAt())
	saveLocal(t, reader.Position{BookHash: book.FastHash, ChapterIndex: 0, LineOffset: 0, Percent: 7}, utcTime("2026-10-08T01:00:00Z"))

	msg := openBook(newFakeCloud(t, book.ReadPosition).client, "token", book)
	msg.Origin = screenLibrary
	app := newTestApp(t)
	app.Update(msg)

	app.Update(downKey)
	_, cmd := app.Update(enterKey)
	runCmd(app, cmd)
	if app.screen != screenReader {
		t.Fatalf("screen = %v after choosing local, want reader", app.screen)
	}
	assertContains(t, app.View(), "Ch 1/12")
	assertNotContains(t, app.View(), "Paragraph 12.")
}

func TestCancellingTheChoiceOpensNothingAndReturnsToOrigin(t *testing.T) {
	isolateHome(t)
	book := cacheDune(t, cloudAt())
	path := saveLocal(t, reader.Position{BookHash: book.FastHash, ChapterIndex: 0, LineOffset: 0, Percent: 7}, utcTime("2026-10-08T01:00:00Z"))
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read local position: %v", err)
	}

	t.Run("from the library", func(t *testing.T) {
		msg := openBook(newFakeCloud(t, book.ReadPosition).client, "token", book)
		msg.Origin = screenLibrary
		app := newTestApp(t)
		app.Update(msg)

		_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyEsc})
		runCmd(app, cmd)
		if app.screen != screenLibrary {
			t.Fatalf("screen = %v after cancelling from the library, want library", app.screen)
		}
	})

	t.Run("from the details", func(t *testing.T) {
		msg := openBook(newFakeCloud(t, book.ReadPosition).client, "token", book)
		msg.Origin = screenDetail
		app := newTestApp(t)
		app.Update(ShowDetailMsg{Book: book})
		app.Update(msg)

		_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyEsc})
		runCmd(app, cmd)
		if app.screen != screenDetail {
			t.Fatalf("screen = %v after cancelling from details, want details", app.screen)
		}
	})

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read local position: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("cancelling changed the local position file")
	}
}

func TestMatchingPositionsSkipTheChoice(t *testing.T) {
	isolateHome(t)
	book := cacheDune(t, cloudAt())

	// Find where the Cloud bookmark lands, then save a local position there.
	first := openBook(newFakeCloud(t, book.ReadPosition).client, "token", book)
	if first.Err != nil || first.Cloud == nil {
		t.Fatalf("openBook: err %v, cloud %v", first.Err, first.Cloud)
	}
	saveLocal(t, reader.Position{
		BookHash:     book.FastHash,
		ChapterIndex: first.Cloud.chapter,
		LineOffset:   first.Cloud.lineOffset,
		Percent:      31,
	}, utcTime("2026-10-08T01:00:00Z"))

	msg := openBook(newFakeCloud(t, book.ReadPosition).client, "token", book)
	app := newTestApp(t)
	app.Update(msg)
	if app.screen != screenReader {
		t.Fatalf("screen = %v for matching positions, want reader without a choice", app.screen)
	}
}
