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
	if len(data) > maxBookSize {
		return nil, fmt.Errorf("book too large: %d MB (max %d MB)", len(data)/(1024*1024), maxBookSize/(1024*1024))
	}

	r := bytes.NewReader(data)
	zr, err := zip.NewReader(r, int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("open epub zip: %w", err)
	}

	// Step 1: Find OPF path from META-INF/container.xml
	opfPath, err := findOPFPath(zr)
	if err != nil {
		return nil, err
	}

	// Step 2: Parse OPF to get manifest and spine
	manifest, spine, err := parseOPF(zr, opfPath)
	if err != nil {
		return nil, err
	}

	// Step 3: Read spine items in order
	var chapters []Chapter
	for _, itemID := range spine {
		href, ok := manifest[itemID]
		if !ok {
			continue
		}
		// Resolve relative path from OPF directory
		opfDir := filepath.Dir(opfPath)
		contentPath := filepath.Join(opfDir, href)
		contentPath = filepath.ToSlash(contentPath) // normalize for zip lookup

		text, title, err := extractChapterText(zr, contentPath)
		if err != nil {
			continue // skip problematic chapters
		}

		chapters = append(chapters, Chapter{
			Title: title,
			Lines: text,
		})
	}

	if len(chapters) == 0 {
		return nil, fmt.Errorf("no readable chapters found")
	}

	return &BookContent{Chapters: chapters}, nil
}

func findOPFPath(zr *zip.Reader) (string, error) {
	for _, f := range zr.File {
		if f.Name == "META-INF/container.xml" {
			rc, err := f.Open()
			if err != nil {
				return "", err
			}
			defer rc.Close()

			data, err := io.ReadAll(rc)
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

func parseOPF(zr *zip.Reader, opfPath string) (manifest map[string]string, spine []string, err error) {
	for _, f := range zr.File {
		if f.Name == opfPath {
			rc, err := f.Open()
			if err != nil {
				return nil, nil, err
			}
			defer rc.Close()

			data, err := io.ReadAll(rc)
			if err != nil {
				return nil, nil, err
			}

			var opf struct {
				Manifest struct {
					Items []struct {
						ID   string `xml:"id,attr"`
						Href string `xml:"href,attr"`
					} `xml:"item"`
				} `xml:"manifest"`
				Spine struct {
					Itemrefs []struct {
						IDRef string `xml:"idref,attr"`
					} `xml:"itemref"`
				} `xml:"spine"`
			}
			if err := xml.Unmarshal(data, &opf); err != nil {
				return nil, nil, err
			}

			manifest = make(map[string]string)
			for _, item := range opf.Manifest.Items {
				manifest[item.ID] = item.Href
			}
			for _, ref := range opf.Spine.Itemrefs {
				spine = append(spine, ref.IDRef)
			}
			return manifest, spine, nil
		}
	}
	return nil, nil, fmt.Errorf("OPF file not found: %s", opfPath)
}

func extractChapterText(zr *zip.Reader, contentPath string) ([]string, string, error) {
	for _, f := range zr.File {
		if f.Name == contentPath {
			rc, err := f.Open()
			if err != nil {
				return nil, "", err
			}
			defer rc.Close()

			data, err := io.ReadAll(rc)
			if err != nil {
				return nil, "", err
			}

			text, title := htmlToText(string(data))
			return text, title, nil
		}
	}
	return nil, "", fmt.Errorf("content file not found: %s", contentPath)
}

func htmlToText(htmlStr string) ([]string, string) {
	doc, err := html.Parse(strings.NewReader(htmlStr))
	if err != nil {
		return nil, ""
	}

	// First pass: extract title
	title := extractTitle(doc)

	// Second pass: extract body text
	textLines := extractBodyText(doc)

	return textLines, title
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

func extractBodyText(n *html.Node) []string {
	var lines []string
	var current strings.Builder

	var walk func(*html.Node, bool)
	walk = func(n *html.Node, skip bool) {
		if n.Type == html.ElementNode {
			// Skip script and style tags entirely
			if n.Data == "script" || n.Data == "style" || n.Data == "nav" {
				return
			}

			// Flush on block elements
			if isBlockElement(n.Data) {
				if current.Len() > 0 {
					lines = append(lines, strings.TrimSpace(current.String()))
					current.Reset()
				}
			}
		}

		if n.Type == html.TextNode && !skip {
			text := strings.TrimSpace(n.Data)
			if text != "" {
				if current.Len() > 0 {
					current.WriteString(" ")
				}
				current.WriteString(text)
			}
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, skip)
		}

		if n.Type == html.ElementNode {
			// Flush after block elements
			if isBlockElement(n.Data) {
				if current.Len() > 0 {
					lines = append(lines, strings.TrimSpace(current.String()))
					current.Reset()
				}
				if isHeaderElement(n.Data) {
					lines = append(lines, "")
				}
			}
		}
	}

	walk(n, false)

	if current.Len() > 0 {
		lines = append(lines, strings.TrimSpace(current.String()))
	}

	return lines
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
