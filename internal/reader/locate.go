package reader

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

// srcKind says what a srcNode stands for.
type srcKind uint8

const (
	srcContainer srcKind = iota // the document itself
	srcElement
	srcText
)

// srcNode is an element or text node of one content document. It is built
// from the HTML parse tree, which gives the text the reader shows, or from the
// XML tree, which is what EPUB CFI steps count. Comments, processing
// instructions and the doctype are left out, because CFI does not count them.
type srcNode struct {
	kind srcKind
	name string // lower-case local name of an element
	id   string // id attribute of an element
	text string // character data of a text node
	kids []*srcNode
}

// fromHTML copies the part of an HTML parse tree that the reader uses.
func fromHTML(n *html.Node) *srcNode {
	switch n.Type {
	case html.DocumentNode:
		return &srcNode{kind: srcContainer, kids: copyHTMLChildren(n)}
	case html.ElementNode:
		s := &srcNode{kind: srcElement, name: n.Data, kids: copyHTMLChildren(n)}
		for _, a := range n.Attr {
			if a.Key == "id" && a.Namespace == "" {
				s.id = a.Val
			}
		}
		return s
	case html.TextNode:
		return &srcNode{kind: srcText, text: n.Data}
	}
	return nil
}

func copyHTMLChildren(n *html.Node) []*srcNode {
	var kids []*srcNode
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if s := fromHTML(c); s != nil {
			kids = append(kids, s)
		}
	}
	return kids
}

// parseXHTML builds the XML tree of an XHTML content document and returns its
// root element. The parser is strict about well-formedness. It also accepts
// the HTML named entities that EPUB text commonly uses, such as &nbsp;.
func parseXHTML(data []byte) (*srcNode, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Entity = xml.HTMLEntity
	dec.CharsetReader = func(charset string, input io.Reader) (io.Reader, error) {
		if strings.EqualFold(charset, "utf-8") || strings.EqualFold(charset, "utf8") {
			return input, nil
		}
		return nil, fmt.Errorf("unsupported encoding %q", charset)
	}

	doc := &srcNode{kind: srcContainer}
	open := []*srcNode{doc}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		parent := open[len(open)-1]
		switch t := tok.(type) {
		case xml.StartElement:
			n := &srcNode{kind: srcElement, name: strings.ToLower(t.Name.Local)}
			for _, a := range t.Attr {
				if a.Name.Space == "" && a.Name.Local == "id" {
					n.id = a.Value
				}
			}
			parent.kids = append(parent.kids, n)
			open = append(open, n)
		case xml.EndElement:
			if len(open) > 1 {
				open = open[:len(open)-1]
			}
		case xml.CharData:
			parent.kids = append(parent.kids, &srcNode{kind: srcText, text: string(t)})
		}
	}
	for _, k := range doc.kids {
		if k.kind == srcElement {
			return k, nil
		}
	}
	return nil, errors.New("content document has no root element")
}

// flowWalk turns a srcNode tree into the reader's virtual body lines. It is the
// only code that decides where lines break, so the text the reader shows and
// the source locations of those lines cannot drift apart. With trace set, it
// also records for every line the CFI path of the text that starts the line.
type flowWalk struct {
	trace    bool
	watch    map[*srcNode]bool
	at       map[*srcNode]int
	lines    []string
	cur      strings.Builder
	path     []cfiStep // steps from the root element to the node being walked
	start    []cfiStep // path of the text that starts the pending line
	hasStart bool
	body     int // depth inside body elements; only body text has a location
	flat     []cfiStep
	ends     []int
}

// walkLines flattens root into body lines. For each node in watch that the walk
// reaches inside body, at records the index of the line the node starts on.
func walkLines(root *srcNode, watch map[*srcNode]bool, trace bool) *flowWalk {
	f := &flowWalk{trace: trace, watch: watch}
	if watch != nil {
		f.at = make(map[*srcNode]int, len(watch))
	}
	if root.kind == srcElement {
		f.element(root)
	} else {
		f.children(root)
	}
	f.flush()
	return f
}

func (f *flowWalk) emit(line string) {
	f.lines = append(f.lines, line)
	if f.trace {
		if f.hasStart {
			f.flat = append(f.flat, f.start...)
		}
		f.ends = append(f.ends, len(f.flat))
	}
	f.hasStart = false
}

func (f *flowWalk) flush() {
	if f.cur.Len() == 0 {
		return
	}
	line := strings.TrimSpace(f.cur.String())
	f.cur.Reset()
	f.emit(line)
}

// children walks the children of n. A text chunk is the text between element
// children: its CFI step is 2k+1, where k is the number of element children
// before it, and its offset counts UTF-16 units from the start of the chunk.
func (f *flowWalk) children(n *srcNode) {
	elem := 0
	chunk := 0
	for _, c := range n.kids {
		switch c.kind {
		case srcElement:
			elem++
			chunk = 0
			f.path = append(f.path, cfiStep{index: 2 * elem})
			f.element(c)
			f.path = f.path[:len(f.path)-1]
		case srcText:
			f.text(c, 2*elem+1, chunk)
			chunk += utf16Len(c.text)
		}
	}
}

// text adds the character data of n to the pending line. index is the CFI step
// of the chunk n belongs to, and before counts the UTF-16 units of that chunk
// that come before n.
func (f *flowWalk) text(n *srcNode, index, before int) {
	if f.watch[n] && f.body > 0 {
		f.at[n] = len(f.lines)
	}
	t := strings.TrimSpace(n.text)
	if t == "" {
		return
	}
	if f.cur.Len() > 0 {
		f.cur.WriteByte(' ')
	} else if f.trace && f.body > 0 {
		lead := len(n.text) - len(strings.TrimLeftFunc(n.text, unicode.IsSpace))
		f.start = append(f.start[:0], f.path...)
		f.start = append(f.start, cfiStep{index: index, offset: before + utf16Len(n.text[:lead])})
		f.hasStart = true
	}
	f.cur.WriteString(t)
}

