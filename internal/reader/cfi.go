package reader

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// errNoPointer is reported when a book has no EPUB pointer to resolve.
var errNoPointer = errors.New("no EPUB pointer in the bookmark")

// cfiStep is one step of an EPUB CFI path. Even indexes name the element
// child at index/2 (1-based). Odd indexes name the text chunk between element
// children, with index (2k+1) meaning the chunk after the k-th element.
// offset is the UTF-16 code-unit offset given after a text chunk, or zero. id
// is the ID assertion on an element step, or empty when there is none.
type cfiStep struct {
	index  int
	offset int
	id     string
}

// epubPointer is a parsed EPUB CFI that points into one spine item. Only the
// parts the reader can use are kept. Text assertions and character offsets are
// checked for syntax, then ignored, because the reader works in whole lines.
type epubPointer struct {
	spineStep int       // package child step of the spine element, always /6
	itemref   int       // step of the spine item inside the spine, e.g. 24
	itemrefID string    // ID assertion on the spine item, or empty
	steps     []cfiStep // content document steps, starting at the root element
}

// parseEPUBPointer parses a pointer such as "#epubcfi(/6/24!/4/4/1)". A leading
// "#" is accepted. A range "epubcfi(P,S,E)" is read as its start, P+S.
// Anything the reader does not support is an error, so the caller can fall
// back instead of guessing a location.
func parseEPUBPointer(pointer string) (epubPointer, error) {
	s := strings.TrimPrefix(strings.TrimSpace(pointer), "#")
	const prefix = "epubcfi("
	if !strings.HasPrefix(s, prefix) || !strings.HasSuffix(s, ")") {
		return epubPointer{}, fmt.Errorf("not an EPUB CFI: %q", pointer)
	}
	inner := s[len(prefix) : len(s)-1]

	if parts := splitTopLevel(inner, ','); len(parts) == 3 {
		inner = parts[0] + parts[1]
	} else if len(parts) != 1 {
		return epubPointer{}, fmt.Errorf("unsupported EPUB CFI range: %q", pointer)
	}

	halves := splitTopLevel(inner, '!')
	if len(halves) != 2 {
		return epubPointer{}, fmt.Errorf("EPUB CFI has no content document path: %q", pointer)
	}

	pkg, err := parseCFIPath(halves[0])
	if err != nil {
		return epubPointer{}, err
	}
	// The spine is the sixth child of the package in every EPUB the reader
	// locates. It is followed by an even itemref step.
	if len(pkg) != 2 || pkg[0].index != 6 || pkg[1].index%2 != 0 {
		return epubPointer{}, fmt.Errorf("unsupported EPUB CFI package path: %q", halves[0])
	}

	steps, err := parseCFIPath(halves[1])
	if err != nil {
		return epubPointer{}, err
	}
	if len(steps) == 0 {
		return epubPointer{}, fmt.Errorf("EPUB CFI has no content path: %q", pointer)
	}
	for i, st := range steps {
		if st.index%2 == 1 && i != len(steps)-1 {
			return epubPointer{}, fmt.Errorf("EPUB CFI text step is not last: %q", pointer)
		}
	}

	return epubPointer{
		spineStep: pkg[0].index,
		itemref:   pkg[1].index,
		itemrefID: pkg[1].id,
		steps:     steps,
	}, nil
}

// parseCFIPath parses a sequence of steps such as "/4[body]/10/3:12".
// A character offset is allowed only after a text chunk step.
func parseCFIPath(s string) ([]cfiStep, error) {
	var steps []cfiStep
	i := 0
	for i < len(s) {
		if s[i] != '/' {
			return nil, fmt.Errorf("expected '/' in EPUB CFI path %q", s)
		}
		i++

		start := i
		for i < len(s) && isDigit(s[i]) {
			i++
		}
		n, err := strconv.Atoi(s[start:i])
		if i == start || err != nil || n < 1 {
			return nil, fmt.Errorf("invalid step in EPUB CFI path %q", s)
		}

		var err2 error
		start = i
		if i, err2 = skipAssertion(s, i); err2 != nil {
			return nil, err2
		}
		id := ""
		if i > start {
			id = assertionID(s[start+1 : i-1])
		}

		offset := 0
		if i < len(s) && s[i] == ':' {
			if n%2 == 0 {
				return nil, fmt.Errorf("character offset on element step in %q", s)
			}
			i++
			start = i
			for i < len(s) && isDigit(s[i]) {
				i++
			}
			if i == start {
				return nil, fmt.Errorf("missing character offset in %q", s)
			}
			if offset, err = strconv.Atoi(s[start:i]); err != nil {
				return nil, fmt.Errorf("character offset out of range in %q", s)
			}
			if i, err2 = skipAssertion(s, i); err2 != nil {
				return nil, err2
			}
		}

		if i < len(s) && s[i] != '/' {
			return nil, fmt.Errorf("unsupported EPUB CFI syntax %q", s[i:])
		}
		steps = append(steps, cfiStep{index: n, offset: offset, id: id})
	}
	return steps, nil
}

