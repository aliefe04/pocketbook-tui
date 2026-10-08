package tui

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/aliefe/pocketbook-tui/internal/reader"
	tea "github.com/charmbracelet/bubbletea"
)

var (
	graphicsMu         sync.RWMutex
	globalNativeActive bool
	globalCellW        = 8
	globalCellH        = 16
)

func isNativeGraphicsActive() bool {
	graphicsMu.RLock()
	defer graphicsMu.RUnlock()
	return globalNativeActive
}

func currentCellGeometry() (int, int) {
	graphicsMu.RLock()
	defer graphicsMu.RUnlock()
	return globalCellW, globalCellH
}

func setGraphicsState(active bool, cellW, cellH int) {
	graphicsMu.Lock()
	defer graphicsMu.Unlock()
	globalNativeActive = active
	if cellW > 0 && cellH > 0 {
		globalCellW = cellW
		globalCellH = cellH
	}
}

type resourceKey struct {
	bookHash string
	chapter  int
	index    int
	gray     bool
}

type kittyResource struct {
	id          uint32
	data        []byte
	width       int
	height      int
	transmitted bool
	size        int
	lastUsed    time.Time
}

type kittyPlacement struct {
	id         uint32
	resourceID uint32
	row        int
	col        int
	cols       int
	rows       int
	cropX      int
	cropY      int
	cropW      int
	cropH      int
}

type graphicsScene struct {
	id         string
	placements []kittyPlacement
}

type graphicsManager struct {
	mu               sync.Mutex
	resources        map[resourceKey]*kittyResource
	nextResID        uint32
	nextPlacementID  uint32
	activePlacements map[uint32]kittyPlacement
	totalBytes       int
	preparing        map[uint32]bool
	pendingDelete    []uint32
}

func newGraphicsManager() *graphicsManager {
	var seed [4]byte
	if _, err := rand.Read(seed[:]); err != nil {
		binary.BigEndian.PutUint32(seed[:], uint32(time.Now().UnixNano()))
	}
	base := binary.BigEndian.Uint32(seed[:])&0x3fffffff | 0x40000000
	return &graphicsManager{resources: make(map[resourceKey]*kittyResource), nextResID: base,
		nextPlacementID: base + 0x40000000, activePlacements: make(map[uint32]kittyPlacement), preparing: make(map[uint32]bool)}
}

