package reader

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// positionVersion is the current on-disk format version for saved positions.
//
// v0 (legacy): lineOffset indexed body lines only; chapter title chrome was
// not counted. Reader would render the title above the body but it did not
// shift lineOffset.
//
// v1 (current): lineOffset indexes the full virtual flow (title chrome + body).
// A titled chapter's first body line is at lineOffset == TitleHeight().
const positionVersion = 1

// Position represents a saved reading position.
type Position struct {
	BookHash     string `json:"book_hash"`
	ChapterIndex int    `json:"chapter_index"`
	LineOffset   int    `json:"line_offset"`
	Percent      int    `json:"percent"`
	// Version is the on-disk format version. Missing/zero means legacy v0.
	Version int `json:"version,omitempty"`

	// migrationPending signals that lineOffset still uses the legacy v0
	// body-only indexing and must be shifted by the target chapter's
	// TitleHeight before use. Never serialized.
	migrationPending bool `json:"-"`
}

// PositionDir returns the directory for storing positions.
func PositionDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", "pocketbook-tui", "positions")
}

// LoadPosition loads a saved position for a book and migrates legacy v0
// positions to the current v1 virtual-flow format. Migration shifts lineOffset
// forward by the chapter's title height so it still points at the same body
// line it referred to under the old model. The migrated position is saved back
// to disk so the migration runs only once.
func LoadPosition(bookHash string) (*Position, error) {
	path := filepath.Join(PositionDir(), bookHash+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read position: %w", err)
	}

	var pos Position
	if err := json.Unmarshal(data, &pos); err != nil {
		return nil, fmt.Errorf("parse position: %w", err)
	}

	if pos.Version < positionVersion {
		// v0 -> v1: shift lineOffset by the chapter's title height.
		// Without the content we cannot know the exact title height here, so
		// we record that a migration is pending and let the reader apply it
		// when it has the BookContent available. We bump the version now to
		// avoid re-entering this branch.
		pos.Version = positionVersion
		pos.migrationPending = true
	}
	return &pos, nil
}

// migrationPending signals that lineOffset still uses the legacy v0 body-only
// indexing and must be shifted by the target chapter's TitleHeight before use.
// It is never serialized.

// ApplyMigration shifts lineOffset by the chapter's title height and clears
// the pending flag. Caller must guarantee chapterIdx is valid in content.
func (p *Position) ApplyMigration(content *BookContent) {
	if !p.migrationPending || content == nil {
		return
	}
	if p.ChapterIndex >= 0 && p.ChapterIndex < len(content.Chapters) {
		p.LineOffset += content.Chapters[p.ChapterIndex].TitleHeight()
	}
	p.migrationPending = false
}

// LoadPositionForReader is a convenience that loads and applies a v0->v1
// migration in one step using the provided content, then persists the migrated
// position so the migration runs only once.
func LoadPositionForReader(bookHash string, content *BookContent) (*Position, error) {
	pos, err := LoadPosition(bookHash)
	if err != nil || pos == nil {
		return pos, err
	}
	if pos.migrationPending {
		pos.ApplyMigration(content)
		_ = SavePosition(pos) // best-effort persist
	}
	return pos, nil
}

// SavePosition saves a reading position.
func SavePosition(pos *Position) error {
	dir := PositionDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create positions dir: %w", err)
	}

	pos.Version = positionVersion
	path := filepath.Join(dir, pos.BookHash+".json")
	data, err := json.MarshalIndent(pos, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal position: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write position: %w", err)
	}
	return nil
}

// CalculatePercent computes reading percentage from position.
func CalculatePercent(content *BookContent, chapterIdx, lineOffset int) int {
	if len(content.Chapters) == 0 {
		return 0
	}

	totalLines := content.TotalLines()
	if totalLines == 0 {
		return 0
	}

	readLines := content.ChapterStartLine(chapterIdx) + lineOffset
	percent := readLines * 100 / totalLines
	if percent > 100 {
		percent = 100
	}
	return percent
}
