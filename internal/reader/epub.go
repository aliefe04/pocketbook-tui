package reader

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"golang.org/x/net/html"
)

const maxBookSize = 200 * 1024 * 1024 // 200MB

// ParseEPUB parses an EPUB file from a byte slice and extracts text content.
func ParseEPUB(data []byte) (*BookContent, error) {
	content, _, err := parseEPUB(data, nil)
	return content, err
}

// Bookmark is an EPUB location resolved to the reader's virtual lines.
type Bookmark struct {
	Chapter    int   // index into BookContent.Chapters
	LineOffset int   // virtual line within the chapter, title chrome included
	Err        error // why the pointer could not be resolved; nil when it was
}

// ParseEPUBAt parses an EPUB like ParseEPUB and resolves an EPUB CFI pointer,
// such as "#epubcfi(/6/24!/4/4/1)", to the paragraph it names. The error return
// is only for a book that cannot be parsed. A pointer that cannot be resolved
// is reported in Bookmark.Err, and the content is still returned.
func ParseEPUBAt(data []byte, pointer string) (*BookContent, Bookmark, error) {
	if strings.TrimSpace(pointer) == "" {
		content, _, err := parseEPUB(data, nil)
		return content, Bookmark{Err: errNoPointer}, err
	}
	ptr, err := parseEPUBPointer(pointer)
	if err != nil {
		content, _, perr := parseEPUB(data, nil)
		return content, Bookmark{Err: err}, perr
	}
	return parseEPUB(data, &ptr)
}

func parseEPUB(data []byte, ptr *epubPointer) (*BookContent, Bookmark, error) {
	if len(data) > maxBookSize {
		return nil, Bookmark{}, fmt.Errorf("book too large: %d MB (max %d MB)", len(data)/(1024*1024), maxBookSize/(1024*1024))
	}

	r := bytes.NewReader(data)
	zr, err := zip.NewReader(r, int64(len(data)))
	if err != nil {
		return nil, Bookmark{}, fmt.Errorf("open epub zip: %w", err)
	}

	// Step 1: Find OPF path from META-INF/container.xml
	opfPath, err := findOPFPath(zr)
	if err != nil {
		return nil, Bookmark{}, err
	}

	// Step 2: Parse OPF to get manifest and spine
	opf, err := parseOPF(zr, opfPath)
	if err != nil {
		return nil, Bookmark{}, err
	}

	bm := Bookmark{Err: errNoPointer}
	if ptr != nil {
		bm = Bookmark{Err: fmt.Errorf("spine item /%d not found in the package", ptr.itemref)}
		if ptr.spineStep != opf.spineStep {
			bm = Bookmark{Err: fmt.Errorf("EPUB CFI package path does not name the spine")}
		}
	}

	// Step 3: Read spine items in order. Each item keeps its package step, so a
	// skipped item does not shift the pointer.
	opfDir := filepath.Dir(opfPath)
	var chapters []Chapter
	for _, item := range opf.spine {
		href, ok := opf.manifest[item.idref]
		if !ok {
			continue
		}
		contentPath := filepath.ToSlash(filepath.Join(opfDir, href))

		located := ptr != nil && item.step == ptr.itemref
		if located && !item.matchesID(ptr.itemrefID) {
			bm = Bookmark{Err: fmt.Errorf("spine item /%d does not match the pointer's ID", item.step)}
			located = false
		}
		var steps []cfiStep
		if located {
			steps = ptr.steps
		}

		xc, err := extractChapterText(zr, contentPath, steps, item.step)
		if err != nil {
			if located {
				bm = Bookmark{Err: fmt.Errorf("spine item /%d unreadable: %w", item.step, err)}
			}
			continue // skip problematic chapters
		}
		ch := Chapter{Title: xc.title, Lines: xc.lines, anchors: xc.anchors}
		if opf.spineStep != 6 {
			// Pointers are written and read as /6/…, so a package with its spine
			// elsewhere has no exact location to write.
			ch.anchors = nil
		}
		if located {
			if xc.locateErr != nil {
				bm = Bookmark{Chapter: len(chapters), Err: xc.locateErr}
			} else {
				bm = Bookmark{Chapter: len(chapters), LineOffset: ch.TitleHeight() + xc.line}
			}
		}
		chapters = append(chapters, ch)
	}

	if len(chapters) == 0 {
		return nil, bm, fmt.Errorf("no readable chapters found")
	}

	return &BookContent{Chapters: chapters}, bm, nil
}

func findOPFPath(zr *zip.Reader) (string, error) {
	for _, f := range zr.File {
		if f.Name == "META-INF/container.xml" {
			data, err := readZipFile(f)
			if err != nil {
				return "", err
			}

			var container struct {
				Rootfiles struct {
					Rootfile []struct {
						FullPath string `xml:"full-path,attr"`
					} `xml:"rootfile"`
				} `xml:"rootfiles"`
			}
			if err := xml.Unmarshal(data, &container); err != nil {
				return "", err
			}
			if len(container.Rootfiles.Rootfile) > 0 {
				return container.Rootfiles.Rootfile[0].FullPath, nil
			}
		}
	}
	return "", fmt.Errorf("META-INF/container.xml not found")
}

