package tui

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/aliefe/pocketbook-tui/internal/config"
	"github.com/aliefe/pocketbook-tui/internal/reader"
)

// This consumer decodes the wire format independently of the producer, including
// PNG chunks, source crops, and scoped deletion. It keeps unrelated images too.
type imageTerminal struct {
	images     map[uint32]image.Image
	placements map[uint32]kittyPlacement
	pendingID  uint32
	pending    []byte
	transfers  int
}

func newImageTerminal() *imageTerminal {
	return &imageTerminal{images: make(map[uint32]image.Image), placements: make(map[uint32]kittyPlacement)}
}
func (v *imageTerminal) apply(t *testing.T, output string) {
	t.Helper()
	for {
		start := strings.Index(output, "\x1b_G")
		if start < 0 {
			return
		}
		output = output[start+3:]
		end := strings.Index(output, "\x1b\\")
		if end < 0 {
			t.Fatal("unterminated graphics command")
		}
		command := output[:end]
		output = output[end+2:]
		parts := strings.SplitN(command, ";", 2)
		attrs := make(map[string]string)
		for _, part := range strings.Split(parts[0], ",") {
			kv := strings.SplitN(part, "=", 2)
			if len(kv) == 2 {
				attrs[kv[0]] = kv[1]
			}
		}
		number := func(key string) int { n, _ := strconv.Atoi(attrs[key]); return n }
		id, pid := uint32(number("i")), uint32(number("p"))
		switch attrs["a"] {
		case "t":
			v.pendingID = id
			v.pending = nil
		case "p":
			if v.images[id] == nil {
				t.Fatal("placement referenced an image not yet transmitted")
			}
			v.placements[pid] = kittyPlacement{id: pid, resourceID: id, row: number("row"), col: number("col"), cols: number("c"), rows: number("r"), cropX: number("x"), cropY: number("y"), cropW: number("w"), cropH: number("h")}
		case "d":
			if attrs["d"] != "i" && attrs["d"] != "I" {
				t.Fatalf("unsafe deletion type %q", attrs["d"])
			}
			for key, p := range v.placements {
				if p.resourceID == id && (pid == 0 || pid == key) {
					delete(v.placements, key)
				}
			}
			if attrs["d"] == "I" {
				delete(v.images, id)
			}
		}
		if attrs["a"] == "t" || (attrs["a"] == "" && attrs["m"] != "") {
			if len(parts) != 2 || len(parts[1]) > 4096 {
				t.Fatal("invalid graphics payload chunk")
			}
			v.pending = append(v.pending, parts[1]...)
			if attrs["m"] != "1" {
				data, err := base64.StdEncoding.DecodeString(string(v.pending))
				if err != nil {
					t.Fatal(err)
				}
				img, err := png.Decode(bytes.NewReader(data))
				if err != nil {
					t.Fatal(err)
				}
				v.images[v.pendingID] = img
				v.transfers++
				v.pending = nil
			}
		}
	}
}

func TestNativePNGChunksPreserveEverySourcePixel(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 100, 100))
	random := rand.New(rand.NewPCG(1, 2))
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			img.SetNRGBA(x, y, color.NRGBA{uint8(random.IntN(256)), uint8(random.IntN(256)), uint8(random.IntN(256)), 255})
		}
	}
	var source bytes.Buffer
	if err := png.Encode(&source, img); err != nil {
		t.Fatal(err)
	}
	if base64.StdEncoding.EncodedLen(source.Len()) <= 4096 {
		t.Fatal("fixture does not cross a graphics chunk boundary")
	}
	var wire bytes.Buffer
	if err := transmitKittyResource(&wire, &kittyResource{id: 42, data: source.Bytes()}); err != nil {
		t.Fatal(err)
	}
	terminal := newImageTerminal()
	terminal.apply(t, wire.String())
	got := terminal.images[42]
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			if color.NRGBAModel.Convert(got.At(x, y)) != img.NRGBAAt(x, y) {
				t.Fatalf("source pixel%d,%d changed", x, y)
			}
		}
	}
}

