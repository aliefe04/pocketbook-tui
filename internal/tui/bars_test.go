package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aliefe/pocketbook-tui/internal/config"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestSaveFailureNoticeSurvivesHiddenBars(t *testing.T) {
	for _, size := range [][2]int{{20, 8}, {40, 14}, {80, 24}} {
		for _, header := range []bool{false, true} {
			for _, footer := range []bool{false, true} {
				t.Run(fmt.Sprintf("%dx%d-header%v-footer%v", size[0], size[1], header, footer), func(t *testing.T) {
					home := isolateHome(t)
					blocker := filepath.Join(home, ".config", "pocketbook-tui")
					if err := os.MkdirAll(filepath.Dir(blocker), 0755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(blocker, []byte("blocker"), 0600); err != nil {
						t.Fatal(err)
					}
					m := newReaderModel(testContent(), "bar-save", "Book", nil, size[0], size[1])
					p := config.DefaultPreferences()
					p.ShowHeader, p.ShowFooter = header, footer
					m.applyPreferences(p)
					next, cmd := m.Update(runeKey("q"))
					m = next.(readerModel)
					if cmd != nil || !m.statusIsError {
						t.Fatal("failed save did not keep reader open")
					}
					assertContains(t, m.View(), "Position not saved")
					assertFits(t, m.View(), size[0], size[1])
					if header || footer {
						assertContains(t, m.View(), "S:settings")
					} else {
						assertNotContains(t, m.View(), "S:settings")
					}
				})
			}
		}
	}
}

func TestSettingsShortcutDisplayedAndOpensSettingsAtAllSizes(t *testing.T) {
	for _, size := range [][2]int{{20, 8}, {40, 14}, {80, 24}} {
		t.Run(fmt.Sprintf("library-%dx%d", size[0], size[1]), func(t *testing.T) {
			isolateHome(t)
			app := newTestApp(t)
			app.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			assertContains(t, app.View(), "S settings")
			assertFits(t, app.View(), size[0], size[1])
			app.Update(runeKey("S"))
			if app.screen != screenSettings {
				t.Fatalf("expected screenSettings, got %v", app.screen)
			}
		})

		t.Run(fmt.Sprintf("detail-%dx%d", size[0], size[1]), func(t *testing.T) {
			isolateHome(t)
			app := newTestApp(t)
			book := testBooks("Detail Book")[0]
			app.detail = newDetailModel(book, app.client, app.cfg)
			app.screen = screenDetail
			app.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			assertContains(t, app.View(), "S settings")
			assertFits(t, app.View(), size[0], size[1])
			app.Update(runeKey("S"))
			if app.screen != screenSettings {
				t.Fatalf("expected screenSettings, got %v", app.screen)
			}
		})

		t.Run(fmt.Sprintf("reader-footer-%dx%d", size[0], size[1]), func(t *testing.T) {
			isolateHome(t)
			app := newTestApp(t)
			app.reader = newReaderModel(testContent(), "r-hash", "Book", nil, size[0], size[1])
			app.screen = screenReader
			app.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			assertContains(t, app.View(), "S:settings")
			assertFits(t, app.View(), size[0], size[1])
			app.Update(runeKey("S"))
			if app.screen != screenSettings {
				t.Fatalf("expected screenSettings, got %v", app.screen)
			}
		})

		t.Run(fmt.Sprintf("reader-header-only-%dx%d", size[0], size[1]), func(t *testing.T) {
			isolateHome(t)
			app := newTestApp(t)
			r := newReaderModel(testContent(), "r-hash", "Book", nil, size[0], size[1])
			p := config.DefaultPreferences()
			p.ShowFooter = false
			r.applyPreferences(p)
			app.reader = r
			app.screen = screenReader
			app.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			assertContains(t, app.View(), "S:settings")
			assertFits(t, app.View(), size[0], size[1])
			app.Update(runeKey("S"))
			if app.screen != screenSettings {
				t.Fatalf("expected screenSettings, got %v", app.screen)
			}
		})
	}
}

func TestFilterModeKeepsSLiteralAndHidesSettingsHint(t *testing.T) {
	isolateHome(t)
	app := newTestApp(t)
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	app.Update(runeKey("/"))
	if lib, ok := app.library.(libraryModel); !ok || !lib.filterMode {
		t.Fatal("expected library to enter filterMode")
	}
	assertNotContains(t, app.View(), "settings")

	app.Update(runeKey("S"))
	if app.screen != screenLibrary {
		t.Fatalf("typing S in filterMode changed screen to %v", app.screen)
	}
	if lib := app.library.(libraryModel); lib.filter != "S" {
		t.Fatalf("expected filter 'S', got %q", lib.filter)
	}

	app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if lib := app.library.(libraryModel); lib.filterMode {
		t.Fatal("expected filterMode false after enter")
	}
	assertContains(t, app.View(), "settings")

	app.Update(runeKey("S"))
	if app.screen != screenSettings {
		t.Fatalf("expected screenSettings after exiting filter, got %v", app.screen)
	}
}

