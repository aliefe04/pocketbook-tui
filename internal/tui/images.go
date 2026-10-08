package tui

import (
	"bytes"
	"fmt"
	"image"
	"math"
	"strconv"
	"strings"

	"github.com/aliefe/pocketbook-tui/internal/reader"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

var terminalDarkBackground = true

func photoBackground(theme string) [3]int {
	if theme == "dark" || (theme == "auto" && terminalDarkBackground) {
		return [3]int{23, 26, 32}
	}
	if theme == "sepia" {
		return [3]int{241, 229, 202}
	}
	return [3]int{250, 251, 253}
}
func pixelRGB(img image.Image, x, y int, background [3]int, gray bool) [3]int {
	r, g, b, a := img.At(x, y).RGBA()
	inv := uint32(65535) - a
	color := [3]int{int((r + uint32(background[0])*257*inv/65535) / 257), int((g + uint32(background[1])*257*inv/65535) / 257), int((b + uint32(background[2])*257*inv/65535) / 257)}
	if gray {
		value := (299*color[0] + 587*color[1] + 114*color[2]) / 1000
		color = [3]int{value, value, value}
	}
	return color
}
func appendColor(buf []byte, kind byte, color [3]int) []byte {
	buf = append(buf, '\x1b', '[', kind, '8', ';', '2', ';')
	for i, value := range color {
		if i > 0 {
			buf = append(buf, ';')
		}
		buf = strconv.AppendInt(buf, int64(value), 10)
	}
	return append(buf, 'm')
}

// Half-block cells carry one upper and one lower pixel. This requires no
// terminal-specific graphics extension or subprocess. Output is cached in flow.
func renderRaster(photo reader.EmbeddedImage, columns, height int, mode, theme string, zoom float64) ([]string, error) {
	if photo.Problem != "" {
		return nil, fmt.Errorf("%s", photo.Problem)
	}
	img, _, err := image.Decode(bytes.NewReader(photo.Data))
	if err != nil {
		return nil, fmt.Errorf("image cannot be decoded")
	}
	return renderPixels(img, columns, height, mode, theme, zoom)
}

func renderPixels(img image.Image, columns, height int, mode, theme string, zoom float64) ([]string, error) {
	bounds := img.Bounds()
	iw, ih := bounds.Dx(), bounds.Dy()
	if iw <= 0 || ih <= 0 {
		return nil, fmt.Errorf("image is empty")
	}
	scale := math.Min(float64(max(columns, 1))/float64(iw), float64(max(height, 1)*2)/float64(ih)) * zoom
	outW := min(max(int(float64(iw)*scale), 1), max(columns*8, 1))
	outH := min(max(int(float64(ih)*scale), 1), max(height*2*8, 2))
	background := photoBackground(theme)
	rows := make([]string, 0, (outH+1)/2)
	for y := 0; y < outH; y += 2 {
		buf := make([]byte, 0, outW*48)
		for x := 0; x < outW; x++ {
			sx := bounds.Min.X + min(int(float64(x)/scale), iw-1)
			sy := bounds.Min.Y + min(int(float64(y)/scale), ih-1)
			lowerY := bounds.Min.Y + min(int(float64(y+1)/scale), ih-1)
			buf = appendColor(buf, '3', pixelRGB(img, sx, sy, background, mode == "grayscale"))
			buf = appendColor(buf, '4', pixelRGB(img, sx, lowerY, background, mode == "grayscale"))
			buf = append(buf, "▀"...)
		}
		buf = append(buf, "\x1b[0m"...)
		rows = append(rows, string(buf))
	}
	return rows, nil
}

func imageRows(photo reader.EmbeddedImage, width, height int, mode, theme string) []string {
	if mode == "hidden" {
		return nil
	}
	caption := cleanBookText(strings.TrimSpace(photo.Alt))
	if caption == "" {
		caption = "Photo"
	}
	var rows []string
	if mode == "color" || mode == "grayscale" {
		pixels, err := renderRaster(photo, width, height, mode, theme, 1)
		if err == nil {
			rows = append(rows, pixels...)
		} else {
			rows = append(rows, wrapText("[Photo unavailable: "+err.Error()+"]", width)...)
		}
	}
	rows = append(rows, truncate("["+caption+"] I:view", width))
	return rows
}

// Dimensions are known from DecodeConfig. Laying out a long illustrated book
// therefore never decodes pictures that have not reached the viewport.
func photoFlowRows(photo reader.EmbeddedImage, width, height int, mode string) []string {
	if mode == "hidden" {
		return nil
	}
	if mode == "captions" {
		return imageRows(photo, width, height, "captions", "auto")
	}
	if photo.Problem != "" {
		return imageRows(photo, width, height, mode, "auto")
	}
	scale := math.Min(float64(width)/float64(max(photo.Width, 1)), float64(height*2)/float64(max(photo.Height, 1)))
	count := (max(int(float64(photo.Height)*scale), 1) + 1) / 2
	rows := make([]string, count+1)
	caption := cleanBookText(strings.TrimSpace(photo.Alt))
	if caption == "" {
		caption = "Photo"
	}
	rows[count] = truncate("["+caption+"] I:view", width)
	return rows
}

type imageAddress struct{ chapter, index int }

func (m *readerModel) availableImages() []imageAddress {
	m.ensureLayout()
	return m.layout.addresses
}
func (m *readerModel) openImage() {
	addresses := m.availableImages()
	if len(addresses) == 0 {
		m.setStatus("This book has no embedded images", false)
		return
	}
	selected := 0
	for i, address := range addresses {
		photo := m.content.Chapters[address.chapter].Images[address.index]
		if address.chapter > m.chapterIdx || (address.chapter == m.chapterIdx && photo.LineOffset >= m.lineOffset) {
			selected = i
			break
		}
		selected = i
	}
	m.showImage = true
	m.imageSelection = selected
	m.imageZoom = 1
	m.imagePan = 0
	m.imageCache = nil
	m.imagePanX = 0
	m.ensureImageCache()
}
func (m *readerModel) ensureImageCache() {
	w, h := termSize(m.width, m.height)
	addresses := m.availableImages()
	if len(addresses) == 0 {
		m.imageCache = nil
		return
	}
	m.imageSelection = min(max(m.imageSelection, 0), len(addresses)-1)
	if m.imageCache != nil && m.imageCacheWidth == w && m.imageCacheHeight == h {
		return
	}
	address := addresses[m.imageSelection]
	photo := m.content.Chapters[address.chapter].Images[address.index]
	mode := m.prefs.ImageMode
	if mode != "grayscale" {
		mode = "color"
	}
	var rows []string
	var err error
	if photo.Problem != "" {
		err = fmt.Errorf("%s", photo.Problem)
	} else {
		if m.imageDecoded == nil || m.imageDecodedAt != address {
			m.imageDecoded, _, err = image.Decode(bytes.NewReader(photo.Data))
			m.imageDecodedAt = address
		}
		if err == nil && m.imageDecoded != nil {
			rows, err = renderPixels(m.imageDecoded, max(w-2, 1), max(h-3, 1), mode, m.prefs.Theme, m.imageZoom)
		}
	}
	if err != nil {
		rows = wrapText("Photo unavailable: "+err.Error(), max(w-2, 1))
	}
	m.imageCache = rows
	m.imageCacheWidth, m.imageCacheHeight = w, h
}

func (m *readerModel) imageView() string {
	w, h := termSize(m.width, m.height)
	addresses := m.availableImages()
	if len(addresses) == 0 {
		return frame(w, h, []string{titleRow(w, "Images", "")}, []string{"No embedded images"}, []string{"esc:reading"})
	}
	address := addresses[m.imageSelection]
	photo := m.content.Chapters[address.chapter].Images[address.index]
	capacity := max(h-3, 1)
	y := min(max(m.imagePan, 0), max(len(m.imageCache)-capacity, 0))
	x := max(m.imagePanX, 0)
	if len(m.imageCache) > 0 {
		x = min(x, max(ansi.StringWidth(m.imageCache[0])-w, 0))
	}
	body := make([]string, 0, capacity)
	for _, row := range m.imageCache[y:min(y+capacity, len(m.imageCache))] {
		body = append(body, ansi.Cut(row, x, x+w))
	}
	caption := cleanBookText(photo.Alt)
	if caption == "" {
		caption = "Photo"
	}
	footer := []string{"n/p photo · +/- zoom", "arrows pan · esc read"}
	if w <= 25 {
		footer = []string{"n/p photo +/- zoom", "arrows pan esc back"}
	}
	for i := range footer {
		footer[i] = truncate(footer[i], w)
	}
	return frame(w, h, []string{titleRow(w, caption, fmt.Sprintf("%d/%d", m.imageSelection+1, len(addresses)))}, body, footer)
}

func (m readerModel) handleImageKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "q", "esc", "I":
		m.showImage = false
		m.imageDecoded = nil
		m.imageCache = nil
		return m, nil
	case "ctrl+c":
		m.invalidate()
		return m, tea.Quit
	case "+", "=":
		m.imageZoom = math.Min(m.imageZoom*1.5, 8)
		m.imageCache = nil
	case "-":
		m.imageZoom = math.Max(m.imageZoom/1.5, 1)
		m.imageCache = nil
	case "0":
		m.imageZoom = 1
		m.imagePan, m.imagePanX = 0, 0
		m.imageCache = nil
	case "n", "p":
		delta := 1
		if key == "p" {
			delta = -1
		}
		m.imageSelection = min(max(m.imageSelection+delta, 0), max(len(m.availableImages())-1, 0))
		m.imageCache = nil
		m.imagePan, m.imagePanX = 0, 0
	case "down", "j":
		m.imagePan++
	case "up", "k":
		m.imagePan = max(m.imagePan-1, 0)
	case "right", "l":
		m.imagePanX += 2
	case "left", "h":
		m.imagePanX = max(m.imagePanX-2, 0)
	}
	m.ensureImageCache()
	_, h := termSize(m.width, m.height)
	m.imagePan = min(m.imagePan, max(len(m.imageCache)-max(h-3, 1), 0))
	if len(m.imageCache) > 0 {
		m.imagePanX = min(m.imagePanX, max(ansi.StringWidth(m.imageCache[0])-m.width, 0))
	}
	return m, nil
}

func (m *readerModel) locateImage(chapter, index int) {
	m.ensureLayout()
	for i, row := range m.layout.rows {
		if row.photo && row.chapter == chapter && row.imageIndex == index {
			m.setRow(i)
			return
		}
	}
}
