package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	pbc "github.com/micronull/pocketbook-cloud-client"

	"github.com/aliefe/pocketbook-tui/internal/api"
	"github.com/aliefe/pocketbook-tui/internal/config"
)

// detailLabelWidth is the column width for metadata labels.
const detailLabelWidth = 10

// detailChromeRows is the number of rows outside the metadata body: the title,
// the scroll hint, the status, and the key hints.
const detailChromeRows = 4

type detailModel struct {
	book          pbc.Book
	client        *api.Client
	cfg           *config.Config
	statusMsg     string
	statusIsError bool
	width         int
	height        int
	scroll        int
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
		m.scrollBy(0)
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "b", "backspace":
			return m, func() tea.Msg {
				return BackToLibraryMsg{}
			}

		case "d":
			m.setStatus("Downloading…", false)
			return m, downloadBookCmd(m.client, m.book)

		case "r":
			m.setStatus("Opening…", false)
			return m, openBookCmd(m.client, m.cfg.Token, m.book, screenDetail)

		case "j", "down":
			m.scrollBy(1)

		case "k", "up":
			m.scrollBy(-1)

		case "pgdown":
			m.scrollBy(m.bodyCapacity())

		case "pgup":
			m.scrollBy(-m.bodyCapacity())

		case "q", "ctrl+c":
			return m, tea.Quit
		}

	case downloadProgressMsg:
		switch {
		case msg.err != nil:
			m.setStatus(fmt.Sprintf("Download failed: %v", msg.err), true)
		case msg.done:
			m.setStatus(fmt.Sprintf("Downloaded: %s", displayTitle(msg.bookTitle)), false)
		}
		return m, nil

	case OpenBookMsg:
		// Successful opens never reach this screen; the App switches to the reader.
		if msg.Err != nil {
			m.setStatus(fmt.Sprintf("Open failed: %v", msg.Err), true)
		}
		return m, nil

	case openCancelledMsg:
		if !m.statusIsError {
			m.setStatus("", false)
		}
		return m, nil
	}

	return m, nil
}

func (m *detailModel) setStatus(msg string, isErr bool) {
	m.statusMsg = msg
	m.statusIsError = isErr
}

func (m detailModel) View() string {
	if tooSmall(m.width, m.height) {
		return tooSmallView(m.width, m.height)
	}
	w, h := termSize(m.width, m.height)

	header := []string{titleRow(w, "PocketBook Cloud", "Book details")}
	footer := []string{
		m.scrollRow(w),
		statusRow(w, m.statusMsg, m.statusIsError),
		hintLine(w,
			keyHint{"r", "read"},
			keyHint{"S", "settings"},
			keyHint{"d", "download"},
			keyHint{"esc", "back"},
			keyHint{"q", "quit"},
		),
	}
	return frame(w, h, header, m.bodyRows(w)[m.scroll:], footer)
}

// bodyCapacity is how many metadata rows fit between the title and the footer.
func (m detailModel) bodyCapacity() int {
	_, h := termSize(m.width, m.height)
	return max(h-detailChromeRows, 1)
}

// scrollBy moves the metadata by delta rows. The offset stays between the
// first row and the last row that still fills the body, so a resize never
// leaves blank rows at the bottom.
func (m *detailModel) scrollBy(delta int) {
	w, _ := termSize(m.width, m.height)
	maxScroll := max(len(m.bodyRows(w))-m.bodyCapacity(), 0)
	m.scroll = min(max(m.scroll+delta, 0), maxScroll)
}

// scrollRow hints at the scroll keys when the metadata does not fit. It is
// blank otherwise, so the layout does not shift.
func (m detailModel) scrollRow(width int) string {
	if len(m.bodyRows(width)) <= m.bodyCapacity() {
		return ""
	}
	return fitLine(mutedStyle.Render(truncate("  j/k or ↑/↓ scroll · PgUp/PgDn page", width)), width)
}

// bodyRows lists the title, authors, progress, and the metadata that is set.
// Progress comes before the metadata so it survives a short terminal.
func (m detailModel) bodyRows(width int) []string {
	const indent = "  "
	b := m.book
	valueWidth := max(width-len(indent)-detailLabelWidth, 0)

	rows := []string{
		"",
		boldStyle.Render(indent + truncate(displayTitle(b.Title), width-len(indent))),
	}
	if b.MetaData.Authors != "" {
		rows = append(rows, mutedStyle.Render(indent+truncate(b.MetaData.Authors, width-len(indent))))
	}
	rows = append(rows, "",
		labelStyle.Render(indent+"Progress"),
		m.progressRow(width),
	)
	if b.ReadPosition.Page != "" && b.ReadPosition.PagesTotal > 0 {
		rows = append(rows, mutedStyle.Render(indent+fmt.Sprintf("Page %s of %d", b.ReadPosition.Page, b.ReadPosition.PagesTotal)))
	}
	rows = append(rows, "")

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
	for _, f := range fields {
		if f.value == "" || f.value == "0" || (f.value == "No" && (f.label == "DRM" || f.label == "LCP")) {
			continue
		}
		rows = append(rows,
			indent+labelStyle.Width(detailLabelWidth).Render(f.label)+
				textStyle.Render(truncate(f.value, valueWidth)),
		)
	}
	return rows
}

// progressRow draws the reading-progress bar and percentage within width.
func (m detailModel) progressRow(width int) string {
	percent := min(max(m.book.ReadPercent, 0), 100)
	// Reserve the indent and the " 100%" suffix.
	barWidth := min(max(width-9, 8), 50)
	filled := barWidth * percent / 100

	bar := progressFill.Render(strings.Repeat("█", filled)) +
		progressEmpty.Render(strings.Repeat("░", barWidth-filled))
	return "  " + bar + mutedStyle.Render(fmt.Sprintf(" %3d%%", percent))
}

func boolStr(cond bool, t, f string) string {
	if cond {
		return t
	}
	return f
}

// BackToLibraryMsg is sent to return to the library view.
type BackToLibraryMsg struct{}
