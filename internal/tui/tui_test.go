package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	pbc "github.com/micronull/pocketbook-cloud-client"

	"github.com/aliefe/pocketbook-tui/internal/api"
	"github.com/aliefe/pocketbook-tui/internal/config"
	"github.com/aliefe/pocketbook-tui/internal/reader"
)

// isolateHome points HOME at a temporary directory, so config and position
// writes never touch the real home directory.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func runeKey(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func pasteKey(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s), Paste: true}
}

var (
	backspaceKey = tea.KeyMsg{Type: tea.KeyBackspace}
	enterKey     = tea.KeyMsg{Type: tea.KeyEnter}
	downKey      = tea.KeyMsg{Type: tea.KeyDown}
)

// send delivers messages in order. Returned commands are not run, so no
// network request is made.
func send(m tea.Model, msgs ...tea.Msg) tea.Model {
	for _, msg := range msgs {
		m, _ = m.Update(msg)
	}
	return m
}

// runCmd runs a command and delivers its message, as the Bubble Tea runtime
// does. Use it only for commands that do not touch the network.
func runCmd(m tea.Model, cmd tea.Cmd) tea.Model {
	if cmd == nil {
		return m
	}
	updated, _ := m.Update(cmd())
	return updated
}

func plainRows(view string) []string {
	return strings.Split(ansi.Strip(view), "\n")
}

func assertContains(t *testing.T, view, want string) {
	t.Helper()
	if plain := ansi.Strip(view); !strings.Contains(plain, want) {
		t.Fatalf("view does not contain %q:\n%s", want, plain)
	}
}

func assertNotContains(t *testing.T, view, unwanted string) {
	t.Helper()
	if plain := ansi.Strip(view); strings.Contains(plain, unwanted) {
		t.Fatalf("view contains %q but should not:\n%s", unwanted, plain)
	}
}

// assertFits checks that a view stays inside width x height terminal cells.
func assertFits(t *testing.T, view string, width, height int) {
	t.Helper()
	rows := strings.Split(view, "\n")
	if len(rows) > height {
		t.Fatalf("view has %d rows, terminal has %d:\n%s", len(rows), height, ansi.Strip(view))
	}
	for i, row := range rows {
		if w := lipgloss.Width(row); w > width {
			t.Fatalf("row %d is %d cells wide, terminal is %d: %q", i, w, width, ansi.Strip(row))
		}
	}
}

// assertSelected checks that the selection marker sits on the row for title.
func assertSelected(t *testing.T, view, title string) {
	t.Helper()
	for _, row := range plainRows(view) {
		if strings.Contains(row, "›") && strings.Contains(row, title) {
			return
		}
	}
	t.Fatalf("selected row %q is not visible:\n%s", title, ansi.Strip(view))
}

func testBooks(titles ...string) []pbc.Book {
	books := make([]pbc.Book, 0, len(titles))
	for _, title := range titles {
		books = append(books, pbc.Book{Title: title, Format: "epub", FastHash: "hash-" + title})
	}
	return books
}

func testContent() *reader.BookContent {
	return &reader.BookContent{Chapters: []reader.Chapter{
		{Title: "Opening", Lines: []string{"It was a bright cold day in April.", "The clocks were striking thirteen."}},
		{Title: "Second", Lines: []string{"Winston walked on."}},
	}}
}

func TestLibraryFilterEditsWholeUnicodeCharacters(t *testing.T) {
	isolateHome(t)
	cfg := &config.Config{Token: "token"}
	m := newLibraryModel(api.New(), cfg)
	m = send(m,
		tea.WindowSizeMsg{Width: 80, Height: 24},
		booksLoadedMsg{books: pbc.Books{Books: testBooks("Über Wald", "Dune", "Ärger")}},
	).(libraryModel)

	m = send(m, runeKey("/"), runeKey("ü")).(libraryModel)
	assertContains(t, m.View(), "Über Wald")
	assertNotContains(t, m.View(), "Dune")

	m = send(m, runeKey("x")).(libraryModel)
	assertNotContains(t, m.View(), "Über Wald")
	assertNotContains(t, m.View(), "Dune")
	assertNotContains(t, m.View(), "Ärger")

	// Backspace removes the whole "x", and then the whole two-byte "ü".
	m = send(m, backspaceKey).(libraryModel)
	assertContains(t, m.View(), "Über Wald")
	m = send(m, backspaceKey).(libraryModel)
	assertContains(t, m.View(), "Dune")
	assertContains(t, m.View(), "Ärger")

	// Pasted text goes into the filter as well.
	m = send(m, pasteKey("wald")).(libraryModel)
	assertContains(t, m.View(), "Über Wald")
	assertNotContains(t, m.View(), "Dune")
}

