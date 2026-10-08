package reader

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// buildEPUB returns a minimal EPUB. docs maps manifest ids to XHTML, and spine
// lists idrefs in reading order. An idref with no doc is in the spine but not
// in the manifest, so the parser skips it.
func buildEPUB(t *testing.T, docs map[string]string, spine []string) []byte {
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

func testXHTML(title, body string) string {
	return "<html><head><title>" + title + "</title></head><body>" + body + "</body></html>"
}

func TestParseEPUBPointerAcceptsSupportedForms(t *testing.T) {
	cases := []struct {
		in      string
		itemref int
		steps   int
	}{
		{"#epubcfi(/6/24!/4/4/1)", 24, 3},
		{"epubcfi(/6/2!/4[body01]/10[para05]/3:10)", 2, 3},
		{"#epubcfi(/6/4!/4/2/1[^[x^]]:0)", 4, 3},
		// A range starts at parent+start.
		{"#epubcfi(/6/4!/4,/2/1:0,/3:5)", 4, 3},
	}
	for _, tc := range cases {
		ptr, err := parseEPUBPointer(tc.in)
		if err != nil {
			t.Fatalf("parseEPUBPointer(%q): %v", tc.in, err)
		}
		if ptr.itemref != tc.itemref || len(ptr.steps) != tc.steps {
			t.Errorf("parseEPUBPointer(%q) = itemref %d, %d steps; want itemref %d, %d steps",
				tc.in, ptr.itemref, len(ptr.steps), tc.itemref, tc.steps)
		}
	}
}

func TestParseEPUBPointerRejectsUnsupportedForms(t *testing.T) {
	invalid := []string{
		"",
		"/6/24!/4/4/1",
		"#epubcfi(/6/24/4/4/1)",
		"#epubcfi(/4/24!/4/1)",
		"#epubcfi(/6/23!/4/1)",
		"#epubcfi(/6/24!/4/1/2)",
		"#epubcfi(/6/24!/4[body/1)",
		"#epubcfi(/6/24!/4/1~5)",
		"#epubcfi(/6/24!/4/0)",
		"#epubcfi(/6/24!/4/2:3)",
	}
	for _, in := range invalid {
		if _, err := parseEPUBPointer(in); err == nil {
			t.Errorf("parseEPUBPointer(%q) succeeded, want an error", in)
		}
	}
}

func TestParseEPUBAtLocatesContainingParagraph(t *testing.T) {
	docs := map[string]string{
		"one":   testXHTML("One", "<p>Opening.</p>"),
		"two":   testXHTML("Two", `<h1>Two</h1><p>First.</p><div><p>Second.</p><p>Third.</p></div>`),
		"three": testXHTML("Three", "<p>Closing.</p>"),
	}
	spine := []string{"one", "two", "three"}
	data := buildEPUB(t, docs, spine)

	cases := []struct {
		pointer string
		want    string
	}{
		{"#epubcfi(/6/4!/4/4/1)", "First."},
		{"#epubcfi(/6/4!/4/6/4/1)", "Third."},
	}
	for _, tc := range cases {
		content, bm, err := ParseEPUBAt(data, tc.pointer)
		if err != nil {
			t.Fatalf("ParseEPUBAt(%q): %v", tc.pointer, err)
		}
		if bm.Err != nil {
			t.Fatalf("ParseEPUBAt(%q) bookmark: %v", tc.pointer, bm.Err)
		}
		if bm.Chapter != 1 {
			t.Fatalf("ParseEPUBAt(%q) chapter = %d, want 1", tc.pointer, bm.Chapter)
		}
		ch := content.Chapters[bm.Chapter]
		body := bm.LineOffset - ch.TitleHeight()
		if body < 0 || body >= len(ch.Lines) {
			t.Fatalf("ParseEPUBAt(%q) body line %d outside %d lines", tc.pointer, body, len(ch.Lines))
		}
		if ch.Lines[body] != tc.want {
			t.Errorf("ParseEPUBAt(%q) line %d = %q, want %q", tc.pointer, body, ch.Lines[body], tc.want)
		}
	}
}

func TestParseEPUBAtKeepsSpineIndexWhenItemIsSkipped(t *testing.T) {
	// "a" is in the spine but not in the manifest, so it produces no chapter.
	// The pointer names spine item 2, which is chapter 0 of the content.
	docs := map[string]string{
		"b": testXHTML("B", "<p>Bee paragraph.</p>"),
	}
	data := buildEPUB(t, docs, []string{"a", "b"})

	content, bm, err := ParseEPUBAt(data, "#epubcfi(/6/4!/4/2/1)")
	if err != nil {
		t.Fatalf("ParseEPUBAt: %v", err)
	}
	if bm.Err != nil {
		t.Fatalf("bookmark: %v", bm.Err)
	}
	if len(content.Chapters) != 1 || bm.Chapter != 0 {
		t.Fatalf("chapters = %d, bookmark chapter = %d; want 1 and 0", len(content.Chapters), bm.Chapter)
	}
	ch := content.Chapters[0]
	if body := bm.LineOffset - ch.TitleHeight(); ch.Lines[body] != "Bee paragraph." {
		t.Errorf("line %d = %q, want %q", body, ch.Lines[body], "Bee paragraph.")
	}
}

func TestParseEPUBAtReportsUnresolvablePointerAndKeepsBook(t *testing.T) {
	docs := map[string]string{
		"one":   testXHTML("One", "<p>Opening.</p>"),
		"two":   testXHTML("Two", "<p>Middle.</p>"),
		"three": testXHTML("Three", "<p>Closing.</p>"),
	}
	data := buildEPUB(t, docs, []string{"one", "two", "three"})

	for _, pointer := range []string{"#epubcfi(/6/4!/4/40/1)", "#epubcfi(/6/4!/4[x)"} {
		content, bm, err := ParseEPUBAt(data, pointer)
		if err != nil {
			t.Fatalf("ParseEPUBAt(%q): %v", pointer, err)
		}
		if bm.Err == nil {
			t.Errorf("ParseEPUBAt(%q) resolved, want an error", pointer)
		}
		if content == nil || len(content.Chapters) != 3 {
			t.Errorf("ParseEPUBAt(%q) dropped chapters", pointer)
		}
	}
}

func TestParseEPUBDoesNotChangeExtractedText(t *testing.T) {
	docs := map[string]string{
		"one": testXHTML("One", `<h1>Head</h1><p>Text <em>inline</em> more.</p>`),
	}
	data := buildEPUB(t, docs, []string{"one"})

	plain, err := ParseEPUB(data)
	if err != nil {
		t.Fatalf("ParseEPUB: %v", err)
	}
	located, _, err := ParseEPUBAt(data, "#epubcfi(/6/2!/4/4/1)")
	if err != nil {
		t.Fatalf("ParseEPUBAt: %v", err)
	}
	if strings.Join(plain.Chapters[0].Lines, "\n") != strings.Join(located.Chapters[0].Lines, "\n") {
		t.Errorf("located text %q differs from plain text %q", located.Chapters[0].Lines, plain.Chapters[0].Lines)
	}
}

func TestLocateFractionCoversTheBook(t *testing.T) {
	c := makeTestContent()

	ch, line := c.LocateFraction(0)
	if ch != 0 || line != 0 {
		t.Errorf("LocateFraction(0) = %d, %d; want 0, 0", ch, line)
	}

	lastCh, lastLine := c.PositionToChapter(c.TotalLines() - 1)
	if ch, line := c.LocateFraction(100); ch != lastCh || line != lastLine {
		t.Errorf("LocateFraction(100) = %d, %d; want %d, %d", ch, line, lastCh, lastLine)
	}
}

// bookmarkLine returns the body line that a resolved bookmark names.
func bookmarkLine(t *testing.T, content *BookContent, bm Bookmark) string {
	t.Helper()
	ch := content.Chapters[bm.Chapter]
	body := bm.LineOffset - ch.TitleHeight()
	if body < 0 || body >= len(ch.Lines) {
		t.Fatalf("bookmark line %d outside %d body lines", body, len(ch.Lines))
	}
	return ch.Lines[body]
}

func TestParseEPUBAtRejectsOverflowingOffset(t *testing.T) {
	docs := map[string]string{
		"one": testXHTML("One", "<p>Opening.</p>"),
		"two": testXHTML("Two", "<p>First.</p>"),
	}
	data := buildEPUB(t, docs, []string{"one", "two"})

	// The offset does not fit in an int, so it must not resolve to any place.
	const pointer = "#epubcfi(/6/4!/4/2/1:999999999999999999999999999999999999999)"
	content, bm, err := ParseEPUBAt(data, pointer)
	if err != nil {
		t.Fatalf("ParseEPUBAt: %v", err)
	}
	if bm.Err == nil {
		t.Fatalf("ParseEPUBAt resolved to chapter %d line %d, want an error", bm.Chapter, bm.LineOffset)
	}
	if content == nil || len(content.Chapters) != 2 {
		t.Fatalf("ParseEPUBAt dropped the readable content")
	}
}

func TestParseEPUBAtChecksTextOffsetsInUTF16Units(t *testing.T) {
	docs := map[string]string{
		"one": testXHTML("One", "<p>Opening.</p>"),
		"two": testXHTML("Two", "<p>First.</p><p>😀ok</p><p>ab<!--note-->cd</p>"),
	}
	data := buildEPUB(t, docs, []string{"one", "two"})

	cases := []struct {
		pointer string
		want    string // expected body line; empty when only the outcome matters
		ok      bool
	}{
		// "First." has six UTF-16 units, so six is the end of the chunk.
		{"#epubcfi(/6/4!/4/2/1:6)", "First.", true},
		{"#epubcfi(/6/4!/4/2/1:7)", "", false},
		// U+1F600 takes two UTF-16 units, so "😀ok" has four, not three.
		{"#epubcfi(/6/4!/4/4/1:4)", "😀ok", true},
		{"#epubcfi(/6/4!/4/4/1:5)", "", false},
		// The comment is not text, so "ab" and "cd" make four units.
		{"#epubcfi(/6/4!/4/6/1:4)", "", true},
		{"#epubcfi(/6/4!/4/6/1:5)", "", false},
	}
	for _, tc := range cases {
		content, bm, err := ParseEPUBAt(data, tc.pointer)
		if err != nil {
			t.Fatalf("ParseEPUBAt(%q): %v", tc.pointer, err)
		}
		if !tc.ok {
			if bm.Err == nil {
				t.Errorf("ParseEPUBAt(%q) resolved to line %d, want an error", tc.pointer, bm.LineOffset)
			}
			if content == nil || len(content.Chapters) != 2 {
				t.Errorf("ParseEPUBAt(%q) dropped chapters", tc.pointer)
			}
			continue
		}
		if bm.Err != nil {
			t.Errorf("ParseEPUBAt(%q) bookmark: %v", tc.pointer, bm.Err)
			continue
		}
		if bm.Chapter != 1 {
			t.Errorf("ParseEPUBAt(%q) chapter = %d, want 1", tc.pointer, bm.Chapter)
		}
		if got := bookmarkLine(t, content, bm); tc.want != "" && got != tc.want {
			t.Errorf("ParseEPUBAt(%q) line = %q, want %q", tc.pointer, got, tc.want)
		}
	}
}

func TestParseEPUBAtRejectsNonexistentTextChunk(t *testing.T) {
	docs := map[string]string{
		"one": testXHTML("One", "<p>First.</p>"),
		"two": testXHTML("Two", "<p>Second <em>word</em></p>"),
	}
	data := buildEPUB(t, docs, []string{"one", "two"})

	cases := []struct {
		pointer string
		want    string // expected body line; empty when the pointer must fail
	}{
		// "First." has no element children, so the chunk after element 1 is missing.
		{"#epubcfi(/6/2!/4/2/1)", "First."},
		{"#epubcfi(/6/2!/4/2/3)", ""},
		// The paragraph has one element child, so the chunk after it exists and is empty.
		{"#epubcfi(/6/4!/4/2/3)", "Second word"},
		{"#epubcfi(/6/4!/4/2/5)", ""},
		{"#epubcfi(/6/4!/4/2/999)", ""},
	}
	for _, tc := range cases {
		content, bm, err := ParseEPUBAt(data, tc.pointer)
		if err != nil {
			t.Fatalf("ParseEPUBAt(%q): %v", tc.pointer, err)
		}
		if tc.want == "" {
			if bm.Err == nil {
				t.Errorf("ParseEPUBAt(%q) resolved to line %d, want an error", tc.pointer, bm.LineOffset)
			}
			if content == nil || len(content.Chapters) != 2 {
				t.Errorf("ParseEPUBAt(%q) dropped chapters", tc.pointer)
			}
			continue
		}
		if bm.Err != nil {
			t.Errorf("ParseEPUBAt(%q) bookmark: %v", tc.pointer, bm.Err)
			continue
		}
		if got := bookmarkLine(t, content, bm); got != tc.want {
			t.Errorf("ParseEPUBAt(%q) line = %q, want %q", tc.pointer, got, tc.want)
		}
	}
}
