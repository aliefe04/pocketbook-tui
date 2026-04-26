package reader

import (
	"testing"
)

func TestParseTXT(t *testing.T) {
	data := []byte("Hello World\n\nThis is line 2\n\nLine 3")
	content, err := ParseTXT(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content.Chapters) != 1 {
		t.Fatalf("expected 1 chapter, got %d", len(content.Chapters))
	}
	if len(content.Chapters[0].Lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(content.Chapters[0].Lines))
	}
}

func TestPositionToChapter(t *testing.T) {
	content := &BookContent{
		Chapters: []Chapter{
			{Title: "Ch1", Lines: []string{"a", "b", "c"}},
			{Title: "Ch2", Lines: []string{"d", "e"}},
			{Title: "Ch3", Lines: []string{"f"}},
		},
	}

	tests := []struct {
		globalLine int
		wantCh     int
		wantLine   int
	}{
		{0, 0, 0},
		{2, 0, 2},
		{3, 1, 0},
		{4, 1, 1},
		{5, 2, 0},
	}

	for _, tt := range tests {
		ch, line := content.PositionToChapter(tt.globalLine)
		if ch != tt.wantCh || line != tt.wantLine {
			t.Errorf("PositionToChapter(%d) = (%d, %d), want (%d, %d)",
				tt.globalLine, ch, line, tt.wantCh, tt.wantLine)
		}
	}
}

func TestWrapLine(t *testing.T) {
	lines := WrapLine("hello world this is a test", 10)
	if len(lines) == 0 {
		t.Fatal("expected wrapped lines")
	}
	for _, line := range lines {
		if len(line) > 10 {
			t.Errorf("line too long: %q (%d chars)", line, len(line))
		}
	}
}