func TestLibraryFilterSurvivesRefresh(t *testing.T) {
	isolateHome(t)
	m := newLibraryModel(api.New(), &config.Config{Token: "token"})
	m = send(m,
		tea.WindowSizeMsg{Width: 80, Height: 24},
		booksLoadedMsg{books: pbc.Books{Books: testBooks("Über Wald", "Dune")}},
		runeKey("/"), pasteKey("wald"), enterKey,
		runeKey("R"),
		booksLoadedMsg{books: pbc.Books{Books: testBooks("Über Wald", "Dune", "Wald Notes")}},
	).(libraryModel)

	assertContains(t, m.View(), "Wald Notes")
	assertContains(t, m.View(), "Über Wald")
	assertNotContains(t, m.View(), "Dune")
}

func TestLibrarySelectionStaysVisibleAfterResize(t *testing.T) {
	isolateHome(t)
	var titles []string
	for i := range 30 {
		titles = append(titles, fmt.Sprintf("Book %02d", i))
	}
	m := newLibraryModel(api.New(), &config.Config{Token: "token"})
	m = send(m,
		tea.WindowSizeMsg{Width: 80, Height: 24},
		booksLoadedMsg{books: pbc.Books{Books: testBooks(titles...)}},
	).(libraryModel)
	for range 20 {
		m = send(m, downKey).(libraryModel)
	}
	assertSelected(t, m.View(), "Book 20")

	for _, size := range []struct{ w, h int }{{40, 8}, {40, 14}, {80, 24}} {
		m = send(m, tea.WindowSizeMsg{Width: size.w, Height: size.h}).(libraryModel)
		assertSelected(t, m.View(), "Book 20")
		assertFits(t, m.View(), size.w, size.h)
	}
}

func TestScreensFitTerminalAcrossTransitions(t *testing.T) {
	for _, size := range []struct{ w, h int }{{20, 8}, {40, 14}, {80, 24}} {
		t.Run(fmt.Sprintf("%dx%d", size.w, size.h), func(t *testing.T) {
			isolateHome(t)
			if err := (&config.Config{Token: "token"}).Save(); err != nil {
				t.Fatalf("save config: %v", err)
			}
			app, err := NewApp()
			if err != nil {
				t.Fatalf("NewApp: %v", err)
			}
			app.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})

			assertFits(t, app.View(), size.w, size.h)

			long := []pbc.Book{
				{
					Title:       "Ein sehr langer deutscher Titel über Ökonomie und Gesellschaft im 21. Jahrhundert",
					Format:      "epub",
					FastHash:    "hash-long",
					Bytes:       2048000,
					ReadPercent: 42,
					Favorite:    true,
					MetaData:    pbc.BookMetaData{Authors: "Jürgen Müller-Großmann, 山田太郎"},
				},
				{
					Title:       "日本語のタイトル：とても長い名前の本です",
					Format:      "txt",
					FastHash:    "hash-jp",
					IsAudioBook: true,
					MetaData:    pbc.BookMetaData{Authors: "山田太郎"},
				},
			}
			app.Update(booksLoadedMsg{books: pbc.Books{Books: long}})
			assertFits(t, app.View(), size.w, size.h)

			// A new screen must lay out at the current size without a resize event.
			app.Update(ShowDetailMsg{Book: long[0]})
			assertFits(t, app.View(), size.w, size.h)

			app.Update(BackToLibraryMsg{})
			app.Update(OpenBookMsg{Content: testContent(), BookHash: "hash-open", BookTitle: long[0].Title})
			assertFits(t, app.View(), size.w, size.h)
			if app.screen != screenReader {
				t.Fatalf("screen = %v after a successful open, want reader", app.screen)
			}

			app.Update(UnauthorizedMsg{})
			assertFits(t, app.View(), size.w, size.h)
			if app.screen != screenLogin {
				t.Fatalf("screen = %v after an unauthorized response, want login", app.screen)
			}

			app.Update(providersMsg{providers: []pbc.Provider{
				{Name: "Verlag der Düsseldorfer Bücherfreunde und Leseratten Gesellschaft", Alias: "a"},
				{Name: "Provider 二", Alias: "b"},
				{Name: "Third", Alias: "c"},
				{Name: "Fourth", Alias: "d"},
				{Name: "Fifth", Alias: "e"},
			}})
			assertFits(t, app.View(), size.w, size.h)

			app.Update(loginErrMsg{err: errors.New("the provider rejected the request because the account is locked and a long explanation follows")})
			assertFits(t, app.View(), size.w, size.h)
		})
	}
}

