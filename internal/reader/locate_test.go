package reader

import "testing"

func TestEmptySelfClosingAnchorBeforeParagraphDoesNotShiftCFI(t *testing.T) {
	body := `<h1>Heading</h1><a id="empty-anchor"/><p>Target paragraph.</p><p>Next.</p>`
	data := buildEPUB(t, map[string]string{"one": testXHTML("One", body)}, []string{"one"})

	// Body step 4, then the second element of the body is the paragraph at
	// step 6, counting the empty anchor as a child.
	content, bm, err := ParseEPUBAt(data, "#epubcfi(/6/2!/4/6/1:0)")
	if err != nil {
		t.Fatalf("ParseEPUBAt: %v", err)
	}
	if bm.Err != nil {
		t.Fatalf("bookmark: %v", bm.Err)
	}
	if got := bookmarkLine(t, content, bm); got != "Target paragraph." {
		t.Errorf("CFI resolved to %q, want the target paragraph", got)
	}
}

// roundTripDocs has inline markup, entities, a self-closing anchor inside a
// paragraph, nested blocks and two chapters.
var roundTripDocs = map[string]string{
	"one": testXHTML("One", `<h1>Opening <em>bold</em> heading</h1>`+
		`<p>First <a id="x"/>paragraph with &amp; entity&nbsp;text.</p>`+
		`<div><p>Second.</p></div>`+
		`<p>&#x1F600; emoji first<span>tail</span></p>`),
	"two": testXHTML("Two", `<h2>Second chapter</h2><p>Only paragraph.</p>`),
}

