package reader

import (
	"fmt"
	"io"
	"strings"
)

// ParseTXT parses a plain text file from a byte slice.
func ParseTXT(data []byte) (*BookContent, error) {
	if len(data) > maxBookSize {
		return nil, fmt.Errorf("book too large: %d MB (max %d MB)", len(data)/(1024*1024), maxBookSize/(1024*1024))
	}

	text := string(data)
	lines := strings.Split(text, "\n")

	var cleanLines []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			cleanLines = append(cleanLines, line)
		}
	}

	return &BookContent{
		Chapters: []Chapter{
			{
				Title: "",
				Lines: cleanLines,
			},
		},
	}, nil
}

// ParsePlainText parses from an io.Reader.
func ParsePlainText(r io.Reader) (*BookContent, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read text: %w", err)
	}
	return ParseTXT(data)
}