// openFailureApp shows the library with a PDF first and an EPUB second. openBook
// rejects the PDF before any download, so the open fails without a network request.
func openFailureApp(t *testing.T) *App {
	t.Helper()
	isolateHome(t)
	cfg := &config.Config{Token: "token"}
	client := api.New()
	app := &App{screen: screenLibrary, client: client, cfg: cfg}
	app.library = newLibraryModel(client, cfg)
	app.Update(tea.WindowSizeMsg{Width: 40, Height: 14})
	app.Update(booksLoadedMsg{books: pbc.Books{Books: []pbc.Book{
		{Title: "Manual", Format: "pdf", FastHash: "hash-manual"},
		{Title: "Dune", Format: "epub", FastHash: "hash-dune"},
	}}})
	return app
}

func TestFailedOpenWaitsForTheLibraryWhileDetailsAreShown(t *testing.T) {
	app := openFailureApp(t)

	_, openCmd := app.Update(runeKey("r"))
	_, showCmd := app.Update(enterKey)
	runCmd(app, showCmd)
	if app.screen != screenDetail {
		t.Fatalf("screen = %v after opening details, want details", app.screen)
	}
	before := app.View()

	runCmd(app, openCmd)
	if app.screen != screenDetail {
		t.Fatalf("screen = %v after a failed open from the library, want details", app.screen)
	}
	if after := app.View(); after != before {
		t.Fatalf("details changed when a library open failed:\n%s", ansi.Strip(after))
	}

	_, backCmd := app.Update(tea.KeyMsg{Type: tea.KeyEsc})
	runCmd(app, backCmd)
	if app.screen != screenLibrary {
		t.Fatalf("screen = %v after going back, want library", app.screen)
	}
	assertContains(t, app.View(), "unsupported format: pdf")
	assertFits(t, app.View(), 40, 14)
}

func TestFailedOpenFromDetailsShowsOnThatDetailsScreen(t *testing.T) {
	app := openFailureApp(t)

	_, showCmd := app.Update(enterKey)
	runCmd(app, showCmd)
	_, openCmd := app.Update(runeKey("r"))
	runCmd(app, openCmd)
	if app.screen != screenDetail {
		t.Fatalf("screen = %v after a failed open from details, want details", app.screen)
	}
	assertContains(t, app.View(), "unsupported format: pdf")
	assertFits(t, app.View(), 40, 14)
}

func TestFailedOpenIsNotShownOnAnotherBooksDetails(t *testing.T) {
	app := openFailureApp(t)

	_, showCmd := app.Update(enterKey)
	runCmd(app, showCmd)
	_, openCmd := app.Update(runeKey("r"))
	_, backCmd := app.Update(tea.KeyMsg{Type: tea.KeyEsc})
	runCmd(app, backCmd)
	app.Update(downKey)
	_, showCmd = app.Update(enterKey)
	runCmd(app, showCmd)

	// The PDF open started from the first book's details fails after the user moved on.
	runCmd(app, openCmd)
	if app.screen != screenDetail {
		t.Fatalf("screen = %v after a late failed open, want details", app.screen)
	}
	assertContains(t, app.View(), "Dune")
	assertNotContains(t, app.View(), "unsupported format")
	assertFits(t, app.View(), 40, 14)
}

func TestReaderStaysOpenWhenPositionSaveFails(t *testing.T) {
	home := isolateHome(t)
	// A file where the config directory should be makes every position save fail.
	blocker := filepath.Join(home, ".config", "pocketbook-tui")
	if err := os.MkdirAll(filepath.Dir(blocker), 0o755); err != nil {
		t.Fatalf("create .config: %v", err)
	}
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}

	cfg := &config.Config{Token: "token"}
	client := api.New()
	app := &App{screen: screenLibrary, client: client, cfg: cfg}
	app.library = newLibraryModel(client, cfg)
	// A wide terminal keeps the whole save error on one row.
	app.Update(tea.WindowSizeMsg{Width: 240, Height: 24})
	app.Update(OpenBookMsg{Origin: screenLibrary, Content: testContent(), BookHash: "hash-save", BookTitle: "Test Book"})
	if app.screen != screenReader {
		t.Fatalf("screen = %v after opening a book, want reader", app.screen)
	}

	// Move down two lines so the saved position is not the start of the book.
	app.Update(runeKey("j"))
	app.Update(runeKey("j"))
	moved := app.reader.(readerModel)
	want := moved.position()
	if want.LineOffset == 0 {
		t.Fatalf("reader did not move, so the position under test is the default")
	}

	_, cmd := app.Update(runeKey("q"))
	runCmd(app, cmd)
	if app.screen != screenReader {
		t.Fatalf("screen = %v after a failed save, want reader", app.screen)
	}
	assertContains(t, app.View(), blocker)

	if err := os.Remove(blocker); err != nil {
		t.Fatalf("remove blocker: %v", err)
	}
	_, cmd = app.Update(runeKey("q"))
	runCmd(app, cmd)
	if app.screen != screenLibrary {
		t.Fatalf("screen = %v after a successful save, want library", app.screen)
	}
	saved, err := reader.LoadPosition("hash-save")
	if err != nil {
		t.Fatalf("load saved position: %v", err)
	}
	if saved.ChapterIndex != want.ChapterIndex || saved.LineOffset != want.LineOffset {
		t.Fatalf("saved chapter %d line %d, want chapter %d line %d",
			saved.ChapterIndex, saved.LineOffset, want.ChapterIndex, want.LineOffset)
	}
}

