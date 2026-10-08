package tui

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/aliefe/pocketbook-tui/internal/reader"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

var terminalDarkBackground = true

var (
	launchViewerHook func(cmd *exec.Cmd) error
	trackedTempFiles []string
	tempFilesMu      sync.Mutex
	tempFilesClosed  bool
)

func resetOriginalExports() {
	tempFilesMu.Lock()
	tempFilesClosed = false
	tempFilesMu.Unlock()
}
func createOriginalExport() (*os.File, error) {
	tempFilesMu.Lock()
	defer tempFilesMu.Unlock()
	if tempFilesClosed {
		return nil, fmt.Errorf("reader session has closed")
	}
	file, err := os.CreateTemp("", "pbtui-photo-*.png")
	if err != nil {
		return nil, err
	}
	trackedTempFiles = append(trackedTempFiles, file.Name())
	return file, nil
}
func originalSessionClosed() bool {
	tempFilesMu.Lock()
	defer tempFilesMu.Unlock()
	return tempFilesClosed
}

func cleanupTempFiles() {
	tempFilesMu.Lock()
	defer tempFilesMu.Unlock()
	tempFilesClosed = true
	for _, f := range trackedTempFiles {
		_ = os.Remove(f)
	}
	trackedTempFiles = nil
}

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
	color := [3]int{
		int((r + uint32(background[0])*257*inv/65535) / 257),
		int((g + uint32(background[1])*257*inv/65535) / 257),
		int((b + uint32(background[2])*257*inv/65535) / 257),
	}
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

// calcInlineImageGeometry computes aspect-correct columns and rows for inline placement.
func calcInlineImageGeometry(cols, maxHeight, imgW, imgH int, cellW, cellH int) (dispCols, dispRows int) {
	if imgW <= 0 || imgH <= 0 {
		return max(cols, 1), 1
	}
	if cellW <= 0 || cellH <= 0 {
		cellW = 8
		cellH = 16
	}
	limit := 60
	if maxHeight > 0 {
		limit = min(maxHeight, 60)
	}
	cellAspect := float64(cellW) / float64(cellH)
	targetRows := int(math.Round(float64(cols) * cellAspect * float64(imgH) / float64(imgW)))
	if targetRows > limit {
		dispRows = limit
		wScale := float64(limit) * float64(cellH) * float64(imgW) / (float64(cellW) * float64(imgH))
		dispCols = min(max(int(math.Round(wScale)), 1), cols)
	} else {
		dispRows = max(targetRows, 1)
		dispCols = cols
	}
	return dispCols, dispRows
}

// calcImageRows computes aspect-correct terminal rows for inline image placement.
// Shared by photoFlowRows and imageRows to prevent row skips.
func calcImageRows(cols, maxHeight, imgW, imgH int, cellW, cellH int, native bool) int {
	if imgW <= 0 || imgH <= 0 {
		return 1
	}
	if native {
		_, rows := calcInlineImageGeometry(cols, maxHeight, imgW, imgH, cellW, cellH)
		return rows
	}
	limit := 60
	if maxHeight > 0 {
		limit = min(maxHeight, 60)
	}
	scale := math.Min(float64(max(cols, 1))/float64(imgW), float64(limit*2)/float64(imgH))
	target := (max(int(float64(imgH)*scale), 1) + 1) / 2
	return min(max(target, 1), limit)
}

type viewerGeometry struct {
	dispCols, dispRows int
	cropCols, cropRows int
	cropX, cropY       int
	cropW, cropH       int
	colPos, rowPos     int
}