// Preparing a frame never writes to the terminal. Only graphicsWriter commits
// cached resources and placements after Bubble Tea commits the matching text.
func (gm *graphicsManager) getOrLoadResource(key resourceKey, photo reader.EmbeddedImage) (*kittyResource, error) {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	if res, ok := gm.resources[key]; ok {
		res.lastUsed = time.Now()
		gm.preparing[res.id] = true
		return res, nil
	}
	if photo.Problem != "" {
		return nil, fmt.Errorf("%s", photo.Problem)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(photo.Data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 8_000_000 {
		return nil, fmt.Errorf("image cannot be decoded safely")
	}
	var data []byte
	if !key.gray && bytes.HasPrefix(photo.Data, []byte("\x89PNG\r\n\x1a\n")) {
		data = photo.Data
	} else {
		img, _, err := image.Decode(bytes.NewReader(photo.Data))
		if err != nil {
			return nil, fmt.Errorf("image cannot be decoded")
		}
		if key.gray {
			bounds := img.Bounds()
			gray := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
			for y := 0; y < bounds.Dy(); y++ {
				for x := 0; x < bounds.Dx(); x++ {
					c := color.NRGBAModel.Convert(img.At(bounds.Min.X+x, bounds.Min.Y+y)).(color.NRGBA)
					v := uint8((299*uint32(c.R) + 587*uint32(c.G) + 114*uint32(c.B)) / 1000)
					gray.SetNRGBA(x, y, color.NRGBA{R: v, G: v, B: v, A: c.A})
				}
			}
			img = gray
		}
		var buf limitedPNGBuffer
		encoder := png.Encoder{CompressionLevel: png.BestSpeed}
		if err := encoder.Encode(&buf, img); err != nil {
			return nil, fmt.Errorf("image exceeds the native rendering limit")
		}
		data = buf.Bytes()
	}
	const budget = 32 << 20
	if len(data) > budget {
		return nil, fmt.Errorf("image exceeds the native rendering limit")
	}
	for len(gm.resources) >= 8 || gm.totalBytes+len(data) > budget {
		var oldestKey resourceKey
		var oldest *kittyResource
		for k, res := range gm.resources {
			if gm.preparing[res.id] {
				continue
			}
			if oldest == nil || res.lastUsed.Before(oldest.lastUsed) {
				oldestKey, oldest = k, res
			}
		}
		if oldest == nil {
			if gm.totalBytes+len(data) > budget {
				return nil, fmt.Errorf("visible images exceed the native rendering limit")
			}
			break
		}
		delete(gm.resources, oldestKey)
		gm.totalBytes -= oldest.size
		if oldest.transmitted {
			gm.pendingDelete = append(gm.pendingDelete, oldest.id)
		}
	}
	res := &kittyResource{id: gm.nextResID, data: data, width: cfg.Width, height: cfg.Height, size: len(data), lastUsed: time.Now()}
	gm.nextResID++
	gm.resources[key] = res
	gm.totalBytes += res.size
	gm.preparing[res.id] = true
	return res, nil
}

type limitedPNGBuffer struct{ bytes.Buffer }

func (b *limitedPNGBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 32<<20 {
		return 0, fmt.Errorf("native image size limit")
	}
	return b.Buffer.Write(p)
}

func (gm *graphicsManager) beginScene() {
	gm.mu.Lock()
	clear(gm.preparing)
	gm.mu.Unlock()
}

func (gm *graphicsManager) placementID(resourceID uint32) uint32 {
	return resourceID ^ 0x40000000
}

func transmitKittyResource(w io.Writer, res *kittyResource) error {
	b64 := base64.StdEncoding.EncodeToString(res.data)
	const chunkSize = 4096
	totalChunks := (len(b64) + chunkSize - 1) / chunkSize
	for c := range totalChunks {
		start := c * chunkSize
		end := min(start+chunkSize, len(b64))
		chunk := b64[start:end]
		more := 1
		if end == len(b64) {
			more = 0
		}
		if c == 0 {
			if _, err := fmt.Fprintf(w, "\x1b_Ga=t,f=100,t=d,i=%d,q=2,m=%d;%s\x1b\\", res.id, more, chunk); err != nil {
				return err
			}
		} else {
			if _, err := fmt.Fprintf(w, "\x1b_Gm=%d;%s\x1b\\", more, chunk); err != nil {
				return err
			}
		}
	}
	return nil
}

func (gm *graphicsManager) cleanupAll(w io.Writer) error {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	var first error
	for _, res := range gm.resources {
		if res.transmitted {
			if _, err := fmt.Fprintf(w, "\x1b_Ga=d,d=I,i=%d,q=2;\x1b\\", res.id); err != nil && first == nil {
				first = err
			}
		}
	}
	for _, id := range gm.pendingDelete {
		if _, err := fmt.Fprintf(w, "\x1b_Ga=d,d=I,i=%d,q=2;\x1b\\", id); err != nil && first == nil {
			first = err
		}
	}
	clear(gm.activePlacements)
	clear(gm.resources)
	gm.pendingDelete = nil
	gm.totalBytes = 0
	return first
}

type graphicsWriter struct {
	underlying io.Writer
	manager    *graphicsManager
	session    *GraphicsSession
	lastScene  string
	mu         sync.Mutex
}

func (gw *graphicsWriter) Write(p []byte) (int, error) {
	gw.mu.Lock()
	defer gw.mu.Unlock()
	prefix := []byte("\x1b_pbtui:" + gw.session.nonce + ":")
	start := bytes.Index(p, prefix)
	if start < 0 {
		return gw.underlying.Write(p)
	}
	end := bytes.Index(p[start+len(prefix):], []byte("\x1b\\"))
	if end < 0 {
		return gw.underlying.Write(p)
	}
	end += start + len(prefix)
	sceneID := string(p[start+len(prefix) : end])
	clean := make([]byte, 0, len(p))
	clean = append(clean, p[:start]...)
	clean = append(clean, p[end+2:]...)
	n, err := gw.underlying.Write(clean)
	if err != nil {
		return n, err
	}
	if n != len(clean) {
		return n, io.ErrShortWrite
	}
	if sceneID != gw.lastScene {
		if err := gw.session.emitScene(gw.underlying, sceneID); err != nil {
			return len(p), err
		}
		gw.lastScene = sceneID
	}
	return len(p), nil
}

type GraphicsSession struct {
	app          *App
	probe        ProbeResult
	manager      *graphicsManager
	writer       *graphicsWriter
	input        io.Reader
	scenesMu     sync.Mutex
	scenes       map[string]graphicsScene
	sceneOrder   []string
	nonce        string
	restoreInput func()
}

// Preserve terminal size and raw-mode discovery through the output adapter.
func (gw *graphicsWriter) Fd() uintptr {
	if file, ok := gw.underlying.(interface{ Fd() uintptr }); ok {
		return file.Fd()
	}
	return ^uintptr(0)
}

func (gw *graphicsWriter) Read(p []byte) (int, error) {
	if reader, ok := gw.underlying.(io.Reader); ok {
		return reader.Read(p)
	}
	return 0, fmt.Errorf("graphics output is not readable")
}
func (gw *graphicsWriter) Close() error {
	if closer, ok := gw.underlying.(io.Closer); ok {
		return closer.Close()
	}
	return fmt.Errorf("graphics output is not closable")
}

func NewGraphicsSession(app *App) *GraphicsSession {
	probe := probeTerminal()
	resetOriginalExports()
	active := probe.KittySupported && app.preferences().ImageBackend != "blocks"
	setGraphicsState(active, probe.CellWidth, probe.CellHeight)

	gm := newGraphicsManager()
	s := &GraphicsSession{
		app:     app,
		probe:   probe,
		manager: gm,
		scenes:  make(map[string]graphicsScene),
	}
	var nonce [8]byte
	_, _ = rand.Read(nonce[:])
	s.nonce = fmt.Sprintf("%x", nonce[:])
	s.writer = &graphicsWriter{
		underlying: os.Stdout,
		manager:    gm,
		session:    s,
	}

	s.input = os.Stdin
	if restore, err := beginGraphicsInput(); err == nil && restore != nil {
		s.restoreInput = restore
		s.input = newGraphicsInput(probe.BufferedInput, os.Stdin)
	}

	return s
}

func (s *GraphicsSession) Model() tea.Model {
	return &sessionModel{session: s, app: s.app}
}

func (s *GraphicsSession) ProgramOptions() []tea.ProgramOption {
	return []tea.ProgramOption{
		tea.WithOutput(s.writer),
		tea.WithInput(s.input),
	}
}

func (s *GraphicsSession) Close() error {
	setGraphicsState(false, s.probe.CellWidth, s.probe.CellHeight)
	s.writer.mu.Lock()
	err := s.manager.cleanupAll(s.writer.underlying)
	s.writer.mu.Unlock()
	cleanupTempFiles()
	if s.restoreInput != nil {
		s.restoreInput()
		s.restoreInput = nil
	}
	return err
}

func (s *GraphicsSession) storeScene(sc graphicsScene) string {
	s.scenesMu.Lock()
	defer s.scenesMu.Unlock()
	if _, ok := s.scenes[sc.id]; !ok {
		s.sceneOrder = append(s.sceneOrder, sc.id)
	}
	s.scenes[sc.id] = sc
	for len(s.sceneOrder) > 16 {
		delete(s.scenes, s.sceneOrder[0])
		s.sceneOrder = s.sceneOrder[1:]
	}
	return sc.id
}

func (s *GraphicsSession) emitScene(w io.Writer, sceneID string) error {
	s.scenesMu.Lock()
	sc, ok := s.scenes[sceneID]
	s.scenesMu.Unlock()
	if !ok {
		return fmt.Errorf("native image frame expired")
	}
	gm := s.manager
	gm.mu.Lock()
	defer gm.mu.Unlock()
	next := make(map[uint32]kittyPlacement, len(sc.placements))
	for _, placement := range sc.placements {
		next[placement.id] = placement
	}
	for id, placement := range gm.activePlacements {
		replacement, exists := next[id]
		if !exists || replacement != placement {
			if _, err := fmt.Fprintf(w, "\x1b_Ga=d,d=i,i=%d,p=%d,q=2;\x1b\\", placement.resourceID, id); err != nil {
				return err
			}
		}
	}
	for _, id := range gm.pendingDelete {
		if _, err := fmt.Fprintf(w, "\x1b_Ga=d,d=I,i=%d,q=2;\x1b\\", id); err != nil {
			return err
		}
	}
	gm.pendingDelete = nil
	for _, placement := range sc.placements {
		var res *kittyResource
		for _, candidate := range gm.resources {
			if candidate.id == placement.resourceID {
				res = candidate
				break
			}
		}
		if res == nil {
			return fmt.Errorf("native image resource expired")
		}
		if !res.transmitted {
			if err := transmitKittyResource(w, res); err != nil {
				return err
			}
			res.transmitted = true
		}
	}
	for _, placement := range sc.placements {
		if old, exists := gm.activePlacements[placement.id]; exists && old == placement {
			continue
		}
		if _, err := fmt.Fprintf(w, "\x1b7\x1b[%d;%dH\x1b_Ga=p,i=%d,p=%d,c=%d,r=%d,x=%d,y=%d,w=%d,h=%d,C=1,q=2;\x1b\\\x1b8",
			placement.row, placement.col, placement.resourceID, placement.id, placement.cols, placement.rows,
			placement.cropX, placement.cropY, placement.cropW, placement.cropH); err != nil {
			return err
		}
	}
	gm.activePlacements = next
	return nil
}

type sessionModel struct {
	session *GraphicsSession
	app     *App
}

func (m *sessionModel) Init() tea.Cmd {
	return m.app.Init()
}

func (m *sessionModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if closed, ok := msg.(settingsClosedMsg); ok && closed.saved {
		setGraphicsState(m.session.probe.KittySupported && closed.prefs.ImageBackend != "blocks", m.session.probe.CellWidth, m.session.probe.CellHeight)
	}
	updated, cmd := m.app.Update(msg)
	if app, ok := updated.(*App); ok {
		m.app = app
	}
	return m, cmd
}

func (m *sessionModel) View() string {
	if !m.session.probe.KittySupported {
		return m.app.View()
	}
	sc := graphicsScene{id: "empty:blocks"}
	if isNativeGraphicsActive() {
		m.session.manager.beginScene()
		sc = m.buildCurrentScene()
	}
	m.session.storeScene(sc)
	return "\x1b_pbtui:" + m.session.nonce + ":" + sc.id + "\x1b\\" + m.app.View()
}

func (m *sessionModel) buildCurrentScene() graphicsScene {
	cellW, cellH := currentCellGeometry()
	if m.app.screen != screenReader {
		id := "empty:non-reader"
		return graphicsScene{id: id}
	}

	r, ok := m.app.reader.(readerModel)
	if !ok || r.showHelp || r.content == nil || len(r.content.Chapters) == 0 {
		return graphicsScene{id: "empty:reader-help"}
	}

	if tooSmall(r.width, r.height) {
		return graphicsScene{id: "empty:too-small"}
	}
	if r.showImage {
		addresses := r.availableImages()
		if len(addresses) == 0 {
			return graphicsScene{id: "empty:no-images"}
		}
		sel := min(max(r.imageSelection, 0), len(addresses)-1)
		addr := addresses[sel]
		photo := r.content.Chapters[addr.chapter].Images[addr.index]
		if photo.Problem != "" || len(photo.Data) == 0 {
			return graphicsScene{id: "empty:photo-problem"}
		}

		key := resourceKey{bookHash: r.bookHash, chapter: addr.chapter, index: addr.index, gray: r.prefs.ImageMode == "grayscale"}
		res, err := m.session.manager.getOrLoadResource(key, photo)
		if err != nil || res == nil {
			return graphicsScene{id: "empty:res-error"}
		}

		w, h := termSize(r.width, r.height)
		capacity := max(h-4, 1)

		vpW := float64(w * cellW)
		vpH := float64(capacity * cellH)
		scale := math.Min(vpW/float64(res.width), vpH/float64(res.height)) * r.imageZoom
		dispCols := max(int(math.Round(float64(res.width)*scale/float64(cellW))), 1)
		dispRows := max(int(math.Round(float64(res.height)*scale/float64(cellH))), 1)

		cropCols := min(dispCols, w)
		cropRows := min(dispRows, capacity)

		panX := min(max(r.imagePanX, 0), max(dispCols-w, 0))
		panY := min(max(r.imagePan, 0), max(dispRows-capacity, 0))

		cropX := int(math.Round(float64(panX) * float64(res.width) / float64(dispCols)))
		cropY := int(math.Round(float64(panY) * float64(res.height) / float64(dispRows)))
		cropW := int(math.Round(float64(cropCols) * float64(res.width) / float64(dispCols)))
		cropH := int(math.Round(float64(cropRows) * float64(res.height) / float64(dispRows)))

		cropX = min(max(cropX, 0), res.width-1)
		cropY = min(max(cropY, 0), res.height-1)
		cropW = min(max(cropW, 1), res.width-cropX)
		cropH = min(max(cropH, 1), res.height-cropY)

		rowPos := 2
		colPos := 1
		if cropCols < w {
			colPos = 1 + (w-cropCols)/2
		}
		if cropRows < capacity {
			rowPos = 2 + (capacity-cropRows)/2
		}

		pid := m.session.manager.placementID(res.id)
		pl := kittyPlacement{
			id:         pid,
			resourceID: res.id,
			row:        rowPos,
			col:        colPos,
			cols:       cropCols,
			rows:       cropRows,
			cropX:      cropX,
			cropY:      cropY,
			cropW:      cropW,
			cropH:      cropH,
		}

		scID := fmt.Sprintf("viewer:%d:%d:%d:%d:%d:%d:%d:%d:%d", pl.resourceID, pl.row, pl.col, pl.cols, pl.rows, pl.cropX, pl.cropY, pl.cropW, pl.cropH)
		sc := graphicsScene{id: scID, placements: []kittyPlacement{pl}}
		m.session.storeScene(sc)
		return sc
	}

	// Inline reader scene
	w, _ := termSize(r.width, r.height)
	column := r.contentWidth()
	margin := max((w-column)/2, 0)
	pageSize := r.pageSize()
	promptRows := len(r.promptRows(column, pageSize))
	padding := r.verticalPadding()
	headerRows := 0
	if r.prefs.ShowHeader {
		headerRows = 2
	}
	topOffset := headerRows + padding + promptRows

	r.ensureLayout()
	if r.layout == nil || len(r.layout.rows) == 0 {
		return graphicsScene{id: "empty:no-layout"}
	}

	start := r.rowIndex()
	end := min(start+pageSize-promptRows, len(r.layout.rows))

	var placements []kittyPlacement
	var idParts []string
	if r.prefs.ImageMode != "color" && r.prefs.ImageMode != "grayscale" {
		return graphicsScene{id: "empty:captions"}
	}

	for i := start; i < end; {
		row := r.layout.rows[i]
		if !row.photo {
			i++
			continue
		}

		photo := r.content.Chapters[row.chapter].Images[row.imageIndex]
		if photo.Problem != "" || len(photo.Data) == 0 {
			i++
			continue
		}

		key := resourceKey{bookHash: r.bookHash, chapter: row.chapter, index: row.imageIndex, gray: r.prefs.ImageMode == "grayscale"}
		res, err := m.session.manager.getOrLoadResource(key, photo)
		if err != nil || res == nil {
			i++
			continue
		}

		displayCols, totalPhotoRows := calcInlineImageGeometry(column, r.prefs.ImageHeight, photo.Width, photo.Height, cellW, cellH)
		if row.imageRow >= totalPhotoRows || r.prefs.ImageMode == "captions" || r.prefs.ImageMode == "hidden" {
			i++
			continue
		}
		firstPhotoRow := row.imageRow
		visCount := 0
		runIdx := i
		for runIdx < end && r.layout.rows[runIdx].photo && r.layout.rows[runIdx].chapter == row.chapter && r.layout.rows[runIdx].imageIndex == row.imageIndex && r.layout.rows[runIdx].imageRow < totalPhotoRows {
			visCount++
			runIdx++
		}

		cropY := int(math.Round(float64(firstPhotoRow) * float64(res.height) / float64(totalPhotoRows)))
		cropH := int(math.Round(float64(visCount) * float64(res.height) / float64(totalPhotoRows)))
		cropX := 0
		cropW := res.width

		cropY = min(max(cropY, 0), res.height-1)
		cropH = min(max(cropH, 1), res.height-cropY)

		termRow := 1 + topOffset + (i - start)
		termCol := 1 + margin + (column-displayCols)/2
		pid := m.session.manager.placementID(res.id)

		pl := kittyPlacement{
			id:         pid,
			resourceID: res.id,
			row:        termRow,
			col:        termCol,
			cols:       displayCols,
			rows:       visCount,
			cropX:      cropX,
			cropY:      cropY,
			cropW:      cropW,
			cropH:      cropH,
		}
		placements = append(placements, pl)
		idParts = append(idParts, fmt.Sprintf("%d@%d,%d:%dx%d[%d,%d,%d,%d]", pl.resourceID, pl.row, pl.col, pl.cols, pl.rows, pl.cropX, pl.cropY, pl.cropW, pl.cropH))
		i = runIdx
	}

	if len(placements) == 0 {
		return graphicsScene{id: "empty:inline-no-photos"}
	}

	h := sha256.Sum256([]byte(strings.Join(idParts, ";")))
	scID := fmt.Sprintf("inline:%x", h[:8])
	sc := graphicsScene{id: scID, placements: placements}
	m.session.storeScene(sc)
	return sc
}
