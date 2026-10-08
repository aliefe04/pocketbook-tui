package reader

import (
	"archive/zip"
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/url"
	"path"
	"strings"
)

const maxImageBytes = 8 << 20
const maxImagePixels = 8_000_000
const maxEmbeddedBytes = 32 << 20

// EmbeddedImage is a raster resource at an existing semantic text boundary.
// Text lines and saved paragraph coordinates never include image display rows.
type EmbeddedImage struct {
	LineOffset         int
	After              bool
	Pointer            string
	Name, Alt, Problem string
	Data               []byte
	Width, Height      int
}

type imageResource struct {
	data          []byte
	width, height int
	problem       string
}
type epubImages struct {
	archive   *zip.Reader
	resources map[string]imageResource
	total     int
}

func newEPUBImages(zr *zip.Reader) *epubImages {
	return &epubImages{archive: zr, resources: make(map[string]imageResource)}
}

func imageResourcePath(chapter, source string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(source))
	if err != nil || u.Scheme != "" || u.Host != "" || u.Opaque != "" || strings.HasPrefix(u.Path, "/") || strings.Contains(u.Path, "\\") {
		return "", fmt.Errorf("external or invalid image resource")
	}
	if u.Path == "" {
		return "", fmt.Errorf("image resource is empty")
	}
	name := path.Clean(path.Join(path.Dir(chapter), u.Path))
	if name == ".." || strings.HasPrefix(name, "../") || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("image resource escapes the EPUB")
	}
	return name, nil
}

func (e *epubImages) load(chapter, source string) (string, imageResource) {
	name, err := imageResourcePath(chapter, source)
	if err != nil {
		return "", imageResource{problem: err.Error()}
	}
	if resource, ok := e.resources[name]; ok {
		return name, resource
	}
	resource := imageResource{problem: "image resource is missing"}
	defer func() { e.resources[name] = resource }()
	for _, entry := range e.archive.File {
		if entry.Name != name {
			continue
		}
		if entry.UncompressedSize64 > maxImageBytes || e.total+int(entry.UncompressedSize64) > maxEmbeddedBytes {
			resource.problem = "embedded image exceeds the memory limit"
			return name, resource
		}
		r, err := entry.Open()
		if err != nil {
			resource.problem = "image resource cannot be read"
			return name, resource
		}
		data, readErr := io.ReadAll(io.LimitReader(r, maxImageBytes+1))
		closeErr := r.Close()
		if readErr != nil || closeErr != nil || len(data) > maxImageBytes {
			resource.problem = "image resource cannot be read safely"
			return name, resource
		}
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			resource.problem = "unsupported or damaged image. PNG, JPEG and GIF are supported"
			return name, resource
		}
		if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxImagePixels {
			resource.problem = "image dimensions exceed the memory limit"
			return name, resource
		}
		e.total += len(data)
		resource = imageResource{data: data, width: cfg.Width, height: cfg.Height}
		return name, resource
	}
	return name, resource
}

type imageReference struct {
	source, alt, pointer string
	line                 int
}

func imageNodes(root *srcNode) []*srcNode {
	var nodes []*srcNode
	var visit func(*srcNode, bool)
	var steps []cfiStep
	visit = func(n *srcNode, inBody bool) {
		inBody = inBody || n.name == "body"
		if inBody && (n.name == "img" || n.name == "image") {
			n.imageSteps = append([]cfiStep(nil), steps...)
			nodes = append(nodes, n)
		}
		index := 0
		for _, child := range n.kids {
			if child.kind != srcElement {
				continue
			}
			index++
			steps = append(steps, cfiStep{index: 2 * index})
			visit(child, inBody)
			steps = steps[:len(steps)-1]
		}
	}
	visit(root, false)
	return nodes
}

func (e *epubImages) chapter(refs []imageReference, chapter string, titleHeight, lineCount int) []EmbeddedImage {
	images := make([]EmbeddedImage, 0, len(refs))
	for _, ref := range refs {
		name, resource := e.load(chapter, ref.source)
		line := min(ref.line, max(lineCount-1, 0))
		images = append(images, EmbeddedImage{LineOffset: titleHeight + line, After: ref.line >= lineCount, Pointer: ref.pointer, Name: name, Alt: ref.alt, Problem: resource.problem, Data: resource.data, Width: resource.width, Height: resource.height})
	}
	return images
}
