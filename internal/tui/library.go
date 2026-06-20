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

type libraryModel struct {
	books         []pbc.Book
	filteredBooks []pbc.Book
	client        *api.Client
	cfg           *config.Config
	cursor        int
	scrollOffset  int
	filter        string
	filterMode    bool
	err           error
	loading       bool
	statusMsg     string
	statusIsError bool
	width         int
	height        int
}

func newLibraryModel(client *api.Client, cfg *config.Config) libraryModel {
	return libraryModel{
		client:  client,
		cfg:     cfg,
		loading: true,
	}
}

func (m libraryModel) Init() tea.Cmd {
	return m.loadBooks()
}

type booksLoadedMsg struct {
	books pbc.Books
	err   error
}

type downloadProgressMsg struct {
	bookTitle string
	done      bool
	err       error
}



func (m libraryModel) loadBooks() tea.Cmd {
	return func() tea.Msg {
		books, err := m.client.Books(context.Background(), m.cfg.Token, 9999, 0)
		return booksLoadedMsg{books: books, err: err}
	}
}

func (m libraryModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		if m.filterMode {
			return m.handleFilterInput(msg)
		}

		switch msg.String() {
		case "q", "esc":
			return m, tea.Quit

		case "R":
			m.loading = true
			m.statusMsg = ""
			return m, m.loadBooks()

		case "L":
			m.cfg.ClearAuth()
			m.cfg.Save()
			return m, func() tea.Msg {
				return UnauthorizedMsg{}
			}

		case "j", "down":
			m.cursorDown()
		case "k", "up":
			m.cursorUp()

		case "d":
			if len(m.filteredBooks) > 0 && m.cursor < len(m.filteredBooks) {
				return m, m.downloadBook(m.filteredBooks[m.cursor])
			}

		case "r":
			if len(m.filteredBooks) > 0 && m.cursor < len(m.filteredBooks) {
				m.statusMsg = fmt.Sprintf("Opening: %s...", m.filteredBooks[m.cursor].Title)
				return m, m.openBook(m.filteredBooks[m.cursor])
			}

		case "enter":
			if len(m.filteredBooks) > 0 && m.cursor < len(m.filteredBooks) {
				return m, func() tea.Msg {
					return ShowDetailMsg{Book: m.filteredBooks[m.cursor]}
				}
			}

		case "/":
			m.filterMode = true
			m.filter = ""
			m.applyFilter()
			return m, nil
		}

	case booksLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			// Check for 401 Unauthorized - token expired
			if isUnauthorized(msg.err) {
				m.cfg.ClearAuth()
				m.cfg.Save()
				return m, func() tea.Msg {
					return UnauthorizedMsg{}
				}
			}
			m.statusMsg = fmt.Sprintf("Error: %v", msg.err)
			m.statusIsError = true
			return m, nil
		}
		m.books = msg.books.Books
		m.filteredBooks = m.books
		m.cursor = 0
		m.scrollOffset = 0
		m.statusMsg = fmt.Sprintf("%d books", len(m.books))
		m.statusIsError = false
		return m, nil

	case downloadProgressMsg:
		if msg.err != nil {
			m.statusMsg = fmt.Sprintf("Download failed: %v", msg.err)
			m.statusIsError = true
		} else if msg.done {
			m.statusMsg = fmt.Sprintf("Downloaded: %s", msg.bookTitle)
			m.statusIsError = false
		}
		return m, nil

	case OpenBookMsg:
		if msg.Err != nil {
			m.statusMsg = fmt.Sprintf("Open failed: %v", msg.Err)
			m.statusIsError = true
			return m, nil
		}
		return m, func() tea.Msg {
			return OpenBookMsg{
				Content:   msg.Content,
				BookHash:  msg.BookHash,
				BookTitle: msg.BookTitle,
				Position:  msg.Position,
			}
		}
	}

	return m, nil
}

