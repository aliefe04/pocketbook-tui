package tui

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/aliefe/pocketbook-tui/internal/config"
	"github.com/aliefe/pocketbook-tui/internal/reader"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

// styledReader is one chapter whose lines carry the kinds that the device
// shows differently.
func styledReader() readerModel {
	return newReaderModel(&reader.BookContent{Chapters: []reader.Chapter{{
		Title: "Chapter Seven",
		Lines: []string{
			"A Bold Heading", "",
			"An epigraph line.", "— The Mirror",
			"Body text on the page.",
			"A quoted passage.", "— Someone Else",
			"A caption line.",
		},
		Kinds: []reader.LineKind{
			reader.LineHeading, reader.LineBody,
			reader.LineEpigraph, reader.LineAttribution,
			reader.LineBody,
			reader.LineQuote, reader.LineAttribution,
			reader.LineCaption,
		},
	}}}, "styled", "The Mirror", nil, 80, 24)
}

func TestReaderPlacesStyledLinesLikeTheDevice(t *testing.T) {
	m := styledReader()
	p := config.DefaultPreferences()
	p.ShowHeader = false
	m.applyPreferences(p)
	column := m.contentWidth()
	margin := max((80-column)/2, 0)
	centred := func(text string) int { return margin + max((column-lipgloss.Width(text))/2, 0) }
	rows := strings.Split(m.View(), "\n")
	row := func(marker string) string {
		for _, candidate := range rows {
			if line := ansi.Strip(candidate); strings.Contains(line, marker) {
				return line
			}
		}
		t.Fatalf("row %q is not on the page", marker)
		return ""
	}
	lead := func(line string) int { return len(line) - len(strings.TrimLeft(line, " ")) }

	// A heading and body text stay at the margin, a quotation is indented two
	// cells, and an epigraph, a source line and a caption are centred.
	if got, want := lead(row("A Bold Heading")), margin; got != want {
		t.Errorf("heading starts at %d, want the margin %d", got, want)
	}
	if got, want := lead(row("Body text on the page.")), margin; got != want {
		t.Errorf("body starts at %d, want the margin %d", got, want)
	}
	if got, want := lead(row("A quoted passage.")), margin+2; got != want {
		t.Errorf("quotation starts at %d, want %d", got, want)
	}
	for _, marker := range []string{"An epigraph line.", "— The Mirror", "— Someone Else", "A caption line."} {
		if got, want := lead(row(marker)), centred(marker); got != want {
			t.Errorf("%q starts at %d, want the centre %d", marker, got, want)
		}
	}
}