func (f *flowWalk) element(n *srcNode) {
	switch n.name {
	case "script", "style", "nav":
		return
	}
	if isBlockElement(n.name) {
		f.flush()
	}
	isBody := n.name == "body"
	if isBody {
		f.body++
	}
	if f.watch[n] && f.body > 0 {
		f.at[n] = len(f.lines)
	}
	f.children(n)
	if isBlockElement(n.name) {
		f.flush()
		if isHeaderElement(n.name) {
			f.emit("")
		}
	}
	if isBody {
		f.body--
	}
}

// anchorTable gives the EPUB source of every body line of one chapter, so a
// line can be written as a CFI without keeping the XML tree. The paths of all
// lines share one slice: line i's path is steps[ends[i-1]:ends[i]], and an
// empty path means the line has no location in the body.
type anchorTable struct {
	itemref int // package child step of the spine item, e.g. 24
	steps   []cfiStep
	ends    []int
}

func (a *anchorTable) path(line int) []cfiStep {
	start := 0
	if line > 0 {
		start = a.ends[line-1]
	}
	return a.steps[start:a.ends[line]]
}

// nearest returns the line that stands for line. That is line itself when it
// has a location, else the closest located line before it, else the first
// located line after it. Title chrome and head-only text therefore stand for
// the first body paragraph. ok is false when no line has a location.
func (a *anchorTable) nearest(line int) (int, bool) {
	n := len(a.ends)
	if n == 0 {
		return 0, false
	}
	line = min(max(line, 0), n-1)
	for i := line; i >= 0; i-- {
		if len(a.path(i)) > 0 {
			return i, true
		}
	}
	for i := line + 1; i < n; i++ {
		if len(a.path(i)) > 0 {
			return i, true
		}
	}
	return 0, false
}

// pointer returns the EPUB CFI that names line, which must have a location.
func (a *anchorTable) pointer(line int) string {
	var b strings.Builder
	b.WriteString("#epubcfi(/6/")
	b.WriteString(strconv.Itoa(a.itemref))
	b.WriteByte('!')
	for _, st := range a.path(line) {
		b.WriteByte('/')
		b.WriteString(strconv.Itoa(st.index))
		if st.index%2 == 1 {
			b.WriteByte(':')
			b.WriteString(strconv.Itoa(st.offset))
		}
	}
	b.WriteByte(')')
	return b.String()
}

// PointerAt returns the EPUB CFI for the paragraph shown at virtual line
// lineOffset of chapter chapterIdx, as "#epubcfi(...)". Title chrome, and
// lines that come from outside the body, stand for the nearest body paragraph.
// ok is false when the chapter has no EPUB source, as for TXT books, or when
// its markup could not be matched exactly. The caller must then not write a
// pointer for it.
func (bc *BookContent) PointerAt(chapterIdx, lineOffset int) (string, bool) {
	if chapterIdx < 0 || chapterIdx >= len(bc.Chapters) {
		return "", false
	}
	ch := &bc.Chapters[chapterIdx]
	if ch.anchors == nil {
		return "", false
	}
	line, ok := ch.anchors.nearest(lineOffset - ch.TitleHeight())
	if !ok {
		return "", false
	}
	return ch.anchors.pointer(line), true
}

// xhtmlChapter is one content document read for the reader.
type xhtmlChapter struct {
	lines     []string
	title     string
	anchors   *anchorTable // nil when the XML tree gives different lines
	line      int          // body line that the pointer names, or -1
	locateErr error        // why the pointer did not resolve
}

// readXHTML reads one content document. steps is the content path of a pointer
// into this document, or nil. itemref is the document's package step. The text
// and title come from the HTML parse, as they always have. The locations come
// from the XML tree, which is what CFI steps count. Both trees must give the
// same lines. If they do not, the chapter has no locations, and a pointer into
// it is reported as unresolved so that the caller can fall back.
func readXHTML(data []byte, steps []cfiStep, itemref int) xhtmlChapter {
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return xhtmlChapter{line: -1, locateErr: fmt.Errorf("parse document: %w", err)}
	}
	res := xhtmlChapter{title: extractTitle(doc), line: -1}
	res.lines = walkLines(fromHTML(doc), nil, false).lines

	root, err := parseXHTML(data)
	if err != nil {
		if steps != nil {
			res.locateErr = fmt.Errorf("content document is not well-formed XML: %w", err)
		}
		return res
	}

	var target *srcNode
	var resolveErr error
	if steps != nil {
		target, resolveErr = resolveEPUBTarget(root, steps)
	}
	var watch map[*srcNode]bool
	if target != nil {
		watch = map[*srcNode]bool{target: true}
	}
	walked := walkLines(root, watch, true)
	if !slices.Equal(walked.lines, res.lines) {
		if steps != nil {
			res.locateErr = errors.New("pointer cannot be placed exactly in this document")
		}
		return res
	}
	res.anchors = &anchorTable{itemref: itemref, steps: walked.flat, ends: walked.ends}

	switch {
	case steps == nil:
	case resolveErr != nil:
		res.locateErr = resolveErr
	default:
		pos, ok := walked.at[target]
		switch {
		case !ok:
			res.locateErr = errors.New("pointer names content outside the readable text")
		case len(res.lines) == 0:
			res.locateErr = errors.New("spine item has no readable text")
		default:
			res.line = min(pos, len(res.lines)-1)
		}
	}
	return res
}