func (m *libraryModel) handleFilterInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "/":
		m.filterMode = false
		m.applyFilter()
		return m, nil
	case "enter":
		m.filterMode = false
		return m, nil
	case "backspace":
		if len(m.filter) > 0 {
			m.filter = m.filter[:len(m.filter)-1]
			m.applyFilter()
		}
		return m, nil
	default:
		if len(msg.String()) == 1 {
			m.filter += msg.String()
			m.applyFilter()
		}
		return m, nil
	}
}

func (m *libraryModel) applyFilter() {
	if m.filter == "" {
		m.filteredBooks = m.books
	} else {
		var filtered []pbc.Book
		lowerFilter := strings.ToLower(m.filter)
		for _, b := range m.books {
			if strings.Contains(strings.ToLower(b.Title), lowerFilter) ||
				strings.Contains(strings.ToLower(b.MetaData.Authors), lowerFilter) {
				filtered = append(filtered, b)
			}
		}
		m.filteredBooks = filtered
	}
	m.cursor = 0
	m.scrollOffset = 0
}

func (m *libraryModel) cursorDown() {
	if len(m.filteredBooks) == 0 {
		return
	}
	m.cursor++
	if m.cursor >= len(m.filteredBooks) {
		m.cursor = len(m.filteredBooks) - 1
	}
	m.adjustScroll()
}

func (m *libraryModel) cursorUp() {
	if m.cursor > 0 {
		m.cursor--
	}
	m.adjustScroll()
}

func (m *libraryModel) adjustScroll() {
	// Each book takes 2 lines (title + desc); reserve 6 lines for header/footer
	visibleItems := (m.height - 6) / 2
	if visibleItems < 1 {
		visibleItems = 1
	}
	if m.cursor < m.scrollOffset {
		m.scrollOffset = m.cursor
	}
	if m.cursor >= m.scrollOffset+visibleItems {
		m.scrollOffset = m.cursor - visibleItems + 1
	}
}

func (m libraryModel) downloadBook(book pbc.Book) tea.Cmd {
	return func() tea.Msg {
		homeDir := filepath.Dir(config.ConfigDir())
		downloadDir := filepath.Join(homeDir, "pocketbook")
		filename := book.Name
		if filename == "" {
			filename = fmt.Sprintf("%s.%s", book.FastHash, book.Format)
		}
		destPath := filepath.Join(downloadDir, filename)

		ctx := context.Background()
		err := m.client.DownloadBook(ctx, book.Link, destPath, nil)

		return downloadProgressMsg{
			bookTitle: book.Title,
			done:      err == nil,
			err:       err,
		}
	}
}

func (m libraryModel) openBook(book pbc.Book) tea.Cmd {
	return func() tea.Msg {
		homeDir := filepath.Dir(config.ConfigDir())
		downloadDir := filepath.Join(homeDir, "pocketbook")
		filename := book.Name
		if filename == "" {
			filename = fmt.Sprintf("%s.%s", book.FastHash, book.Format)
		}
		destPath := filepath.Join(downloadDir, filename)

		// Download if not exists
		if _, err := os.Stat(destPath); os.IsNotExist(err) {
			ctx := context.Background()
			if err := m.client.DownloadBook(ctx, book.Link, destPath, nil); err != nil {
				return OpenBookMsg{Err: fmt.Errorf("download: %w", err)}
			}
		}

		// Read file
		data, err := os.ReadFile(destPath)
		if err != nil {
			return OpenBookMsg{Err: fmt.Errorf("read file: %w", err)}
		}

		// Check DRM
		if book.IsDrm || book.IsLcp {
			return OpenBookMsg{Err: fmt.Errorf("this book is DRM-protected and cannot be read in the terminal")}
		}

		// Parse based on format
		var content *reader.BookContent
		format := strings.ToLower(book.Format)
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

		// Check if content is actually readable
		totalLines := content.TotalLines()
		if totalLines == 0 {
			return OpenBookMsg{Err: fmt.Errorf("book has no readable content (parsed 0 lines from %d chapters)", len(content.Chapters))}
		}

		// Load saved position
		pos, _ := reader.LoadPosition(book.FastHash)

		return OpenBookMsg{
			Content:   content,
			BookHash:  book.FastHash,
			BookTitle: book.Title,
			Position:  pos,
		}
	}
}

