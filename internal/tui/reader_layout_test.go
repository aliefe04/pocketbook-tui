package tui

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/aliefe/pocketbook-tui/internal/reader"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

var numberedWord = regexp.MustCompile(`WORD\d{3}`)

func numberedReader() readerModel {
	var words []string
	for i := 1; i <= 360; i++ {
		words = append(words, fmt.Sprintf("WORD%03d", i))
	}
	return newReaderModel(&reader.BookContent{Chapters: []reader.Chapter{{Lines: []string{strings.Join(words, " ")}}, {Title: "Next chapter", Lines: []string{"CHAPTER_SEAM"}}}}, "numbered", "Numbered", nil, 44, 14)
}

func TestPageTurnsCoverLongParagraphExactlyOnce(t *testing.T) {
	m := numberedReader()
	var got []string
	seam := false
	for range 100 {
		view := m.View()
		assertFits(t, view, 44, 14)
		got = append(got, numberedWord.FindAllString(ansi.Strip(view), -1)...)
		seam = seam || strings.Contains(view, "CHAPTER_SEAM")
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
		t.Fatalf("page turns lost or repeated words: got %d ordered words, want360", len(got))
	}
	if !seam {
		t.Fatal("page turns did not expose the next chapter")
	}
}

func TestPageBackRestoresPassageAndLineMoveDoesNotSkipParagraph(t *testing.T) {
	m := numberedReader()
	first := numberedWord.FindAllString(m.View(), -1)
	m.pageDown()
	m.pageUp()
	if got := numberedWord.FindAllString(m.View(), -1); !reflect.DeepEqual(got, first) {
		t.Fatal("forward/back changed the visible passage")
	}
	m.scrollDown(1)
	got := numberedWord.FindAllString(m.View(), -1)
	if len(got) == 0 || got[0] != "WORD006" {
		t.Fatalf("line move skipped the wrapped paragraph: first words %v", got[:min(3, len(got))])
	}
}

func TestResizeKeepsTheSourcePassage(t *testing.T) {
	m := numberedReader()
	m.pageDown()
	word := numberedWord.FindString(m.View())
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 64, Height: 18})
	m = updated.(readerModel)
	assertContains(t, m.View(), word)
	assertFits(t, m.View(), 64, 18)
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 20, Height: 8})
	m = updated.(readerModel)
	assertContains(t, m.View(), word)
	assertFits(t, m.View(), 20, 8)
}

func TestPageCoverageAccountsForCloudStatusRows(t *testing.T) {
	m := numberedReader()
	m.note = &cloudStatus{body: "Cloud timed out. C:retry", brief: "Cloud timed out", isErr: true}
	var got []string
	for range 100 {
		got = append(got, numberedWord.FindAllString(m.View(), -1)...)
		before := m.rowIndex()
		m.pageDown()
		if before == m.rowIndex() {
			break
		}
	}
	if len(got) != 360 {
		t.Fatalf("Cloud status caused lost/repeated text: got%d words", len(got))
	}
	for i, word := range got {
		if word != fmt.Sprintf("WORD%03d", i+1) {
			t.Fatalf("word%d=%s", i, word)
		}
	}
}

func TestGraphemeWrapPreservesWideAndCombiningText(t *testing.T) {
	input := strings.Repeat("界🙂e\u0301", 30)
	rows := wrapSource(input, 16)
	var rebuilt strings.Builder
	for _, row := range rows {
		if ansi.StringWidth(row.text) > 16 {
			t.Fatalf("row exceeds16cells: %q", row.text)
		}
		rebuilt.WriteString(row.text)
	}
	if rebuilt.String() != input {
		t.Fatal("wrapping lost or split the original grapheme text")
	}
	if got := cleanBookText("before\x1b[31mred\x1b[0mafter\x07"); got != "beforeredafter" {
		t.Fatalf("unsafe terminal controls retained: %q", got)
	}
}

func TestWrappedMovementIsNotAnUnchangedCloudBookmark(t *testing.T) {
	m := numberedReader()
	m.attach(nil, &resumePoint{source: sourceCloud})
	m.scrollDown(1)
	if m.unmoved() {
		t.Fatal("moving inside a paragraph was treated as an unchanged Cloud bookmark")
	}
	if m.position().LineOffset != 0 {
		t.Fatal("wrapped-row movement changed the persisted paragraph coordinate")
	}
}