// skipAssertion skips an optional bracketed assertion that starts at i. Inside
// it, "^" escapes the next character. It returns the index after the closing
// bracket, or i itself when there is no assertion.
func skipAssertion(s string, i int) (int, error) {
	if i >= len(s) || s[i] != '[' {
		return i, nil
	}
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '^':
			j++
		case ']':
			return j + 1, nil
		}
	}
	return i, fmt.Errorf("unterminated assertion in EPUB CFI %q", s)
}

// assertionID returns the ID part of an assertion's contents, the text before
// an unescaped ',' or ';'. Escapes are removed. An assertion that holds only
// text context, such as "[;s=b]", has no ID.
func assertionID(raw string) string {
	var b strings.Builder
	for i := 0; i < len(raw); i++ {
		switch c := raw[i]; {
		case c == '^' && i+1 < len(raw):
			i++
			b.WriteByte(raw[i])
		case c == ',' || c == ';':
			return b.String()
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// splitTopLevel splits s at sep, ignoring separators inside assertions.
func splitTopLevel(s string, sep byte) []string {
	var parts []string
	inBracket := false
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inBracket && c == '^':
			i++
		case c == '[':
			inBracket = true
		case c == ']':
			inBracket = false
		case c == sep && !inBracket:
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return append(parts, s[start:])
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

// resolveEPUBTarget returns the node that a content path names. The path starts
// at the root element, so "/4" is the body of an XHTML document with a head and
// a body. A text chunk resolves to its first non-blank text node. When the
// chunk has none, it resolves to the element that follows it, or to the element
// before it at the end of the container. A text chunk after a missing element,
// or an offset past the end of the chunk's text, is an error. An ID assertion on
// an element step must match that element's id.
func resolveEPUBTarget(root *srcNode, steps []cfiStep) (*srcNode, error) {
	cur := root
	for _, st := range steps {
		if st.index%2 == 1 {
			return textChunkTarget(cur, (st.index-1)/2, st.offset)
		}
		child := elementChild(cur, st.index/2-1)
		if child == nil {
			return nil, fmt.Errorf("EPUB CFI step /%d names no element", st.index)
		}
		if st.id != "" && child.id != st.id {
			return nil, fmt.Errorf("EPUB CFI ID assertion %q does not match step /%d", st.id, st.index)
		}
		cur = child
	}
	return cur, nil
}

// elementChild returns the k-th (zero-based) element child of n, or nil.
func elementChild(n *srcNode, k int) *srcNode {
	seen := 0
	for _, c := range n.kids {
		if c.kind != srcElement {
			continue
		}
		if seen == k {
			return c
		}
		seen++
	}
	return nil
}

// textChunkTarget returns the target of the text chunk after the k-th
// (zero-based) element child of n, and checks offset against the length of the
// chunk's text in UTF-16 code units. Comments are not kept in the tree, so they
// do not change the numbering or the text. The chunk exists only when n has at
// least k element children.
func textChunkTarget(n *srcNode, k, offset int) (*srcNode, error) {
	seen := 0
	var first, next, prev *srcNode
	units := 0
	for _, c := range n.kids {
		if c.kind == srcElement {
			if seen == k {
				next = c
				break
			}
			prev = c
			seen++
			continue
		}
		if c.kind != srcText || seen != k {
			continue
		}
		if first == nil && strings.TrimSpace(c.text) != "" {
			first = c
		}
		units += utf16Len(c.text)
	}
	if seen < k {
		return nil, fmt.Errorf("EPUB CFI text step names no text after element %d", k)
	}
	if offset > units {
		return nil, fmt.Errorf("EPUB CFI character offset %d is past the %d-unit text", offset, units)
	}
	switch {
	case first != nil:
		return first, nil
	case next != nil:
		return next, nil
	case prev != nil:
		return prev, nil
	}
	return n, nil
}

// utf16Len returns the length of s in UTF-16 code units without allocating.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}
