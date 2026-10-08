package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aliefe/pocketbook-tui/internal/config"
	"github.com/aliefe/pocketbook-tui/internal/reader"
	tea "github.com/charmbracelet/bubbletea"
	pbc "github.com/micronull/pocketbook-cloud-client"
)

func settingIndex(field string) int {
	for i, o := range settingsOptions {
		if o.field == field {
			return i
		}
	}
	panic("unknown setting")
}
func settingsApp(t *testing.T) *App {
	t.Helper()
	isolateHome(t)
	app := newTestApp(t)
	m := numberedReader()
	app.reader = m
	app.screen = screenReader
	app.Update(tea.WindowSizeMsg{Width: 44, Height: 14})
	return app
}
func TestSettingsCancelKeepsPassageAndDoesNotPersist(t *testing.T) {
	app := settingsApp(t)
	before := numberedWord.FindAllString(app.View(), -1)
	app.Update(runeKey("S"))
	m := app.settings.(settingsModel)
	m.cursor = settingIndex("ReadingWidth")
	app.settings = m
	app.Update(tea.KeyMsg{Type: tea.KeyRight})
	_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyEsc})
	runCmd(app, cmd)
	if !reflect.DeepEqual(numberedWord.FindAllString(app.View(), -1), before) {
		t.Fatal("discarded settings changed the reading passage")
	}
	if _, err := os.Stat(config.PreferencesPath()); !os.IsNotExist(err) {
		t.Fatalf("cancel wrote preferences: %v", err)
	}
}
func TestSettingsSaveReflowsWithoutLosingSourcePassage(t *testing.T) {
	app := settingsApp(t)
	m := app.reader.(readerModel)
	m.pageDown()
	app.reader = m
	word := numberedWord.FindString(app.View())
	app.Update(runeKey("S"))
	s := app.settings.(settingsModel)
	s.cursor = settingIndex("ReadingWidth")
	app.settings = s
	app.Update(tea.KeyMsg{Type: tea.KeyRight})
	_, cmd := app.Update(runeKey("s"))
	runCmd(app, cmd)
	assertContains(t, app.View(), word)
	prefs, err := config.LoadPreferences()
	if err != nil || prefs.ReadingWidth != 20 {
		t.Fatalf("saved width = %d: %v", prefs.ReadingWidth, err)
	}
	assertFits(t, app.View(), 44, 14)
}
func TestFailedSettingsSaveKeepsDraftOpen(t *testing.T) {
	app := settingsApp(t)
	if err := os.MkdirAll(filepath.Dir(config.ConfigDir()), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.ConfigDir(), []byte("blocker"), 0600); err != nil {
		t.Fatal(err)
	}
	app.Update(runeKey("S"))
	_, cmd := app.Update(runeKey("s"))
	if cmd != nil || app.screen != screenSettings || app.settings.(settingsModel).err == nil {
		t.Fatal("failed settings save claimed success or closed the draft")
	}
}
func TestSettingsRecoveryActionsFitMinimumTerminal(t *testing.T) {
	for _, size := range [][2]int{{20, 8}, {40, 14}, {80, 24}} {
		m := settingsModel{draft: config.DefaultPreferences(), width: size[0], height: size[1], cursor: settingIndex("CloudSync")}
		assertFits(t, m.View(), size[0], size[1])
		assertContains(t, m.View(), "s save")
		assertContains(t, m.View(), "esc cancel")
	}
}
func TestConfiguredSpacingPagesKeepCompleteWordCoverage(t *testing.T) {
	m := numberedReader()
	p := config.DefaultPreferences()
	p.ReadingWidth = 30
	p.LineSpacing = 2
	p.ParagraphSpacing = 2
	p.ShowHeader = false
	p.ShowFooter = false
	p.VerticalMargin = 2
	m.applyPreferences(p)
	var words []string
	for range 300 {
		words = append(words, numberedWord.FindAllString(m.View(), -1)...)
		before := m.rowIndex()
		m.pageDown()
		if before == m.rowIndex() {
			break
		}
	}
	if len(words) != 360 {
		t.Fatalf("spacing lost/repeated words: got%d", len(words))
	}
	for i, w := range words {
		if w != fmtWord(i+1) {
			t.Fatalf("word%d=%s", i, w)
		}
	}
}
func fmtWord(i int) string {
	return "WORD" + string(rune('0'+i/100)) + string(rune('0'+i/10%10)) + string(rune('0'+i%10))
}
func TestCustomNavigationKeyTurnsARealPage(t *testing.T) {
	m := numberedReader()
	p := config.DefaultPreferences()
	p.NavigationKeys.NextPage = "m"
	m.applyPreferences(p)
	before := numberedWord.FindString(m.View())
	next, _ := m.Update(runeKey("m"))
	m = next.(readerModel)
	if numberedWord.FindString(m.View()) == before {
		t.Fatal("configured page key did not change the passage")
	}
}
func TestLocalOnlySettingNeverWritesCloud(t *testing.T) {
	isolateHome(t)
	book := cacheDune(t, cloudAt())
	cloud := newFakeCloud(t, book.ReadPosition)
	app := openInApp(t, cloud, book)
	m := app.reader.(readerModel)
	p := config.DefaultPreferences()
	p.CloudSync = false
	m.applyPreferences(p)
	m.nextChapter()
	wantChapter := m.chapterIdx
	app.reader = m
	_, cmd := app.Update(runeKey("q"))
	runCmd(app, cmd)
	if app.screen != screenLibrary || cloud.postCount() != 0 {
		t.Fatal("local-only exit sent Cloud progress or failed to leave")
	}
	pos, err := reader.LoadPosition(book.FastHash)
	if err != nil || pos == nil || pos.ChapterIndex != wantChapter {
		t.Fatal("local-only exit lost the reading position")
	}
}
func TestLibrarySortAndCompactDensityKeepSelectedBook(t *testing.T) {
	isolateHome(t)
	books := []pbc.Book{{FastHash: "z", Title: "Zulu"}, {FastHash: "a", Title: "Alpha"}, {FastHash: "b", Title: "Beta"}}
	m := newLibraryModel(nil, nil)
	m.loading = false
	m.width, m.height = 40, 8
	m.setBooks(books)
	m.cursor = 2
	selected, _ := m.selectedBook()
	m.sortOrder = "title"
	m.compact = true
	m.setBooks(books)
	got, _ := m.selectedBook()
	if got.FastHash != selected.FastHash || m.books[0].Title != "Alpha" {
		t.Fatal("sorting changed selection or ignored title order")
	}
	assertFits(t, m.View(), 40, 8)
	assertContains(t, m.View(), "Beta")
}

func TestHelpScrollsAllControlsWithoutMovingBook(t *testing.T) {
	m := numberedReader()
	m.pageDown()
	before := m.position()
	sourceOffset := m.withinLineOffset
	next, _ := m.Update(tea.WindowSizeMsg{Width: 20, Height: 8})
	m = next.(readerModel)
	next, _ = m.Update(runeKey("?"))
	m = next.(readerModel)
	foundPhoto, foundExit := false, false
	for range 80 {
		view := m.View()
		assertFits(t, view, 20, 8)
		for _, row := range plainRows(view) {
			foundPhoto = foundPhoto || strings.HasPrefix(strings.TrimSpace(row), "I ")
			foundExit = foundExit || strings.HasPrefix(strings.TrimSpace(row), "ctrl+c ")
		}
		next, _ = m.Update(downKey)
		m = next.(readerModel)
	}
	if !foundPhoto || !foundExit {
		t.Fatal("short-terminal help hid reader controls")
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(readerModel)
	if cmd != nil || m.showHelp || m.chapterIdx != before.ChapterIndex || m.lineOffset != before.LineOffset || m.withinLineOffset != sourceOffset {
		t.Fatal("reading help moved or saved the book position")
	}
}
