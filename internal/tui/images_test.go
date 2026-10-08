package tui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"regexp"
	"strings"
	"testing"

	"github.com/aliefe/pocketbook-tui/internal/config"
	"github.com/aliefe/pocketbook-tui/internal/reader"
	tea "github.com/charmbracelet/bubbletea"
)

func knownPhoto(t *testing.T) reader.EmbeddedImage {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	img.SetNRGBA(1, 0, color.NRGBA{G: 255, A: 255})
	img.SetNRGBA(0, 1, color.NRGBA{B: 255, A: 255})
	img.SetNRGBA(1, 1, color.NRGBA{R: 255, G: 255, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return reader.EmbeddedImage{LineOffset: 1, Alt: "Color photo", Data: b.Bytes(), Width: 2, Height: 2, Pointer: "#epubcfi(/6/2!/4/4)"}
}
func photoReader(t *testing.T) readerModel {
	photo := knownPhoto(t)
	return newReaderModel(&reader.BookContent{Chapters: []reader.Chapter{{Lines: []string{"BEFORE_PHOTO", "AFTER_PHOTO"}, Images: []reader.EmbeddedImage{photo}}}}, "photos", "Photos", nil, 40, 14)
}
func TestPhotoRendererMapsUpperAndLowerPixelColors(t *testing.T) {
	rows, err := renderRaster(knownPhoto(t), 2, 1, "color", "dark", 1)
	if err != nil {
		t.Fatal(err)
	}
	view := strings.Join(rows, "\n")
	for _, color := range []string{"38;2;255;0;0m", "48;2;0;0;255m", "38;2;0;255;0m", "48;2;255;255;0m"} {
		if !strings.Contains(view, color) {
			t.Fatalf("pixel color %s was lost or swapped", color)
		}
	}
}
func TestGrayscaleAndCaptionsModesChangeVisibleImages(t *testing.T) {
	photo := knownPhoto(t)
	rows, err := renderRaster(photo, 2, 1, "grayscale", "light", 1)
	if err != nil {
		t.Fatal(err)
	}
	colors := regexp.MustCompile(`\x1b\[(?:38|48);2;(\d+);(\d+);(\d+)m`).FindAllStringSubmatch(strings.Join(rows, ""), -1)
	if len(colors) != 4 {
		t.Fatal("grayscale output did not contain the four source pixels")
	}
	for _, rgb := range colors {
		if rgb[1] != rgb[2] || rgb[2] != rgb[3] {
			t.Fatal("grayscale left a colored pixel")
		}
	}
	m := photoReader(t)
	p := config.DefaultPreferences()
	p.ImageMode = "captions"
	m.applyPreferences(p)
	assertContains(t, m.View(), "Color photo")
	assertNotContains(t, m.View(), "▀")
	assertContains(t, m.View(), "AFTER_PHOTO")
	p.ImageMode = "hidden"
	m.applyPreferences(p)
	assertNotContains(t, m.View(), "Color photo")
	assertContains(t, m.View(), "BEFORE_PHOTO")
	assertContains(t, m.View(), "AFTER_PHOTO")
}
func TestImageFlowKeepsFollowingTextAndNativeLocation(t *testing.T) {
	m := photoReader(t)
	m.locateImage(0, 0)
	target, ok := m.target()
	if !ok || target.pointer != "#epubcfi(/6/2!/4/4)" {
		t.Fatalf("photo target=%+v, exact=%v", target, ok)
	}
	m.goToChapterStart()
	var foundAfter bool
	for range 20 {
		if strings.Contains(m.View(), "AFTER_PHOTO") {
			foundAfter = true
		}
		before := m.rowIndex()
		m.pageDown()
		if before == m.rowIndex() {
			break
		}
	}
	if !foundAfter {
		t.Fatal("image display hid the following text")
	}
}
func TestImageViewerZoomPanAndCloseKeepReadingPlace(t *testing.T) {
	isolateHome(t)
	m := photoReader(t)
	m.scrollDown(1)
	chapter, line, offset, part := m.chapterIdx, m.lineOffset, m.withinLineOffset, m.withinLinePart
	next, _ := m.Update(runeKey("I"))
	m = next.(readerModel)
	if !m.showImage {
		t.Fatal("photo view did not open")
	}
	for _, size := range [][2]int{{20, 8}, {40, 14}, {80, 24}} {
		next, _ = m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = next.(readerModel)
		assertFits(t, m.View(), size[0], size[1])
		assertContains(t, m.View(), "esc")
		assertContains(t, m.View(), "zoom")
		assertContains(t, m.View(), "n/p")
	}
	next, _ = m.Update(runeKey("+"))
	m = next.(readerModel)
	if m.imageZoom <= 1 {
		t.Fatal("zoom did not change image scale")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = next.(readerModel)
	assertFits(t, m.View(), 80, 24)
	next, cmd := m.Update(runeKey("q"))
	m = next.(readerModel)
	if cmd != nil || m.showImage || m.chapterIdx != chapter || m.lineOffset != line || m.withinLineOffset != offset || m.withinLinePart != part {
		t.Fatal("closing photo view saved or moved the reading position")
	}
}
func TestImageSettingsPreserveAnUnchangedNativeBookmark(t *testing.T) {
	m := photoReader(t)
	m.locateImage(0, 0)
	m.attach(nil, &resumePoint{source: sourceCloud, isImage: true})
	p := config.DefaultPreferences()
	p.ImageMode = "hidden"
	m.applyPreferences(p)
	if !m.unmoved() {
		t.Fatal("hiding an image made an unchanged Cloud bookmark writable")
	}
}
func TestTransparentPhotoCompositesAgainstThemeBackground(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	img.SetNRGBA(0, 0, color.NRGBA{A: 0})
	rgb := pixelRGB(img, 0, 0, [3]int{23, 26, 32}, false)
	if rgb != [3]int{23, 26, 32} {
		t.Fatalf("transparent pixel=%v instead of theme background", rgb)
	}
}

func TestUntitledLeadingPhotosShowAtChapterStart(t *testing.T) {
	photo := knownPhoto(t)
	photo.LineOffset = 0
	content := &reader.BookContent{Chapters: []reader.Chapter{
		{Lines: []string{"AFTER_LEADING_PHOTO"}, Images: []reader.EmbeddedImage{photo}},
		{Lines: []string{"SECOND_CHAPTER"}, Images: []reader.EmbeddedImage{photo}},
	}}
	m := newReaderModel(content, "leading", "Photos", nil, 40, 14)
	assertContains(t, m.View(), "▀")
	m.nextChapter()
	assertContains(t, m.View(), "▀")
	m.scrollDown(3)
	m.goToChapterStart()
	assertContains(t, m.View(), "▀")
	m.prevChapter()
	assertContains(t, m.View(), "▀")
}

func TestViewerMouseWheelDoesNotMoveHiddenReadingPosition(t *testing.T) {
	m := photoReader(t)
	p := config.DefaultPreferences()
	p.Mouse = true
	m.applyPreferences(p)
	m.openImage()
	chapter, line, offset, part := m.chapterIdx, m.lineOffset, m.withinLineOffset, m.withinLinePart
	next, _ := m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown})
	m = next.(readerModel)
	if m.chapterIdx != chapter || m.lineOffset != line || m.withinLineOffset != offset || m.withinLinePart != part {
		t.Fatal("viewing a photo with the wheel moved the hidden book position")
	}
}

func TestMatchingLocalAndCloudPhotoBookmarksDoNotAskOnEveryOpen(t *testing.T) {
	isolateHome(t)
	m := photoReader(t)
	msg := OpenBookMsg{Content: m.content, BookHash: "photos", BookTitle: "Photos",
		Position: &reader.Position{ChapterIndex: 0, LineOffset: 1},
		Cloud:    &resumePoint{source: sourceCloud, chapter: 0, lineOffset: 1, isImage: true, imageIndex: 0},
	}
	app := newTestApp(t)
	app.openReader(msg)
	if app.screen != screenReader {
		t.Fatal("matching saved and native image positions caused a recurring resume choice")
	}
	current := app.reader.(readerModel)
	target, ok := current.target()
	if !ok || target.pointer != "#epubcfi(/6/2!/4/4)" {
		t.Fatal("automatic resume did not prefer the exact native image")
	}
}

func TestDownscalingAveragesFineLinesAndCheckerboard(t *testing.T) {
	// 32x32 image: white background with a 1px black horizontal line across center
	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	for y := range 32 {
		for x := range 32 {
			if y == 16 {
				img.SetNRGBA(x, y, color.NRGBA{R: 0, G: 0, B: 0, A: 255})
			} else {
				img.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
			}
		}
	}

	// Downscale to 4 columns, 2 rows (4x4 half-block pixels)
	rows, err := renderPixels(img, 4, 2, "color", "dark", 1)
	if err != nil {
		t.Fatalf("renderPixels failed: %v", err)
	}
	view := strings.Join(rows, "")
	// Nearest neighbor would either make a row 100% black or 100% white.
	// Area averaging preserves the line by producing gray values (e.g. not purely 255,255,255 and not 0,0,0)
	foundAntialiased := false
	colors := regexp.MustCompile(`\x1b\[(?:38|48);2;(\d+);(\d+);(\d+)m`).FindAllStringSubmatch(view, -1)
	for _, c := range colors {
		if c[1] != "255" && c[1] != "0" {
			foundAntialiased = true
			break
		}
	}
	if !foundAntialiased {
		t.Fatal("box averaging failed to preserve fine line: output was purely binary without intermediate antialiased values")
	}

	// Checkerboard: alternating black and white pixels
	chk := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	for y := range 16 {
		for x := range 16 {
			if (x+y)%2 == 0 {
				chk.SetNRGBA(x, y, color.NRGBA{R: 0, G: 0, B: 0, A: 255})
			} else {
				chk.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
			}
		}
	}
	chkRows, err := renderPixels(chk, 4, 2, "color", "dark", 1)
	if err != nil {
		t.Fatalf("renderPixels checkerboard failed: %v", err)
	}
	chkView := strings.Join(chkRows, "")
	chkColors := regexp.MustCompile(`\x1b\[(?:38|48);2;(\d+);(\d+);(\d+)m`).FindAllStringSubmatch(chkView, -1)
	for _, c := range chkColors {
		// Average of 0 and 255 is ~127
		if c[1] == "0" || c[1] == "255" {
			t.Fatalf("checkerboard aliased to pure color %s instead of averaging to gray", c[1])
		}
	}
}

func TestAlphaGrayscaleAndNonZeroBounds(t *testing.T) {
	// Empty bounds error
	emptyImg := image.NewNRGBA(image.Rect(0, 0, 0, 0))
	_, err := renderPixels(emptyImg, 10, 5, "color", "dark", 1)
	if err == nil {
		t.Fatal("expected error for empty image bounds, got nil")
	}

	// Alpha compositing: 50% transparent white against dark background [23, 26, 32]
	alphaImg := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	for y := range 2 {
		for x := range 2 {
			alphaImg.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: 128})
		}
	}
	rows, err := renderPixels(alphaImg, 2, 1, "color", "dark", 1)
	if err != nil {
		t.Fatalf("renderPixels alpha failed: %v", err)
	}
	// Background is [23, 26, 32]. 50% blend with [255, 255, 255] should be roughly [139, 140, 143]
	colors := regexp.MustCompile(`\x1b\[(?:38|48);2;(\d+);(\d+);(\d+)m`).FindAllStringSubmatch(strings.Join(rows, ""), -1)
	if len(colors) == 0 {
		t.Fatal("no colors rendered for alpha image")
	}

	// Grayscale
	grayRows, err := renderPixels(alphaImg, 2, 1, "grayscale", "dark", 1)
	if err != nil {
		t.Fatalf("renderPixels grayscale failed: %v", err)
	}
	grayColors := regexp.MustCompile(`\x1b\[(?:38|48);2;(\d+);(\d+);(\d+)m`).FindAllStringSubmatch(strings.Join(grayRows, ""), -1)
	for _, c := range grayColors {
		if c[1] != c[2] || c[2] != c[3] {
			t.Fatalf("grayscale pixel channels not equal: R=%s G=%s B=%s", c[1], c[2], c[3])
		}
	}
}
