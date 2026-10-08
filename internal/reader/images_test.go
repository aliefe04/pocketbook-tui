package reader

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"reflect"
	"testing"
)

func epubWithResources(t *testing.T, body string, resources map[string][]byte) []byte {
	t.Helper()
	original := buildEPUB(t, map[string]string{"one": testXHTML("Photos", body)}, []string{"one"})
	r, err := zip.NewReader(bytes.NewReader(original), int64(len(original)))
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	w := zip.NewWriter(&output)
	for _, entry := range r.File {
		source, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		dest, err := w.Create(entry.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(dest, source); err != nil {
			t.Fatal(err)
		}
		source.Close()
	}
	for name, data := range resources {
		dest, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := dest.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	img.SetNRGBA(1, 1, color.NRGBA{B: 255, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func TestEPUBPhotosKeepTextCoordinatesAndExactImageCFI(t *testing.T) {
	body := `<p>BEFORE_PHOTO</p><img src="Pictures/photo%20one.png" alt="Color photo"/><p>AFTER_PHOTO</p>`
	data := epubWithResources(t, body, map[string][]byte{"OEBPS/Pictures/photo one.png": tinyPNG(t)})
	content, err := ParseEPUB(data)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := ParseEPUB(buildEPUB(t, map[string]string{"one": testXHTML("Photos", "<p>BEFORE_PHOTO</p><p>AFTER_PHOTO</p>")}, []string{"one"}))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(content.Chapters[0].Lines, plain.Chapters[0].Lines) {
		t.Fatal("photos changed saved text coordinates")
	}
	photos := content.Chapters[0].Images
	if len(photos) != 1 || photos[0].Problem != "" || photos[0].Name != "OEBPS/Pictures/photo one.png" {
		t.Fatalf("photo resource=%+v", photos)
	}
	if photos[0].Pointer != "#epubcfi(/6/2!/4/4)" {
		t.Fatalf("image path=%s, want actual second body element", photos[0].Pointer)
	}
	_, bookmark, err := ParseEPUBAt(data, photos[0].Pointer)
	if err != nil || bookmark.Err != nil || !bookmark.IsImage || bookmark.ImageIndex != 0 {
		t.Fatalf("native image resume=%+v: %v", bookmark, err)
	}
	pointer, ok := content.PointerAt(0, photos[0].LineOffset)
	if !ok {
		t.Fatal("following text lost its CFI")
	}
	_, bookmark, err = ParseEPUBAt(data, pointer)
	if err != nil || bookmark.Err != nil || bookmark.IsImage {
		t.Fatalf("text CFI became image CFI: %+v,%v", bookmark, err)
	}
}
func TestPhotoOnlyChapterAndTrailingPhotoAreRetained(t *testing.T) {
	for _, body := range []string{`<img src="p.png"/>`, `<p>Before trailing photo</p><img src="p.png"/>`} {
		data := epubWithResources(t, body, map[string][]byte{"OEBPS/p.png": tinyPNG(t)})
		content, err := ParseEPUB(data)
		if err != nil {
			t.Fatal(err)
		}
		photo := content.Chapters[0].Images[0]
		if body != `<img src="p.png"/>` && !photo.After {
			t.Fatal("trailing photo was moved before preceding text")
		}
		_, bookmark, err := ParseEPUBAt(data, photo.Pointer)
		if err != nil || bookmark.Err != nil || !bookmark.IsImage {
			t.Fatalf("image CFI cannot resume: %+v %v", bookmark, err)
		}
	}
}
func TestImagePathsRejectExternalResourcesAndArchiveEscapes(t *testing.T) {
	for _, source := range []string{"https://example.com/private.png", "//example.com/p.png", "data:image/png;base64,AA", "../../../secret.png", "/etc/passwd", "..%2f..%2f..%2fsecret", "..\\private.png"} {
		if _, err := imageResourcePath("OEBPS/Text/chapter.xhtml", source); err == nil {
			t.Fatalf("unsafe resource accepted: %q", source)
		}
	}
	name, err := imageResourcePath("OEBPS/Text/chapter.xhtml", "../Pictures/photo%20one.png#view")
	if err != nil || name != "OEBPS/Pictures/photo one.png" {
		t.Fatalf("relative resource=%q %v", name, err)
	}
}
func TestMissingAndDamagedPhotosDoNotDiscardFollowingText(t *testing.T) {
	data := epubWithResources(t, `<img src="missing.png"/><img src="damaged.png"/><p>STILL_READABLE</p>`, map[string][]byte{"OEBPS/damaged.png": []byte("broken")})
	content, err := ParseEPUB(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, photo := range content.Chapters[0].Images {
		if photo.Problem == "" {
			t.Fatal("unavailable image has no explanation")
		}
	}
	if got := content.Chapters[0].Lines[len(content.Chapters[0].Lines)-1]; got != "STILL_READABLE" {
		t.Fatalf("following text=%q", got)
	}
}
func TestEmbeddedRasterFormatsDecode(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for _, format := range []string{"png", "jpeg", "gif"} {
		var b bytes.Buffer
		var err error
		switch format {
		case "png":
			err = png.Encode(&b, img)
		case "jpeg":
			err = jpeg.Encode(&b, img, nil)
		case "gif":
			err = gif.Encode(&b, img, nil)
		}
		if err != nil {
			t.Fatal(err)
		}
		content, err := ParseEPUB(epubWithResources(t, `<img src="photo"/>`, map[string][]byte{"OEBPS/photo": b.Bytes()}))
		if err != nil {
			t.Fatal(err)
		}
		if photo := content.Chapters[0].Images[0]; photo.Problem != "" || photo.Width != 2 || photo.Height != 2 {
			t.Fatalf("%s rejected: %+v", format, photo)
		}
	}
}

func TestOversizedImageResourcesKeepReadingAvailable(t *testing.T) {
	header := append([]byte(nil), tinyPNG(t)...)
	binary.BigEndian.PutUint32(header[16:20], 4000)
	binary.BigEndian.PutUint32(header[20:24], 2001)
	binary.BigEndian.PutUint32(header[29:33], crc32.ChecksumIEEE(header[12:29]))
	for _, data := range [][]byte{header, bytes.Repeat([]byte("x"), maxImageBytes+1)} {
		content, err := ParseEPUB(epubWithResources(t, `<img src="large.png"/><p>AFTER_LARGE_PHOTO</p>`, map[string][]byte{"OEBPS/large.png": data}))
		if err != nil {
			t.Fatal(err)
		}
		photo := content.Chapters[0].Images[0]
		if photo.Problem == "" || photo.Data != nil {
			t.Fatal("oversized image was retained for decoding")
		}
		last := content.Chapters[0].Lines[len(content.Chapters[0].Lines)-1]
		if last != "AFTER_LARGE_PHOTO" {
			t.Fatal("image resource limits discarded following text")
		}
	}
}
