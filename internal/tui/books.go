package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	pbc "github.com/micronull/pocketbook-cloud-client"

	"github.com/aliefe/pocketbook-tui/internal/api"
	"github.com/aliefe/pocketbook-tui/internal/config"
	"github.com/aliefe/pocketbook-tui/internal/reader"
)

// errDRMBook is returned for DRM or LCP protected books, which the reader
// cannot open.
var errDRMBook = errors.New("this book is DRM-protected and cannot be read in the terminal")

// downloadProgressMsg reports the result of a download started from the
// library or the detail screen.
type downloadProgressMsg struct {
	bookTitle string
	done      bool
	err       error
}

// bookCacheDir is where downloaded books are kept: the "pocketbook" directory
// next to the config directory, i.e. ~/.config/pocketbook.
func bookCacheDir() string {
	return filepath.Join(filepath.Dir(config.ConfigDir()), "pocketbook")
}

// bookCachePath is the local file for a book. It uses the server file name and
// falls back to "<fasthash>.<format>" when the server omits the name.
func bookCachePath(book pbc.Book) string {
	filename := book.Name
	if filename == "" {
		filename = fmt.Sprintf("%s.%s", book.FastHash, book.Format)
	}
	return filepath.Join(bookCacheDir(), filename)
}

// downloadBookCmd downloads a book to its cache path, overwriting any existing
// copy, and reports the outcome as downloadProgressMsg.
func downloadBookCmd(client *api.Client, book pbc.Book) tea.Cmd {
	return func() tea.Msg {
		err := client.DownloadBook(context.Background(), book.Link, bookCachePath(book), nil)
		return downloadProgressMsg{
			bookTitle: book.Title,
			done:      err == nil,
			err:       err,
		}
	}
}

// openBookCmd prepares a book for the reader and reports the result as
// OpenBookMsg. origin is the screen that started the open, so a failure
// reaches that screen even if the user has moved on. token authorizes the
// native position requests. A failure is an OpenBookMsg with Err set.
func openBookCmd(client *api.Client, token string, book pbc.Book, origin screen) tea.Cmd {
	return func() tea.Msg {
		msg := openBook(client, token, book)
		msg.Origin = origin
		msg.BookHash = book.FastHash
		return msg
	}
}

// openBook checks that a book can be read, gets its file from the cache
// (downloading it only when absent), parses it, and loads both saved positions:
// the local one, and the account's Cloud one located in the parsed book.
//
// The Cloud position is the last one the library saw. openBook never waits for
// the native API; the reader checks Cloud in the background once it is open.
// Rejections happen before any download. Nothing is written.
func openBook(client *api.Client, token string, book pbc.Book) OpenBookMsg {
	if book.IsDrm || book.IsLcp {
		return OpenBookMsg{Err: errDRMBook}
	}

	format := strings.ToLower(book.Format)
	if format != "epub" && format != "txt" {
		return OpenBookMsg{Err: fmt.Errorf("unsupported format: %s", format)}
	}

	path := bookCachePath(book)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := client.DownloadBook(context.Background(), book.Link, path, nil); err != nil {
			return OpenBookMsg{Err: fmt.Errorf("download: %w", err)}
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return OpenBookMsg{Err: fmt.Errorf("read file: %w", err)}
	}

	// The library's snapshot is the last known Cloud state, and the baseline the
	// reader's Cloud checks compare against.
	native, hasNative := latestNativeProgress(book)

	// The EPUB pointer is resolved in the same pass that parses the book.
	var content *reader.BookContent
	var bm *reader.Bookmark
	if format == "epub" {
		var located reader.Bookmark
		content, located, err = reader.ParseEPUBAt(data, native.pointer)
		bm = &located
	} else {
		content, err = reader.ParseTXT(data)
	}
	if err != nil {
		return OpenBookMsg{Err: fmt.Errorf("parse: %w", err)}
	}

	if content.TotalLines() == 0 {
		return OpenBookMsg{Err: fmt.Errorf("book has no readable content (parsed 0 lines from %d chapters)", len(content.Chapters))}
	}

	pos, posErr := reader.LoadPosition(book.FastHash)
	if pos != nil {
		pos.ApplyMigration(content)
	}
	savedAt, _ := reader.SavedAt(book.FastHash)

	msg := OpenBookMsg{
		Content:         content,
		BookHash:        book.FastHash,
		BookTitle:       book.Title,
		Position:        pos,
		PositionErr:     posErr,
		PositionSavedAt: savedAt,
	}
	msg.Cloud, msg.CloudErr = cloudBookmark(native, hasNative, content, bm)
	msg.Sync = &cloudLink{
		client:   client,
		token:    token,
		bookHash: book.FastHash,
		epub:     format == "epub",
		baseline: native,
		reparse: func(pointer string) (*reader.BookContent, reader.Bookmark, error) {
			return reader.ParseEPUBAt(data, pointer)
		},
	}
	return msg
}