func (m libraryModel) View() string {
	if m.loading && len(m.books) == 0 {
		content := lipgloss.JoinVertical(lipgloss.Left,
			TitleStyle.Render("PocketBook Cloud"),
			BoxStyle.Render("Loading your library..."),
		)
		if m.width > 0 && m.height > 0 {
			return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, content)
		}
		return content
	}

	if m.err != nil && len(m.books) == 0 {
		errText := m.err.Error()
		if m.width > 0 {
			errText = truncate(errText, m.width-8)
		}
		content := lipgloss.JoinVertical(lipgloss.Left,
			TitleStyle.Render("PocketBook Cloud"),
			BoxStyle.Render(
				lipgloss.JoinVertical(lipgloss.Left,
					ErrorStyle.Render("Failed to load library"),
					errText,
				),
			),
			HelpStyle.Render("R: retry • q: quit"),
		)
		if m.width > 0 && m.height > 0 {
			return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, content)
		}
		return content
	}

	// Available width for list items (account for cursor "  " or "> " prefix)
	listWidth := 80
	if m.width > 0 {
		listWidth = m.width - 4
		if listWidth < 20 {
			listWidth = 20
		}
	}

	var lines []string
	lines = append(lines, TitleStyle.Render("PocketBook Cloud"))

	if m.filterMode {
		lines = append(lines, SubtitleStyle.Render(fmt.Sprintf("Filter: %s_", m.filter)))
	}

	// Book list
	visibleItems := m.height - 6
	if visibleItems < 1 {
		visibleItems = 1
	}
	// Each book takes 2 lines (title + desc), so halve visible items
	visibleItems = visibleItems / 2
	if visibleItems < 1 {
		visibleItems = 1
	}

	end := m.scrollOffset + visibleItems
	if end > len(m.filteredBooks) {
		end = len(m.filteredBooks)
	}

	for i := m.scrollOffset; i < end; i++ {
		book := m.filteredBooks[i]
		isSelected := i == m.cursor

		title := book.Title
		if title == "" {
			title = "Untitled"
		}

		author := book.MetaData.Authors
		if author == "" {
			author = "Unknown Author"
		}

		icon := "  "
		if book.Favorite {
			icon = "★ "
		} else if book.IsAudioBook {
			icon = "♪ "
		}

		// Truncate title to fit terminal width
		titleMax := listWidth - lipgloss.Width(icon)
		if titleMax < 5 {
			titleMax = 5
		}
		titleStr := fmt.Sprintf("%s%s", icon, truncate(title, titleMax))

		// Truncate description to fit
		descStr := fmt.Sprintf("    %s • %s • %d%% • %s",
			truncate(author, listWidth-20),
			strings.ToUpper(book.Format), book.ReadPercent, humanBytes(book.Bytes))
		descStr = truncate(descStr, listWidth)

		if isSelected {
			lines = append(lines,
				SelectedItemStyle.Render("> "+titleStr),
				ItemStyle.Render(descStr),
			)
		} else {
			lines = append(lines,
				ItemStyle.Render("  "+titleStr),
				DimColorStyle.Render(descStr),
			)
		}
	}

	// Status bar
	statusStyle := SuccessStyle
	if m.statusIsError {
		statusStyle = ErrorStyle
	}

	helpText := fmt.Sprintf("%s • enter: details • r: read • d: download • /: filter • R: refresh • L: logout • q: quit",
		statusStyle.Render(m.statusMsg))

	helpStyle := HelpStyle
	if m.width > 0 {
		helpStyle = HelpStyle.MaxWidth(m.width)
	}

	lines = append(lines, "")
	lines = append(lines, helpStyle.Render(helpText))

	return strings.Join(lines, "\n")
}

// ShowDetailMsg is sent when a book is selected.
type ShowDetailMsg struct {
	Book pbc.Book
}

// UnauthorizedMsg is sent when a 401 is received, triggering re-login.
type UnauthorizedMsg struct{}

func isUnauthorized(err error) bool {
	return err != nil && strings.Contains(err.Error(), "401")
}

func humanBytes(b int) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