// opfPackage is the part of the package document that the reader uses.
type opfPackage struct {
	manifest  map[string]string
	spineStep int         // package child step of the spine element, normally 6
	spine     []spineItem // in reading order
}

// spineItem is one itemref. step is its child step inside the spine element,
// counting every element child, so a pointer's step matches the file even when
// the spine holds other elements.
type spineItem struct {
	step  int
	id    string
	idref string
}

// matchesID reports whether an ID assertion on a pointer's spine item holds. An
// empty assertion always holds. The ID may name the itemref or the item it
// refers to.
func (s spineItem) matchesID(id string) bool {
	return id == "" || id == s.id || id == s.idref
}

func parseOPF(zr *zip.Reader, opfPath string) (opfPackage, error) {
	for _, f := range zr.File {
		if f.Name == opfPath {
			data, err := readZipFile(f)
			if err != nil {
				return opfPackage{}, err
			}
			return decodeOPF(data)
		}
	}
	return opfPackage{}, fmt.Errorf("OPF file not found: %s", opfPath)
}

// decodeOPF reads the manifest and the spine from a package document. Child
// steps count every element child in document order, as CFI does.
func decodeOPF(data []byte) (opfPackage, error) {
	var pkg struct {
		Children []struct {
			XMLName xml.Name
			Inner   []byte `xml:",innerxml"`
		} `xml:",any"`
	}
	if err := xml.Unmarshal(data, &pkg); err != nil {
		return opfPackage{}, err
	}

	opf := opfPackage{manifest: make(map[string]string)}
	for k, child := range pkg.Children {
		switch child.XMLName.Local {
		case "manifest":
			var m struct {
				Items []struct {
					ID   string `xml:"id,attr"`
					Href string `xml:"href,attr"`
				} `xml:"item"`
			}
			if err := unmarshalInner(child.Inner, "manifest", &m); err != nil {
				return opfPackage{}, err
			}
			for _, item := range m.Items {
				opf.manifest[item.ID] = item.Href
			}
		case "spine":
			opf.spineStep = 2 * (k + 1)
			var s struct {
				Children []struct {
					XMLName xml.Name
					ID      string `xml:"id,attr"`
					IDRef   string `xml:"idref,attr"`
				} `xml:",any"`
			}
			if err := unmarshalInner(child.Inner, "spine", &s); err != nil {
				return opfPackage{}, err
			}
			for j, c := range s.Children {
				if c.XMLName.Local == "itemref" {
					opf.spine = append(opf.spine, spineItem{step: 2 * (j + 1), id: c.ID, idref: c.IDRef})
				}
			}
		}
	}
	return opf, nil
}

// unmarshalInner decodes the children of an element whose inner XML is given.
func unmarshalInner(inner []byte, name string, v any) error {
	wrapped := append(append([]byte("<"+name+">"), inner...), []byte("</"+name+">")...)
	return xml.Unmarshal(wrapped, v)
}

func readZipFile(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

func extractChapterText(zr *zip.Reader, contentPath string, steps []cfiStep, itemref int) (xhtmlChapter, error) {
	for _, f := range zr.File {
		if f.Name == contentPath {
			data, err := readZipFile(f)
			if err != nil {
				return xhtmlChapter{}, err
			}
			return readXHTML(data, steps, itemref), nil
		}
	}
	return xhtmlChapter{}, fmt.Errorf("content file not found: %s", contentPath)
}

// htmlToText returns the body lines and title of an HTML document, as the
// reader lays them out.
func htmlToText(htmlStr string) ([]string, string) {
	doc, err := html.Parse(strings.NewReader(htmlStr))
	if err != nil {
		return nil, ""
	}
	return walkLines(fromHTML(doc), nil, false).lines, extractTitle(doc)
}

func extractTitle(n *html.Node) string {
	if n.Type == html.ElementNode && n.Data == "title" {
		if n.FirstChild != nil && n.FirstChild.Type == html.TextNode {
			return strings.TrimSpace(n.FirstChild.Data)
		}
	}

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if t := extractTitle(c); t != "" {
			return t
		}
	}
	return ""
}

func isBlockElement(tag string) bool {
	switch tag {
	case "p", "div", "h1", "h2", "h3", "h4", "h5", "h6",
		"li", "tr", "td", "th", "blockquote", "pre",
		"br", "hr", "section", "article", "aside":
		return true
	}
	return false
}

func isHeaderElement(tag string) bool {
	switch tag {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		return true
	}
	return false
}
