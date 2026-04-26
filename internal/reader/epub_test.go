package reader

import (
	"archive/zip"
	"bytes"
	"os"
	"testing"
)

func createTestEPUB(t *testing.T) []byte {
	// Create in-memory zip
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	// mimetype (must be first and uncompressed)
	w, _ := zw.Create("mimetype")
	w.Write([]byte("application/epub+zip"))

	// META-INF/container.xml
	w, _ = zw.Create("META-INF/container.xml")
	w.Write([]byte(`<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`))

	// OEBPS/content.opf
	w, _ = zw.Create("OEBPS/content.opf")
	w.Write([]byte(`<?xml version="1.0"?>
<package version="2.0" xmlns="http://www.idpf.org/2007/opf">
  <metadata>
    <dc:title>Test Book</dc:title>
  </metadata>
  <manifest>
    <item id="chapter1" href="chapter1.html" media-type="application/xhtml+xml"/>
  </manifest>
  <spine>
    <itemref idref="chapter1"/>
  </spine>
</package>`))

	// OEBPS/chapter1.html
	w, _ = zw.Create("OEBPS/chapter1.html")
	w.Write([]byte(`<?xml version="1.0"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml">
<head><title>Chapter 1</title></head>
<body>
<h1>Chapter One</h1>
<p>This is the first paragraph of the test book.</p>
<p>This is the second paragraph.</p>
</body>
</html>`))

	zw.Close()
	return buf.Bytes()
}

func TestParseEPUB(t *testing.T) {
	data := createTestEPUB(t)
	
	content, err := ParseEPUB(data)
	if err != nil {
		t.Fatalf("ParseEPUB failed: %v", err)
	}

	if len(content.Chapters) == 0 {
		t.Fatal("Expected at least 1 chapter, got 0")
	}

	ch := content.Chapters[0]
	if ch.Title != "Chapter 1" {
		t.Errorf("Expected title 'Chapter 1', got %q", ch.Title)
	}

	if len(ch.Lines) == 0 {
		t.Fatal("Expected lines in chapter, got 0")
	}

	t.Logf("Chapter title: %q", ch.Title)
	t.Logf("Lines count: %d", len(ch.Lines))
	for i, line := range ch.Lines {
		t.Logf("Line %d: %q", i, line)
	}
}

func TestParseEPUBRealFile(t *testing.T) {
	// Test with real file if exists
	data, err := os.ReadFile("/tmp/test_book.epub")
	if err != nil {
		t.Skip("No real test EPUB found:", err)
	}

	content, err := ParseEPUB(data)
	if err != nil {
		t.Fatalf("ParseEPUB failed: %v", err)
	}

	t.Logf("Chapters: %d", len(content.Chapters))
	for i, ch := range content.Chapters {
		t.Logf("Chapter %d: %q - %d lines", i, ch.Title, len(ch.Lines))
		for j, line := range ch.Lines {
			if j < 3 {
				t.Logf("  %q", line)
			}
		}
	}
}

func TestHTMLToText(t *testing.T) {
	html := `<html><head><title>Test</title></head>
<body>
<h1>Header</h1>
<p>First paragraph with text.</p>
<p>Second paragraph.</p>
<script>alert('skip me');</script>
</body></html>`

	lines, title := htmlToText(html)
	
	t.Logf("Title: %q", title)
	t.Logf("Lines (%d):", len(lines))
	for i, line := range lines {
		t.Logf("  %d: %q", i, line)
	}

	if title != "Test" {
		t.Errorf("Expected title 'Test', got %q", title)
	}

	if len(lines) == 0 {
		t.Fatal("Expected lines, got 0")
	}

	// Should contain "Header", "First paragraph", "Second paragraph"
	found := false
	for _, line := range lines {
		if line == "Header" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected 'Header' in lines")
	}
}
