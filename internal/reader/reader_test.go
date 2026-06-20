package reader

import (
	"os"
	"path/filepath"
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

func TestTitleHeight(t *testing.T) {
	t.Run("titled", func(t *testing.T) {
		c := Chapter{Title: "Ch1", Lines: []string{"a"}}
		if h := c.TitleHeight(); h != 3 {
			t.Errorf("titled chapter TitleHeight = %d, want 3", h)
		}
		if rc := c.RenderedLineCount(); rc != 4 {
			t.Errorf("titled chapter RenderedLineCount = %d, want 4", rc)
		}
	})
	t.Run("untitled", func(t *testing.T) {
		c := Chapter{Title: "", Lines: []string{"a", "b"}}
		if h := c.TitleHeight(); h != 0 {
			t.Errorf("untitled chapter TitleHeight = %d, want 0", h)
		}
		if rc := c.RenderedLineCount(); rc != 2 {
			t.Errorf("untitled chapter RenderedLineCount = %d, want 2", rc)
		}
	})
	t.Run("whitespace-only title", func(t *testing.T) {
		c := Chapter{Title: "   \t  ", Lines: []string{"a"}}
		if h := c.TitleHeight(); h != 0 {
			t.Errorf("whitespace-only title TitleHeight = %d, want 0", h)
		}
	})
}

func makeTestContent() *BookContent {
	return &BookContent{
		Chapters: []Chapter{
			{Title: "Ch1", Lines: []string{"a", "b", "c"}}, // title(3) + body(3) = 6
			{Title: "Ch2", Lines: []string{"d", "e"}},       // title(3) + body(2) = 5
			{Title: "Ch3", Lines: []string{"f"}},            // title(3) + body(1) = 4
		},
	}
}

func TestTotalLinesVirtual(t *testing.T) {
	c := makeTestContent()
	// 6 + 5 + 4 = 15
	if got := c.TotalLines(); got != 15 {
		t.Errorf("TotalLines = %d, want 15", got)
	}
}

func TestChapterStartLineVirtual(t *testing.T) {
	c := makeTestContent()
	tests := []struct {
		ch   int
		want int
	}{
		{0, 0},
		{1, 6},  // Ch1 rendered 6 lines
		{2, 11}, // Ch1+Ch2 = 6+5 = 11
	}
	for _, tt := range tests {
		if got := c.ChapterStartLine(tt.ch); got != tt.want {
			t.Errorf("ChapterStartLine(%d) = %d, want %d", tt.ch, got, tt.want)
		}
	}
}

func TestPositionToChapter(t *testing.T) {
	content := makeTestContent()

	tests := []struct {
		globalLine int
		wantCh     int
		wantLine   int
	}{
		{0, 0, 0},  // Ch1 title
		{2, 0, 2},  // Ch1 blank
		{3, 0, 3},  // Ch1 body[0]
		{5, 0, 5},  // Ch1 body[2]
		{6, 1, 0},  // Ch2 title
		{8, 1, 2},  // Ch2 blank
		{9, 1, 3},  // Ch2 body[0]
		{10, 1, 4}, // Ch2 body[1]
		{11, 2, 0}, // Ch3 title
		{14, 2, 3}, // Ch3 body[0]
	}

	for _, tt := range tests {
		ch, line := content.PositionToChapter(tt.globalLine)
		if ch != tt.wantCh || line != tt.wantLine {
			t.Errorf("PositionToChapter(%d) = (%d, %d), want (%d, %d)",
				tt.globalLine, ch, tt.wantCh, line, tt.wantLine)
		}
	}
}

func TestCalculatePercentVirtual(t *testing.T) {
	c := makeTestContent() // total = 15

	tests := []struct {
		ch, line, wantPct int
	}{
		{0, 0, 0},   // Ch1 title, 0 read
		{0, 3, 20},  // 3/15 = 20
		{1, 0, 40},  // 6/15 = 40
		{2, 0, 73},  // 11/15 = 73
		{2, 3, 93},  // 14/15 = 93
	}
	for _, tt := range tests {
		got := CalculatePercent(c, tt.ch, tt.line)
		if got != tt.wantPct {
			t.Errorf("CalculatePercent(ch=%d, line=%d) = %d, want %d",
				tt.ch, tt.line, got, tt.wantPct)
		}
	}
}

func TestWrapLine(t *testing.T) {
	lines := WrapLine("hello world this is a test", 10)
	if len(lines) == 0 {
		t.Fatal("expected wrapped lines")
	}
	for _, line := range lines {
		if len([]rune(line)) > 10 {
			t.Errorf("line too long: %q (%d runes)", line, len([]rune(line)))
		}
	}
}

func TestWrapLineUnicode(t *testing.T) {
	// CJK + emoji — must use rune-based wrapping, not byte-based.
	lines := WrapLine("你好世界这是一个测试🌟star", 6)
	if len(lines) == 0 {
		t.Fatal("expected wrapped lines")
	}
	for i, line := range lines {
		if len([]rune(line)) > 6 {
			t.Errorf("line %d too long: %q (%d runes > 6)", i, line, len([]rune(line)))
		}
	}
}

func TestPositionMigrationV0ToV1(t *testing.T) {
	// Build a fake v0 position file (no Version field) on disk.
	dir := PositionDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	bookHash := "test-migration-v0"
	path := filepath.Join(dir, bookHash+".json")
	// v0: lineOffset=2 indexes body line 2 (no title offset).
	v0JSON := `{"book_hash":"test-migration-v0","chapter_index":0,"line_offset":2,"percent":50}`
	if err := os.WriteFile(path, []byte(v0JSON), 0600); err != nil {
		t.Fatalf("write v0 pos: %v", err)
	}
	defer os.Remove(path)

	content := makeTestContent() // Ch1 has TitleHeight=3

	pos, err := LoadPosition(bookHash)
	if err != nil || pos == nil {
		t.Fatalf("LoadPosition: %v / %v", pos, err)
	}
	if !pos.migrationPending {
		t.Fatal("expected migrationPending=true for v0 position")
	}

	// Apply migration with content: lineOffset should shift by TitleHeight (3).
	pos.ApplyMigration(content)
	if pos.LineOffset != 5 {
		t.Errorf("after migration LineOffset = %d, want 5 (2+3)", pos.LineOffset)
	}
	if pos.migrationPending {
		t.Error("migrationPending should be cleared after ApplyMigration")
	}
	if pos.Version != positionVersion {
		t.Errorf("Version = %d, want %d", pos.Version, positionVersion)
	}
}

func TestPositionMigrationNoOpForV1(t *testing.T) {
	dir := PositionDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	bookHash := "test-migration-v1"
	path := filepath.Join(dir, bookHash+".json")
	// v1: Version=1, lineOffset=5 (already virtual-flow indexed).
	v1JSON := `{"book_hash":"test-migration-v1","chapter_index":0,"line_offset":5,"percent":33,"version":1}`
	if err := os.WriteFile(path, []byte(v1JSON), 0600); err != nil {
		t.Fatalf("write v1 pos: %v", err)
	}
	defer os.Remove(path)

	pos, err := LoadPosition(bookHash)
	if err != nil || pos == nil {
		t.Fatalf("LoadPosition: %v / %v", pos, err)
	}
	if pos.migrationPending {
		t.Error("v1 position should not be marked migrationPending")
	}
	if pos.LineOffset != 5 {
		t.Errorf("v1 LineOffset = %d, want 5 (unchanged)", pos.LineOffset)
	}
}

func TestPositionMigrationUntitledChapter(t *testing.T) {
	dir := PositionDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	bookHash := "test-migration-untitled"
	path := filepath.Join(dir, bookHash+".json")
	v0JSON := `{"book_hash":"test-migration-untitled","chapter_index":0,"line_offset":2,"percent":50}`
	if err := os.WriteFile(path, []byte(v0JSON), 0600); err != nil {
		t.Fatalf("write v0 pos: %v", err)
	}
	defer os.Remove(path)

	// Untitled chapter — TitleHeight=0, so migration should be a no-op shift.
	content := &BookContent{
		Chapters: []Chapter{{Title: "", Lines: []string{"a", "b", "c", "d"}}},
	}

	pos, err := LoadPosition(bookHash)
	if err != nil || pos == nil {
		t.Fatalf("LoadPosition: %v / %v", pos, err)
	}
	if !pos.migrationPending {
		t.Fatal("expected migrationPending=true for v0 position")
	}
	pos.ApplyMigration(content)
	if pos.LineOffset != 2 {
		t.Errorf("untitled migration LineOffset = %d, want 2 (unchanged)", pos.LineOffset)
	}
}