func graphicsTestSession(t *testing.T, m readerModel) (*sessionModel, *bytes.Buffer) {
	t.Helper()
	isolateHome(t)
	app := newTestApp(t)
	app.reader = m
	app.screen = screenReader
	app.width, app.height = m.width, m.height
	app.prefs = m.prefs
	setGraphicsState(true, 8, 16)
	s := &GraphicsSession{app: app, probe: ProbeResult{KittySupported: true, CellWidth: 8, CellHeight: 16}, manager: newGraphicsManager(), scenes: make(map[string]graphicsScene), nonce: "test-session"}
	var wire bytes.Buffer
	s.writer = &graphicsWriter{underlying: &wire, manager: s.manager, session: s}
	t.Cleanup(func() { _ = s.Close(); setGraphicsState(false, 8, 16) })
	return &sessionModel{session: s, app: app}, &wire
}
func commitGraphicsFrame(t *testing.T, m *sessionModel, wire *bytes.Buffer, v *imageTerminal) string {
	t.Helper()
	wire.Reset()
	view := m.View()
	n, err := m.session.writer.Write([]byte(view))
	if err != nil || n != len(view) {
		t.Fatalf("frame commit=%d %v", n, err)
	}
	if strings.Contains(wire.String(), "\x1b_pbtui:") {
		t.Fatal("private renderer marker leaked to the terminal")
	}
	v.apply(t, wire.String())
	return view
}

func TestNativePhotoLeavesNoOverlayBehindSettingsOrBlocks(t *testing.T) {
	m := photoReader(t)
	p := config.DefaultPreferences()
	p.ImageHeight = 6
	m.applyPreferences(p)
	m.locateImage(0, 0)
	model, wire := graphicsTestSession(t, m)
	terminal := newImageTerminal()
	terminal.images[999] = image.NewRGBA(image.Rect(0, 0, 1, 1))
	terminal.placements[998] = kittyPlacement{id: 998, resourceID: 999}
	commitGraphicsFrame(t, model, wire, terminal)
	if len(terminal.placements) != 2 || terminal.transfers != 1 {
		t.Fatal("native photo did not appear")
	}
	commitGraphicsFrame(t, model, wire, terminal)
	if terminal.transfers != 1 {
		t.Fatal("unchanged frame retransmitted the source image")
	}
	model.app.settings = settingsModel{draft: p, width: 40, height: 14}
	model.app.screen = screenSettings
	model.app.settingsOrigin = screenReader
	commitGraphicsFrame(t, model, wire, terminal)
	if len(terminal.placements) != 1 || terminal.placements[998].resourceID != 999 {
		t.Fatal("settings retained the photo or deleted another application's image")
	}
	model.app.screen = screenReader
	commitGraphicsFrame(t, model, wire, terminal)
	model.app.screen = screenSettings
	p.ImageBackend = "blocks"
	model.Update(settingsClosedMsg{saved: true, prefs: p})
	commitGraphicsFrame(t, model, wire, terminal)
	if len(terminal.placements) != 1 || !strings.Contains(model.app.View(), "▀") {
		t.Fatal("switching to blocks retained native overlay or left a blank image")
	}
}

func TestNativeCropStopsBeforeCaptionAndFooter(t *testing.T) {
	m := photoReader(t)
	p := config.DefaultPreferences()
	p.ImageHeight = 12
	m.applyPreferences(p)
	photo := m.content.Chapters[0].Images[0]
	img := image.NewNRGBA(image.Rect(0, 0, 64, 96))
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		t.Fatal(err)
	}
	photo.Data = data.Bytes()
	photo.Width, photo.Height = 64, 96
	m.content.Chapters[0].Images[0] = photo
	m.layout = nil
	model, wire := graphicsTestSession(t, m)
	r := model.app.reader.(readerModel)
	r.locateImage(0, 0)
	r.scrollDown(4)
	model.app.reader = r
	terminal := newImageTerminal()
	commitGraphicsFrame(t, model, wire, terminal)
	if len(terminal.placements) != 1 {
		t.Fatal("partly visible native photo was missing")
	}
	for _, placement := range terminal.placements {
		if placement.rows != 8 || placement.cropY != 32 || placement.cropH != 64 {
			t.Fatalf("wrong source crop: %+v", placement)
		}
		if placement.row+placement.rows > 3+8 {
			t.Fatal("native bitmap covered its caption or footer")
		}
	}
}

func TestNativeGrayscalePreservesTransparentPixels(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	img.SetNRGBA(0, 0, color.NRGBA{255, 30, 80, 128})
	img.SetNRGBA(1, 0, color.NRGBA{0, 0, 0, 0})
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		t.Fatal(err)
	}
	manager := newGraphicsManager()
	res, err := manager.getOrLoadResource(resourceKey{gray: true}, reader.EmbeddedImage{Data: data.Bytes(), Width: 2, Height: 1})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := png.Decode(bytes.NewReader(res.data))
	if err != nil {
		t.Fatal(err)
	}
	first := color.NRGBAModel.Convert(decoded.At(0, 0)).(color.NRGBA)
	last := color.NRGBAModel.Convert(decoded.At(1, 0)).(color.NRGBA)
	if first.A != 128 || first.R != first.G || first.G != first.B || last.A != 0 {
		t.Fatal("native grayscale lost alpha or kept colored pixels")
	}
}

