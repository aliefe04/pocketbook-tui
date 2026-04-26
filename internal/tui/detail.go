package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	pbc "github.com/micronull/pocketbook-cloud-client"

	"github.com/aliefe/pocketbook-tui/internal/api"
	"github.com/aliefe/pocketbook-tui/internal/config"
	"github.com/aliefe/pocketbook-tui/internal/reader"
)

type detailModel struct {
	book          pbc.Book
	client        *api.Client
	cfg           *config.Config
	statusMsg     string
	statusIsError bool
	width         int
}

func newDetailModel(book pbc.Book, client *api.Client, cfg *config.Config) detailModel {
	return detailModel{
		book:   book,
		client: client,
		cfg:    cfg,
	}
}

func (m detailModel) Init() tea.Cmd {
	return nil
}

func (m detailModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width

	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "b", "backspace":
			return m, func() tea.Msg {
				return BackToLibraryMsg{}
			}

		case "d":
			return m, m.downloadBook()

		case "r":
			return m, m.openBook()

		case "q":
			return m, tea.Quit
		}

	case downloadProgressMsg:
		if msg.err != nil {
			m.statusMsg = fmt.Sprintf("Download failed: %v", msg.err)
			m.statusIsError = true
		} else if msg.done {
			m.statusMsg = fmt.Sprintf("Downloaded: %s", msg.bookTitle)
			m.statusIsError = false
		}
		return m, nil
	}

	return m, nil
}

func (m detailModel) downloadBook() tea.Cmd {
	return func() tea.Msg {
		homeDir := filepath.Dir(config.ConfigDir())
		downloadDir := filepath.Join(homeDir, "pocketbook")

		filename := m.book.Name
		if filename == "" {
			filename = fmt.Sprintf("%s.%s", m.book.FastHash, m.book.Format)
		}
		destPath := filepath.Join(downloadDir, filename)

		ctx := context.Background()
		err := m.client.DownloadBook(ctx, m.book.Link, destPath, nil)

		return downloadProgressMsg{
			bookTitle: m.book.Title,
			done:      err == nil,
			err:       err,
		}
	}
}

func (m detailModel) openBook() tea.Cmd {
	return func() tea.Msg {
		homeDir := filepath.Dir(config.ConfigDir())
		downloadDir := filepath.Join(homeDir, "pocketbook")
		filename := m.book.Name
		if filename == "" {
			filename = fmt.Sprintf("%s.%s", m.book.FastHash, m.book.Format)
		}
		destPath := filepath.Join(downloadDir, filename)

		// Download if not exists
		if _, err := os.Stat(destPath); os.IsNotExist(err) {
			ctx := context.Background()
			if err := m.client.DownloadBook(ctx, m.book.Link, destPath, nil); err != nil {
				return OpenBookMsg{Err: fmt.Errorf("download: %w", err)}
			}
		}

		data, err := os.ReadFile(destPath)
		if err != nil {
			return OpenBookMsg{Err: fmt.Errorf("read file: %w", err)}
		}

		// Check DRM
		if m.book.IsDrm || m.book.IsLcp {
			return OpenBookMsg{Err: fmt.Errorf("this book is DRM-protected and cannot be read in the terminal")}
		}

		var content *reader.BookContent
		format := strings.ToLower(m.book.Format)
		switch format {
		case "epub":
			content, err = reader.ParseEPUB(data)
		case "txt":
			content, err = reader.ParseTXT(data)
		default:
			return OpenBookMsg{Err: fmt.Errorf("unsupported format: %s", format)}
		}
		if err != nil {
			return OpenBookMsg{Err: fmt.Errorf("parse: %w", err)}
		}

		totalLines := content.TotalLines()
		if totalLines == 0 {
			return OpenBookMsg{Err: fmt.Errorf("book has no readable content (parsed 0 lines)")}
		}

		pos, _ := reader.LoadPosition(m.book.FastHash)

		return OpenBookMsg{
			Content:   content,
			BookHash:  m.book.FastHash,
			BookTitle: m.book.Title,
			Position:  pos,
		}
	}
}

func (m detailModel) View() string {
	b := m.book

	// Title
	title := TitleStyle.Render(b.Title)

	// Metadata fields
	fields := []struct {
		label string
		value string
	}{
		{"Author", b.MetaData.Authors},
		{"Format", strings.ToUpper(b.Format)},
		{"Language", strings.ToUpper(b.MetaData.Lang)},
		{"Publisher", b.MetaData.Publisher},
		{"Year", fmt.Sprintf("%d", b.MetaData.Year)},
		{"ISBN", b.MetaData.Isbn},
		{"Size", humanBytes(b.Bytes)},
		{"DRM", boolStr(b.IsDrm, "Yes", "No")},
		{"LCP", boolStr(b.IsLcp, "Yes", "No")},
		{"Favorite", boolStr(b.Favorite, "Yes", "No")},
	}

	var rows []string
	for _, f := range fields {
		if f.value == "" || f.value == "0" || f.value == "No" && (f.label == "DRM" || f.label == "LCP") {
			continue
		}
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Left,
			DetailLabelStyle.Width(12).Render(f.label+":"),
			DetailValueStyle.Render(f.value),
		))
	}

	// Reading progress
	progressBar := m.renderProgressBar()
	rows = append(rows, "")
	rows = append(rows, DetailLabelStyle.Render("Progress:"))
	rows = append(rows, progressBar)

	if b.ReadPosition.Page != "" && b.ReadPosition.PagesTotal > 0 {
		rows = append(rows, DetailValueStyle.Render(
			fmt.Sprintf("Page %s of %d", b.ReadPosition.Page, b.ReadPosition.PagesTotal),
		))
	}

	metaBox := BoxStyle.Width(m.width - 8).Render(
		lipgloss.JoinVertical(lipgloss.Left, rows...),
	)

	// Status
	statusStyle := SuccessStyle
	if m.statusIsError {
		statusStyle = ErrorStyle
	}
	status := ""
	if m.statusMsg != "" {
		status = statusStyle.Render(m.statusMsg)
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		title,
		metaBox,
		status,
		HelpStyle.Render("r: read • d: download • esc/b: back • q: quit"),
	)
}

func (m detailModel) renderProgressBar() string {
	width := 40
	percent := m.book.ReadPercent
	if percent > 100 {
		percent = 100
	}

	filled := width * percent / 100
	empty := width - filled

	bar := ProgressBarStyle.Render(strings.Repeat("█", filled)) +
		ProgressBarEmptyStyle.Render(strings.Repeat("░", empty))

	return fmt.Sprintf("%s %d%%", bar, percent)
}

func boolStr(cond bool, t, f string) string {
	if cond {
		return t
	}
	return f
}

// BackToLibraryMsg is sent to return to the library view.
type BackToLibraryMsg struct{}