func TestPageWordCoverageWithExtraStatusRow(t *testing.T) {
	m := numberedReader()
	m.setStatus("Cloud sync notice", false)
	var got []string
	for range 100 {
		view := m.View()
		assertFits(t, view, 44, 14)
		got = append(got, numberedWord.FindAllString(ansi.Strip(view), -1)...)
		before := m.rowIndex()
		m.pageDown()
		if before == m.rowIndex() {
			break
		}
	}
	var want []string
	for i := 1; i <= 360; i++ {
		want = append(want, fmt.Sprintf("WORD%03d", i))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("page turns lost or repeated words with status row: got %d, want 360", len(got))
	}
}

func TestCompact20x8BoundsWithMaxVerticalMarginAndRecoveryAction(t *testing.T) {
	m := newReaderModel(testContent(), "compact", "Book", nil, 20, 8)
	p := config.DefaultPreferences()
	p.VerticalMargin = 4
	m.applyPreferences(p)
	m.phase = phaseConflict
	m.conflict = &nativeProgress{percent: 42}
	m.setStatus("Cloud changed elsewhere", true)
	view := m.View()
	assertFits(t, view, 20, 8)
	assertContains(t, view, "S:settings")
	assertContains(t, view, "o send")
	assertContains(t, view, "c load")
	assertContains(t, view, "l local")
	assertContains(t, view, "↵ read")
}

func TestBookFooterShowsPageCountAndChapterCountdown(t *testing.T) {
	m := numberedReader()
	p := config.DefaultPreferences()
	p.Progress, p.ShowHeader = "book", false
	m.applyPreferences(p)
	page, total := m.pageCount()
	if total < 2 {
		t.Fatalf("page count = %d, want several pages", total)
	}
	view := m.View()
	assertFits(t, view, 44, 14)
	assertContains(t, view, fmt.Sprintf("%d / %d", page, total))
	assertContains(t, view, "pages to next chapter")
	if strings.Contains(ansi.Strip(view), "S:settings") {
		t.Fatal("the book footer kept the control row")
	}
}

func TestBookFooterCountdownTracksPagesAndChapterEnd(t *testing.T) {
	m := numberedReader()
	p := config.DefaultPreferences()
	p.Progress, p.ShowHeader = "book", false
	m.applyPreferences(p)
	if pages, ok := m.pagesToNextChapter(); !ok || pages < 2 {
		t.Fatalf("countdown on the first page = %d, ok=%v, want several pages", pages, ok)
	}
	sawOne := false
	for {
		pages, ok := m.pagesToNextChapter()
		if !ok {
			t.Fatal("countdown lost the next chapter before it started")
		}
		want := fmt.Sprintf("%d pages to next chapter", pages)
		if pages == 1 {
			want = "1 page to next chapter"
			sawOne = true
		}
		assertContains(t, m.View(), want)
		before := m.rowIndex()
		m.pageDown()
		if before == m.rowIndex() {
			break
		}
	}
	if !sawOne {
		t.Fatal("the last page before the seam never reported one page")
	}
	m.nextChapter()
	if m.chapterIdx != 1 {
		t.Fatalf("next chapter left the reader in chapter %d", m.chapterIdx)
	}
	assertContains(t, m.View(), "End of book")
}

func TestPageWordCoverageWithBookFooter(t *testing.T) {
	m := numberedReader()
	p := config.DefaultPreferences()
	p.Progress, p.ShowHeader = "book", false
	m.applyPreferences(p)
	var got []string
	for range 100 {
		view := m.View()
		assertFits(t, view, 44, 14)
		got = append(got, numberedWord.FindAllString(ansi.Strip(view), -1)...)
		before := m.rowIndex()
		m.pageDown()
		if before == m.rowIndex() {
			break
		}
	}
	var want []string
	for i := 1; i <= 360; i++ {
		want = append(want, fmt.Sprintf("WORD%03d", i))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("page turns lost or repeated words with the book footer: got %d, want 360", len(got))
	}
}

func TestBookProgressKeepsStatusWithHiddenFooter(t *testing.T) {
	m := numberedReader()
	p := config.DefaultPreferences()
	p.Progress, p.ShowFooter = "book", false
	m.applyPreferences(p)
	m.setStatus("Position not saved", true)
	page, total := m.pageCount()
	view := m.View()
	assertFits(t, view, 44, 14)
	assertContains(t, view, "Position not saved")
	assertContains(t, view, "S:settings")
	assertContains(t, view, fmt.Sprintf("%d / %d", page, total))
}

func TestBookFooterFitsCompactTerminal(t *testing.T) {
	for _, size := range [][2]int{{20, 8}, {40, 14}} {
		for _, header := range []bool{false, true} {
			t.Run(fmt.Sprintf("%dx%d-header%v", size[0], size[1], header), func(t *testing.T) {
				m := newReaderModel(testContent(), "book-compact", "Book", nil, size[0], size[1])
				p := config.DefaultPreferences()
				p.Progress, p.ShowHeader = "book", header
				m.applyPreferences(p)
				page, total := m.pageCount()
				view := m.View()
				assertFits(t, view, size[0], size[1])
				assertContains(t, view, fmt.Sprintf("%d / %d", page, total))
				assertContains(t, view, "to next")
			})
		}
	}
}