func TestExportedPointersImportToTheSameParagraph(t *testing.T) {
	data := buildEPUB(t, roundTripDocs, []string{"one", "two"})
	content, err := ParseEPUB(data)
	if err != nil {
		t.Fatalf("ParseEPUB: %v", err)
	}

	checked := 0
	for ch := range content.Chapters {
		c := content.Chapters[ch]
		titleH := c.TitleHeight()
		// Line 0 is the <head> title text, which has no body location. Blank
		// lines are spacing and stand for the nearest paragraph instead.
		for b := 1; b < len(c.Lines); b++ {
			if c.Lines[b] == "" {
				continue
			}
			ptr, ok := content.PointerAt(ch, titleH+b)
			if !ok {
				t.Fatalf("chapter %d line %d has no pointer", ch, b)
			}
			_, bm, err := ParseEPUBAt(data, ptr)
			if err != nil {
				t.Fatalf("ParseEPUBAt(%q): %v", ptr, err)
			}
			if bm.Err != nil {
				t.Fatalf("exported %q does not import: %v", ptr, bm.Err)
			}
			if bm.Chapter != ch || bm.LineOffset != titleH+b {
				t.Errorf("exported %q imported as chapter %d line %d, want chapter %d line %d (%q)",
					ptr, bm.Chapter, bm.LineOffset, ch, titleH+b, c.Lines[b])
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatalf("no paragraph was checked")
	}
}

func TestTitleChromeStandsForTheFirstBodyParagraph(t *testing.T) {
	data := buildEPUB(t, map[string]string{"one": testXHTML("One", `<h1>Heading</h1><p>Para.</p>`)}, []string{"one"})
	content, err := ParseEPUB(data)
	if err != nil {
		t.Fatalf("ParseEPUB: %v", err)
	}
	want := "#epubcfi(/6/2!/4/2/1:0)"
	for _, line := range []int{0, 2} {
		got, ok := content.PointerAt(0, line)
		if !ok || got != want {
			t.Errorf("PointerAt(0, %d) = %q, %v; want %q", line, got, ok, want)
		}
	}
}

func TestIDAssertionsMustMatchTheElement(t *testing.T) {
	doc := `<html><head><title>T</title></head><body id="body01"><h1>H</h1><p id="p2">Target.</p></body></html>`
	data := buildEPUB(t, map[string]string{"one": doc}, []string{"one"})

	cases := []struct {
		pointer string
		want    string
	}{
		{"#epubcfi(/6/2!/4[body01]/4[p2]/1:0)", "Target."},
		{"#epubcfi(/6/2!/4[nope]/4/1:0)", ""},
		{"#epubcfi(/6/2!/4/4[other]/1:0)", ""},
	}
	for _, tc := range cases {
		content, bm, err := ParseEPUBAt(data, tc.pointer)
		if err != nil {
			t.Fatalf("ParseEPUBAt(%q): %v", tc.pointer, err)
		}
		if tc.want == "" {
			if bm.Err == nil {
				t.Errorf("ParseEPUBAt(%q) resolved to line %d, want an assertion error", tc.pointer, bm.LineOffset)
			}
			continue
		}
		if bm.Err != nil {
			t.Fatalf("ParseEPUBAt(%q): %v", tc.pointer, bm.Err)
		}
		if got := bookmarkLine(t, content, bm); got != tc.want {
			t.Errorf("ParseEPUBAt(%q) line = %q, want %q", tc.pointer, got, tc.want)
		}
	}
}

func TestMalformedXHTMLHasNoExactLocations(t *testing.T) {
	// The unclosed <br> is accepted by HTML but is not well-formed XML.
	doc := `<html><head><title>T</title></head><body><p>Broken <br> markup</p><p>Second</p></body></html>`
	data := buildEPUB(t, map[string]string{"one": doc}, []string{"one"})

	content, err := ParseEPUB(data)
	if err != nil {
		t.Fatalf("ParseEPUB: %v", err)
	}
	if len(content.Chapters[0].Lines) == 0 {
		t.Fatalf("the text was lost")
	}
	if _, ok := content.PointerAt(0, content.Chapters[0].TitleHeight()); ok {
		t.Errorf("PointerAt gave an exact location for markup that is not well-formed XML")
	}

	_, bm, err := ParseEPUBAt(data, "#epubcfi(/6/2!/4/4/1:0)")
	if err != nil {
		t.Fatalf("ParseEPUBAt: %v", err)
	}
	if bm.Err == nil {
		t.Errorf("a pointer into malformed markup resolved to line %d, want an error", bm.LineOffset)
	}
}

func TestTXTHasNoEPUBLocations(t *testing.T) {
	content, err := ParseTXT([]byte("First line.\n\nSecond line."))
	if err != nil {
		t.Fatalf("ParseTXT: %v", err)
	}
	if _, ok := content.PointerAt(0, 0); ok {
		t.Errorf("TXT content gave an EPUB pointer")
	}
}

// kindDocs is markup with the styled blocks that the device shows differently:
// an in-text heading, an epigraph, a quotation with its source, a figure
// caption, a caption with block children and an italic-only passage.
var kindDocs = `<h1>Chapter Seven</h1>` +
	`<p class="epigraph">Nothing is written unless it is read.</p>` +
	`<p>Body text starts here.</p>` +
	`<blockquote><p>Quoted words, set apart.</p><p class="attribution">— Someone Else</p></blockquote>` +
	`<figure><figcaption>Figure 7.1 — A caption</figcaption> tail after the caption</figure>` +
	`<p><em>An italic passage that stands alone.</em></p>` +
	`<blockquote>Inline quotation text.</blockquote>` +
	`<figure><figcaption><p>First caption part.</p><p>Second caption part.</p></figcaption></figure>` +
	`<p>After the figure.</p>`

func TestLineKindsFollowTheBookMarkup(t *testing.T) {
	data := buildEPUB(t, map[string]string{"one": testXHTML("Chapter Seven", kindDocs)}, []string{"one"})
	content, err := ParseEPUB(data)
	if err != nil {
		t.Fatalf("ParseEPUB: %v", err)
	}
	want := []struct {
		line string
		kind LineKind
	}{
		{"Chapter Seven", LineBody}, // the document title, shown as chapter chrome
		{"Chapter Seven", LineHeading},
		{"", LineBody},
		{"Nothing is written unless it is read.", LineEpigraph},
		{"Body text starts here.", LineBody},
		{"Quoted words, set apart.", LineQuote},
		{"— Someone Else", LineAttribution},
		{"Figure 7.1 — A caption tail after the caption", LineCaption},
		{"An italic passage that stands alone.", LineEpigraph},
		{"Inline quotation text.", LineQuote},
		{"First caption part.", LineCaption},
		{"Second caption part.", LineCaption},
		{"After the figure.", LineBody},
	}
	chapter := content.Chapters[0]
	if len(chapter.Lines) != len(want) {
		t.Fatalf("chapter has %d lines, want %d: %q", len(chapter.Lines), len(want), chapter.Lines)
	}
	for i, w := range want {
		if got := chapter.Lines[i]; got != w.line {
			t.Errorf("line %d = %q, want %q", i, got, w.line)
		}
		if got := chapter.KindAt(i); got != w.kind {
			t.Errorf("line %d kind = %d, want %d (%q)", i, got, w.kind, w.line)
		}
	}
	// Kinds are decoration: a book without them, and an index past the end,
	// still read as body text.
	if got := chapter.KindAt(len(want)); got != LineBody {
		t.Errorf("kind past the end = %d, want LineBody", got)
	}
	if got := chapter.KindAt(-1); got != LineBody {
		t.Errorf("kind before the start = %d, want LineBody", got)
	}
}
