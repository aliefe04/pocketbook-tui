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
	height        int
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
		m.height = msg.Height
		return m, nil

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

	// Title - truncate if too long
	titleText := b.Title
	if m.width > 0 {
		maxTitle := m.width - 4
		if maxTitle < 10 {
			maxTitle = 10
		}
		titleText = truncate(titleText, maxTitle)
	}
	title := TitleStyle.Render(titleText)

	// Calculate available width for metadata
	metaWidth := 0
	if m.width > 0 {
		metaWidth = m.width - 8
		if metaWidth < 30 {
			metaWidth = 30
		}
	}

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
		// Truncate long values to fit
		val := f.value
		if metaWidth > 0 {
			maxVal := metaWidth - 14 // label(12) + ": "(2)
			if maxVal < 5 {
				maxVal = 5
			}
			val = truncate(val, maxVal)
		}
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Left,
			DetailLabelStyle.Width(12).Render(f.label+":"),
			DetailValueStyle.Render(val),
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

	boxW := 0
	if m.width > 0 {
		boxW = m.width - 8
		if boxW < 30 {
			boxW = 30
		}
	}
	metaBox := BoxStyle.Width(boxW).Render(
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

	help := HelpStyle.Render("r: read • d: download • esc/b: back • q: quit")

	if m.width > 0 && m.height > 0 {
		content := lipgloss.JoinVertical(lipgloss.Left, title, metaBox, status, help)
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, content)
	}

	return lipgloss.JoinVertical(lipgloss.Left, title, metaBox, status, help)
}

func (m detailModel) renderProgressBar() string {
	barWidth := 40
	if m.width > 0 {
		barWidth = m.width - 20 // account for box padding, margin, percentage text
		if barWidth < 10 {
			barWidth = 10
		}
		if barWidth > 60 {
			barWidth = 60
		}
	}
	percent := m.book.ReadPercent
	if percent > 100 {
		percent = 100
	}

	filled := barWidth * percent / 100
	empty := barWidth - filled

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