type shortGraphicsOutput struct{}

func (shortGraphicsOutput) Write(p []byte) (int, error) { return len(p) / 2, nil }
func TestNativeFrameDoesNotClaimSuccessAfterShortWrite(t *testing.T) {
	model, _ := graphicsTestSession(t, photoReader(t))
	model.session.writer.underlying = shortGraphicsOutput{}
	_, err := model.session.writer.Write([]byte(model.View()))
	if err != io.ErrShortWrite || model.session.writer.lastScene != "" {
		t.Fatal("short frame write was falsely confirmed")
	}
	model.session.writer.underlying = io.Discard
}

func TestProbeKeepsTypingAndUnrelatedControlSequences(t *testing.T) {
	input := []byte("c\x1b[A\x1b_other;user\x1b\\\x1b[6;20;10t\x1b_Gi=31;OK\x1b\\\x1b[?62;1;2center\r")
	kitty, w, h, keys := parseProbeStream(input)
	if !kitty || w != 10 || h != 20 || string(keys) != "c\x1b[A\x1b_other;user\x1b\\enter\r" {
		t.Fatalf("probe consumed or inserted typing: %q", keys)
	}
	if hasCompleteDA1([]byte("c\x1b[A")) {
		t.Fatal("typed c and up-arrow completed the graphics probe")
	}
}

func TestOpenOriginalUsesPrivateExactPNGAndReportsLaunchFailure(t *testing.T) {
	resetOriginalExports()
	photo := knownPhoto(t)
	var opened string
	launchViewerHook = func(cmd *exec.Cmd) error { opened = cmd.Args[len(cmd.Args)-1]; return fmt.Errorf("viewer unavailable") }
	t.Cleanup(func() { launchViewerHook = nil; cleanupTempFiles() })
	if err := openOriginalPhoto(photo); err == nil {
		t.Fatal("OS viewer failure was reported as successful")
	}
	info, err := os.Stat(opened)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("original image was not private")
	}
	data, err := os.ReadFile(opened)
	if err != nil || !bytes.Equal(data, photo.Data) {
		t.Fatal("opening the original changed its pixels")
	}
	cleanupTempFiles()
	if _, err := os.Stat(opened); !os.IsNotExist(err) {
		t.Fatal("owned original-image export was not cleaned")
	}
}

type fragmentedProbeInput struct{ chunks [][]byte }

func (r *fragmentedProbeInput) Read(p []byte) (int, error) {
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.chunks[0])
	r.chunks[0] = r.chunks[0][n:]
	if len(r.chunks[0]) == 0 {
		r.chunks = r.chunks[1:]
	}
	return n, nil
}
func TestLateProbeRepliesNeverBecomeLoginOrFilterText(t *testing.T) {
	source := &fragmentedProbeInput{chunks: [][]byte{
		[]byte("\x1b"), []byte("_Gi=31;O"), []byte("K\x1b\\c"),
		[]byte("\x1b[A"), []byte("hello\r"), []byte("\x1b"),
	}}
	r := newGraphicsInput(nil, source)
	data, err := io.ReadAll(r)
	if err != nil || string(data) != "c\x1b[Ahello\r\x1b" {
		t.Fatalf("probe altered typing: %q, %v", data, err)
	}
}
func TestOriginalLaunchResultStaysWithItsReaderAndIsVisible(t *testing.T) {
	isolateHome(t)
	m := photoReader(t)
	m.openImage()
	app := newTestApp(t)
	app.reader = m
	app.screen = screenSettings
	app.settingsOrigin = screenReader
	app.Update(openOriginalResultMsg{session: m.session, err: fmt.Errorf("launcher unavailable")})
	m = app.reader.(readerModel)
	assertContains(t, m.View(), "Cannot open original")
	other := photoReader(t)
	app.reader = other
	app.screen = screenReader
	app.Update(openOriginalResultMsg{session: m.session, err: fmt.Errorf("stale failure")})
	if app.reader.(readerModel).imageNotice != "" {
		t.Fatal("old reader launch error reached another book")
	}
}
func TestOriginalExportsCannotOutliveSessionShutdown(t *testing.T) {
	resetOriginalExports()
	file, err := createOriginalExport()
	if err != nil {
		t.Fatal(err)
	}
	name := file.Name()
	t.Cleanup(func() { _ = file.Close(); _ = os.Remove(name) })
	cleanupTempFiles()
	_ = file.Close()
	if _, err := os.Stat(name); !os.IsNotExist(err) {
		t.Fatal("shutdown left an image export")
	}
	if file, err := createOriginalExport(); err == nil {
		file.Close()
		t.Fatal("closed session created another export")
	}
}