func TestCachedBookOpensWithExistingLocalPosition(t *testing.T) {
	home := isolateHome(t)
	book := pbc.Book{Title: "Notes", Name: "notes.txt", Format: "txt", FastHash: "hash-txt"}

	want := filepath.Join(home, ".config", "pocketbook", "notes.txt")
	if got := bookCachePath(book); got != want {
		t.Fatalf("cache path = %q, want %q", got, want)
	}

	if err := os.MkdirAll(filepath.Dir(want), 0o755); err != nil {
		t.Fatalf("create cache dir: %v", err)
	}
	text := "First line\n\nSecond paragraph of the notes.\n\nThird paragraph."
	if err := os.WriteFile(want, []byte(text), 0o600); err != nil {
		t.Fatalf("write cached book: %v", err)
	}
	if err := reader.SavePosition(&reader.Position{BookHash: book.FastHash, ChapterIndex: 0, LineOffset: 2, Percent: 10}); err != nil {
		t.Fatalf("save position: %v", err)
	}

	// The book is cached, so opening it must not download anything.
	msg := openBook(api.New(), "token", book)
	if msg.Err != nil {
		t.Fatalf("openBook: %v", msg.Err)
	}
	if msg.Content == nil || msg.Content.TotalLines() == 0 {
		t.Fatalf("openBook returned no readable content")
	}
	if msg.Position == nil || msg.Position.LineOffset != 2 {
		t.Fatalf("restored position = %+v, want line offset 2", msg.Position)
	}
}

func TestRefusedBooksAreRejectedBeforeDownload(t *testing.T) {
	isolateHome(t)
	// The link is unreachable. A rejection that tries to download would fail
	// with a "download:" error and miss the wanted message.
	const unreachable = "http://127.0.0.1:1/never"
	cases := []struct {
		name string
		book pbc.Book
		want string
	}{
		{"drm", pbc.Book{Name: "locked.epub", Format: "epub", IsDrm: true, Link: unreachable}, "DRM-protected"},
		{"lcp", pbc.Book{Name: "locked.epub", Format: "epub", IsLcp: true, Link: unreachable}, "DRM-protected"},
		{"pdf", pbc.Book{Name: "doc.pdf", Format: "pdf", Link: unreachable}, "unsupported format: pdf"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := openBook(api.New(), "token", tc.book)
			if msg.Err == nil || !strings.Contains(msg.Err.Error(), tc.want) {
				t.Fatalf("openBook error = %v, want it to contain %q", msg.Err, tc.want)
			}
			if _, err := os.Stat(bookCachePath(tc.book)); !os.IsNotExist(err) {
				t.Fatalf("refused book was written to the cache: %v", err)
			}
		})
	}
}

func TestDetailMetadataScrollsIntoViewInShortTerminal(t *testing.T) {
	isolateHome(t)
	book := pbc.Book{
		Title:       "Dune",
		Format:      "epub",
		FastHash:    "hash-dune",
		Bytes:       2048000,
		ReadPercent: 42,
		Favorite:    true,
		MetaData: pbc.BookMetaData{
			Authors:   "Frank Herbert",
			Publisher: "Lektorat Press",
			Year:      2024,
			Isbn:      "9783161484100",
			Lang:      "en",
		},
	}
	m := send(newDetailModel(book, api.New(), &config.Config{Token: "token"}),
		tea.WindowSizeMsg{Width: 40, Height: 14},
	).(detailModel)
	assertFits(t, m.View(), 40, 14)
	assertNotContains(t, m.View(), "Lektorat Press")

	for range 10 {
		m = send(m, downKey).(detailModel)
	}
	assertContains(t, m.View(), "Lektorat Press")
	assertContains(t, m.View(), "9783161484100")
	assertContains(t, m.View(), "2024")
	assertFits(t, m.View(), 40, 14)

	m = send(m, tea.KeyMsg{Type: tea.KeyPgUp}).(detailModel)
	assertContains(t, m.View(), "Dune")
	assertNotContains(t, m.View(), "Lektorat Press")
	assertFits(t, m.View(), 40, 14)

	// A taller terminal leaves no room to scroll, so the offset must clamp to the top.
	for range 10 {
		m = send(m, runeKey("j")).(detailModel)
	}
	m = send(m, tea.WindowSizeMsg{Width: 40, Height: 24}).(detailModel)
	assertContains(t, m.View(), "Dune")
	assertContains(t, m.View(), "Lektorat Press")
	assertFits(t, m.View(), 40, 24)
}
