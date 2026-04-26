package reader

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Position represents a saved reading position.
type Position struct {
	BookHash     string `json:"book_hash"`
	ChapterIndex int    `json:"chapter_index"`
	LineOffset   int    `json:"line_offset"`
	Percent      int    `json:"percent"`
}

// PositionDir returns the directory for storing positions.
func PositionDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", "pocketbook-tui", "positions")
}

// LoadPosition loads a saved position for a book.
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
	return &pos, nil
}

// SavePosition saves a reading position.
func SavePosition(pos *Position) error {
	dir := PositionDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create positions dir: %w", err)
	}

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