func calcViewerGeometry(w, capacity, imgW, imgH, cellW, cellH int, zoom float64, panX, panY int) viewerGeometry {
	if imgW <= 0 || imgH <= 0 || cellW <= 0 || cellH <= 0 {
		return viewerGeometry{dispCols: 1, dispRows: 1, cropCols: 1, cropRows: 1, cropW: 1, cropH: 1, colPos: 1, rowPos: 2}
	}
	vpW := float64(w * cellW)
	vpH := float64(capacity * cellH)
	scale := math.Min(vpW/float64(imgW), vpH/float64(imgH)) * zoom
	dispCols := max(int(math.Round(float64(imgW)*scale/float64(cellW))), 1)
	dispRows := max(int(math.Round(float64(imgH)*scale/float64(cellH))), 1)

	cropCols := min(dispCols, w)
	cropRows := min(dispRows, capacity)

	clampedPanX := min(max(panX, 0), max(dispCols-w, 0))
	clampedPanY := min(max(panY, 0), max(dispRows-capacity, 0))

	cropX := int(math.Round(float64(clampedPanX) * float64(imgW) / float64(dispCols)))
	cropY := int(math.Round(float64(clampedPanY) * float64(imgH) / float64(dispRows)))
	cropW := int(math.Round(float64(cropCols) * float64(imgW) / float64(dispCols)))
	cropH := int(math.Round(float64(cropRows) * float64(imgH) / float64(dispRows)))

	cropX = min(max(cropX, 0), imgW-1)
	cropY = min(max(cropY, 0), imgH-1)
	cropW = min(max(cropW, 1), imgW-cropX)
	cropH = min(max(cropH, 1), imgH-cropY)

	rowPos := 2
	colPos := 1
	if cropCols < w {
		colPos = 1 + (w-cropCols)/2
	}
	if cropRows < capacity {
		rowPos = 2 + (capacity-cropRows)/2
	}

	return viewerGeometry{
		dispCols: dispCols,
		dispRows: dispRows,
		cropCols: cropCols,
		cropRows: cropRows,
		cropX:    cropX,
		cropY:    cropY,
		cropW:    cropW,
		cropH:    cropH,
		colPos:   colPos,
		rowPos:   rowPos,
	}
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
	targetHeight := height
	if targetHeight == 0 {
		targetHeight = calcImageRows(columns, 0, photo.Width, photo.Height, 8, 16, false)
	}
	return renderPixels(img, columns, targetHeight, mode, theme, zoom)
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

	// If 1:1 or zoomed in (scale >= 1.0), use exact pixel sampling (lossless at 1:1)
	if scale >= 1.0 {
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

	// Downscaling (scale < 1.0): proper area / box averaging over source pixels
	sampleBox := func(x, y int) [3]int {
		srcX0 := float64(x) * float64(iw) / float64(outW)
		srcX1 := float64(x+1) * float64(iw) / float64(outW)
		srcY0 := float64(y) * float64(ih) / float64(outH)
		srcY1 := float64(y+1) * float64(ih) / float64(outH)

		startX := max(int(math.Floor(srcX0)), 0)
		endX := min(int(math.Ceil(srcX1)), iw)
		startY := max(int(math.Floor(srcY0)), 0)
		endY := min(int(math.Ceil(srcY1)), ih)

		var sumR, sumG, sumB, sumA, sumW float64
		for sy := startY; sy < endY; sy++ {
			y0 := math.Max(float64(sy), srcY0)
			y1 := math.Min(float64(sy+1), srcY1)
			wy := y1 - y0
			if wy <= 0 {
				continue
			}
			for sx := startX; sx < endX; sx++ {
				x0 := math.Max(float64(sx), srcX0)
				x1 := math.Min(float64(sx+1), srcX1)
				wx := x1 - x0
				if wx <= 0 {
					continue
				}
				w := wx * wy
				r, g, b, a := img.At(bounds.Min.X+sx, bounds.Min.Y+sy).RGBA()
				sumR += float64(r) * w
				sumG += float64(g) * w
				sumB += float64(b) * w
				sumA += float64(a) * w
				sumW += w
			}
		}
		if sumW > 0 {
			sumR /= sumW
			sumG /= sumW
			sumB /= sumW
			sumA /= sumW
		}
		r := uint32(sumR + 0.5)
		g := uint32(sumG + 0.5)
		b := uint32(sumB + 0.5)
		a := uint32(sumA + 0.5)
		inv := uint32(65535) - a
		color := [3]int{
			int((r + uint32(background[0])*257*inv/65535) / 257),
			int((g + uint32(background[1])*257*inv/65535) / 257),
			int((b + uint32(background[2])*257*inv/65535) / 257),
		}
		if mode == "grayscale" {
			val := (299*color[0] + 587*color[1] + 114*color[2]) / 1000
			color = [3]int{val, val, val}
		}
		return color
	}

	for y := 0; y < outH; y += 2 {
		buf := make([]byte, 0, outW*48)
		for x := 0; x < outW; x++ {
			topColor := sampleBox(x, y)
			bottomColor := sampleBox(x, min(y+1, outH-1))
			buf = appendColor(buf, '3', topColor)
			buf = appendColor(buf, '4', bottomColor)
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
	if photo.Problem != "" || len(photo.Data) == 0 {
		msg := photo.Problem
		if msg == "" {
			msg = "empty photo data"
		}
		rows = append(rows, wrapText("[Photo unavailable: "+msg+"]", width)...)
	} else if mode == "color" || mode == "grayscale" {
		if isNativeGraphicsActive() {
			cellW, cellH := currentCellGeometry()
			count := calcImageRows(width, height, photo.Width, photo.Height, cellW, cellH, true)
			for range count {
				rows = append(rows, strings.Repeat(" ", width))
			}
		} else {
			pixels, err := renderRaster(photo, width, height, mode, theme, 1)
			if err == nil {
				rows = append(rows, pixels...)
			} else {
				rows = append(rows, wrapText("[Photo unavailable: "+err.Error()+"]", width)...)
			}
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
	native := isNativeGraphicsActive()
	cellW, cellH := currentCellGeometry()
	count := calcImageRows(width, height, photo.Width, photo.Height, cellW, cellH, native)
	rows := make([]string, count+1)
	caption := cleanBookText(strings.TrimSpace(photo.Alt))
	if caption == "" {
		caption = "Photo"
	}
	rows[count] = truncate("["+caption+"] I:view", width)
	return rows
}

func viewerNativeBlankRows(w, capacity, imgW, imgH, cellW, cellH int, zoom float64) []string {
	geom := calcViewerGeometry(w, capacity, imgW, imgH, cellW, cellH, zoom, 0, 0)
	rows := make([]string, geom.dispRows)
	line := strings.Repeat(" ", geom.dispCols)
	for i := range rows {
		rows[i] = line
	}
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
	m.imageNotice = ""
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
	if m.imageCache != nil && m.imageCacheWidth == w && m.imageCacheHeight == h && m.imageCacheNative == isNativeGraphicsActive() {
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
	capacity := max(h-4, 1)
	if photo.Problem != "" {
		err = fmt.Errorf("%s", photo.Problem)
	} else {
		if m.imageDecoded == nil || m.imageDecodedAt != address {
			m.imageDecoded, _, err = image.Decode(bytes.NewReader(photo.Data))
			m.imageDecodedAt = address
		}
		if err == nil && m.imageDecoded != nil {
			if isNativeGraphicsActive() {
				cellW, cellH := currentCellGeometry()
				rows = viewerNativeBlankRows(w, capacity, m.imageDecoded.Bounds().Dx(), m.imageDecoded.Bounds().Dy(), cellW, cellH, m.imageZoom)
			} else {
				rows, err = renderPixels(m.imageDecoded, max(w-2, 1), capacity, mode, m.prefs.Theme, m.imageZoom)
			}
		}
	}
	if err != nil {
		rows = wrapText("Photo unavailable: "+err.Error(), max(w-2, 1))
	}
	m.imageCache = rows
	m.imageCacheWidth, m.imageCacheHeight = w, h
	m.imageCacheNative = isNativeGraphicsActive()
}

func (m *readerModel) imageView() string {
	w, h := termSize(m.width, m.height)
	addresses := m.availableImages()
	if len(addresses) == 0 {
		return frame(w, h, []string{titleRow(w, "Images", "")}, []string{"No embedded images"}, []string{"esc:reading"})
	}
	address := addresses[m.imageSelection]
	photo := m.content.Chapters[address.chapter].Images[address.index]
	capacity := max(h-4, 1)
	y := min(max(m.imagePan, 0), max(len(m.imageCache)-capacity, 0))
	x := max(m.imagePanX, 0)
	if len(m.imageCache) > 0 {
		x = min(x, max(ansi.StringWidth(m.imageCache[0])-w, 0))
	}
	body := make([]string, 0, capacity)
	for _, row := range m.imageCache[y:min(y+capacity, len(m.imageCache))] {
		body = append(body, ansi.Cut(row, x, x+w))
	}
	for len(body) < capacity {
		body = append(body, strings.Repeat(" ", w))
	}
	caption := cleanBookText(photo.Alt)
	if caption == "" {
		caption = "Photo"
	}
	titleRight := fmt.Sprintf("%d/%d", m.imageSelection+1, len(addresses))
	if !isNativeGraphicsActive() {
		titleRight = "[blocks] " + titleRight
	}
	footer := []string{
		truncate("n/p photo · +/- zoom", w),
		truncate("arrows pan · esc read", w),
		truncate("O:open original · 0:fit", w),
	}
	if w <= 25 {
		footer = []string{
			truncate("n/p photo +/- zoom", w),
			truncate("arrows pan esc back", w),
			truncate("O:original 0:fit", w),
		}
	}
	if m.imageNotice != "" {
		footer[len(footer)-1] = truncate(m.imageNotice, w)
	}
	return frame(w, h, []string{titleRow(w, caption, titleRight)}, body, footer)
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
	case "o", "O":
		addresses := m.availableImages()
		if len(addresses) > 0 {
			m.imageSelection = min(max(m.imageSelection, 0), len(addresses)-1)
			address := addresses[m.imageSelection]
			photo := m.content.Chapters[address.chapter].Images[address.index]
			m.imageNotice = "Opening original"
			return m, openOriginalPhotoCmd(photo, m.session)
		}
	}
	m.ensureImageCache()
	_, h := termSize(m.width, m.height)
	capacity := max(h-4, 1)
	m.imagePan = min(m.imagePan, max(len(m.imageCache)-capacity, 0))
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

type openOriginalResultMsg struct {
	err     error
	session uint64
}

func openOriginalPhoto(photo reader.EmbeddedImage) error {
	if len(photo.Data) == 0 {
		return fmt.Errorf("empty photo data")
	}
	if photo.Problem != "" {
		return fmt.Errorf("%s", photo.Problem)
	}
	tmp, err := createOriginalExport()
	if err != nil {
		return fmt.Errorf("create original image: %w", err)
	}
	name := tmp.Name()
	defer func() {
		_ = tmp.Close()
		if originalSessionClosed() {
			_ = os.Remove(name)
		}
	}()
	if bytes.HasPrefix(photo.Data, []byte("\x89PNG\r\n\x1a\n")) {
		_, err = tmp.Write(photo.Data)
	} else {
		var img image.Image
		img, _, err = image.Decode(bytes.NewReader(photo.Data))
		if err == nil {
			encoder := png.Encoder{CompressionLevel: png.BestSpeed}
			err = encoder.Encode(tmp, img)
		}
	}
	if err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("prepare original image: %w", err)
	}
	if err = tmp.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("finish original image: %w", err)
	}
	if originalSessionClosed() {
		return fmt.Errorf("reader session has closed")
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", name)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", name)
	default:
		cmd = exec.Command("xdg-open", name)
	}
	if launchViewerHook != nil {
		return launchViewerHook(cmd)
	}
	if err = cmd.Run(); err != nil {
		return fmt.Errorf("open original image: %w", err)
	}
	return nil
}

func openOriginalPhotoCmd(photo reader.EmbeddedImage, session uint64) tea.Cmd {
	return func() tea.Msg {
		err := openOriginalPhoto(photo)
		return openOriginalResultMsg{err: err, session: session}
	}
}
