package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	pbc "github.com/micronull/pocketbook-cloud-client"

	"github.com/aliefe/pocketbook-tui/internal/api"
	"github.com/aliefe/pocketbook-tui/internal/config"
	"github.com/aliefe/pocketbook-tui/internal/reader"
)

// libraryChromeRows is the number of rows the library uses outside the book
// list: title, filter, a blank row, status, and key hints.
const libraryChromeRows = 5

// bookRowHeight is the number of terminal rows one book takes in the list.
const bookRowHeight = 2

type libraryModel struct {
	fetched       []pbc.Book // as the server listed them, the input to each reorder
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
	sortOrder     string
	compact       bool
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
		m.adjustScroll()
		return m, nil

	case tea.KeyMsg:
		if m.filterMode {
			return m.handleFilterKey(msg)
		}
		return m.handleKey(msg)

	case BackToLibraryMsg:
		// Returning from the reader or details: drop a finished "Opening" status,
		// but keep an error so a failed open is still shown. A save may have made
		// a book the most recent read, so the list is ordered again.
		if !m.statusIsError {
			m.setStatus("", false)
		}
		m.setBooks(m.fetched)
		return m, nil

	case openCancelledMsg:
		if !m.statusIsError {
			m.setStatus("", false)
		}
		return m, nil

	case booksLoadedMsg:
		m.loading = false
		if msg.err != nil {
			// A 401 means the stored token is no longer valid.
			if isUnauthorized(msg.err) {
				m.cfg.ClearAuth()
				m.cfg.Save()
				return m, func() tea.Msg {
					return UnauthorizedMsg{}
				}
			}
			m.err = msg.err
			if len(m.books) > 0 {
				m.setStatus(fmt.Sprintf("Refresh failed: %v", msg.err), true)
			}
			return m, nil
		}
		m.err = nil
		m.setStatus("", false)
		m.setBooks(msg.books.Books)
		return m, nil

	case OpenBookMsg:
		// Successful opens never reach this screen; the App switches to the reader.
		if msg.Err != nil {
			m.setStatus(fmt.Sprintf("Open failed: %v", msg.Err), true)
		}
		return m, nil

	case readerLeftMsg:
		// The reader's session ended. Its notice replaces the status, and a
		// confirmed Cloud state updates the book's progress. The list is ordered
		// again, because the book may now be the most recently read.
		m.setStatus(msg.notice, msg.noticeErr)
		if msg.confirmed != nil {
			m.applyConfirmed(msg.bookHash, *msg.confirmed)
		}
		m.setBooks(m.fetched)
		return m, nil

	case downloadProgressMsg:
		m.showDownloadResult(msg)
		return m, nil
	}

	return m, nil
}

func (m *libraryModel) setStatus(msg string, isErr bool) {
	m.statusMsg = msg
	m.statusIsError = isErr
}

func (m *libraryModel) showDownloadResult(msg downloadProgressMsg) {
	switch {
	case msg.err != nil:
		m.setStatus(fmt.Sprintf("Download failed: %v", msg.err), true)
	case msg.done:
		m.setStatus(fmt.Sprintf("Downloaded: %s", displayTitle(msg.bookTitle)), false)
	}
}

func (m libraryModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc", "ctrl+c":
		return m, tea.Quit

	case "R":
		m.loading = true
		m.setStatus("", false)
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
		if book, ok := m.selectedBook(); ok {
			m.setStatus("Downloading "+displayTitle(book.Title)+"…", false)
			return m, downloadBookCmd(m.client, book)
		}

	case "r":
		if book, ok := m.selectedBook(); ok {
			m.setStatus("Opening "+displayTitle(book.Title)+"…", false)
			return m, openBookCmd(m.client, m.cfg.Token, book, screenLibrary)
		}

	case "enter":
		if book, ok := m.selectedBook(); ok {
			return m, func() tea.Msg {
				return ShowDetailMsg{Book: book}
			}
		}

	case "/":
		m.filterMode = true
		m.filter = ""
		m.applyFilter()
	}

	return m, nil
}

func (m libraryModel) handleFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "/":
		m.filterMode = false
		m.applyFilter()
		return m, nil
	case "enter":
		m.filterMode = false
		return m, nil
	case "backspace":
		m.filter = dropLastRune(m.filter)
		m.applyFilter()
		return m, nil
	}

	switch msg.Type {
	case tea.KeySpace:
		m.filter += " "
	case tea.KeyRunes:
		// Covers typed Unicode and pasted text; control characters are dropped.
		m.filter += printableRunes(msg.Runes)
	default:
		return m, nil
	}
	m.applyFilter()
	return m, nil
}

// dropLastRune removes the last whole character, not the last byte.
func dropLastRune(s string) string {
	if s == "" {
		return s
	}
	_, size := utf8.DecodeLastRuneInString(s)
	return s[:len(s)-size]
}

// printableRunes returns the printable characters from runes.
func printableRunes(runes []rune) string {
	var b strings.Builder
	for _, r := range runes {
		if unicode.IsPrint(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// filterBooks returns the books whose title or authors contain query, ignoring
// case. An empty query returns all books.
func filterBooks(books []pbc.Book, query string) []pbc.Book {
	if query == "" {
		return books
	}
	needle := strings.ToLower(query)
	var out []pbc.Book
	for _, b := range books {
		if strings.Contains(strings.ToLower(b.Title), needle) ||
			strings.Contains(strings.ToLower(b.MetaData.Authors), needle) {
			out = append(out, b)
		}
	}
	return out
}

// applyFilter recomputes the visible books for the current filter and moves
// the selection back to the top. It runs when the filter text changes.
func (m *libraryModel) applyFilter() {
	m.filteredBooks = filterBooks(m.books, m.filter)
	m.cursor = 0
	m.scrollOffset = 0
}

// setBooks replaces the book list with fetched, ordered by recent reading. The
// selection stays on the same book when that book is still listed. Otherwise it
// keeps its index, clamped to the list. The filter text is kept.
func (m *libraryModel) setBooks(fetched []pbc.Book) {
	prev, hadSelection := m.selectedBook()
	m.fetched = fetched
	if m.sortOrder == "title" || m.sortOrder == "author" {
		m.books = slices.Clone(fetched)
		slices.SortStableFunc(m.books, func(a, b pbc.Book) int {
			left, right := displayTitle(a.Title), displayTitle(b.Title)
			if m.sortOrder == "author" {
				left, right = a.MetaData.Authors, b.MetaData.Authors
			}
			return strings.Compare(strings.ToLower(left), strings.ToLower(right))
		})
	} else {
		m.books = sortByRecency(fetched)
	}
	m.filteredBooks = filterBooks(m.books, m.filter)
	if hadSelection {
		for i, b := range m.filteredBooks {
			if sameBook(b, prev) {
				m.cursor = i
				break
			}
		}
	}
	m.cursor = min(m.cursor, max(len(m.filteredBooks)-1, 0))
	m.adjustScroll()
}

// sortByRecency returns books ordered by when they were last read, newest first.
// Books with no reading activity follow in server order. Sort keys are computed
// once per book, so the comparator does no file access.
func sortByRecency(books []pbc.Book) []pbc.Book {
	type ranked struct {
		book pbc.Book
		at   time.Time
	}
	entries := make([]ranked, len(books))
	for i, b := range books {
		entries[i] = ranked{book: b, at: readingActivity(b)}
	}
	slices.SortStableFunc(entries, func(a, b ranked) int {
		switch {
		case a.at.IsZero() && b.at.IsZero():
			return 0
		case a.at.IsZero():
			return 1
		case b.at.IsZero():
			return -1
		}
		return b.at.Compare(a.at)
	})

	sorted := make([]pbc.Book, len(entries))
	for i, e := range entries {
		sorted[i] = e.book
	}
	return sorted
}

// readingActivity is when a book was last read: the newer of the account's
// reading position and the local save. It is zero when the book has no reading
// activity. Upload and metadata dates are not reading activity.
func readingActivity(book pbc.Book) time.Time {
	var at time.Time
	if native, ok := latestNativeProgress(book); ok {
		at = native.updated
	}
	if saved, ok := reader.SavedAt(book.FastHash); ok && saved.After(at) {
		at = saved
	}
	return at
}

// sameBook reports whether a and b are the same book in the list.
func sameBook(a, b pbc.Book) bool {
	return bookIdentity(a) == bookIdentity(b)
}

// bookIdentity is the server id of a book. The file hash or title stands in
// when the id is missing.
func bookIdentity(b pbc.Book) string {
	switch {
	case b.ID != "":
		return b.ID
	case b.FastHash != "":
		return b.FastHash
	}
	return b.Title
}

func (m libraryModel) selectedBook() (pbc.Book, bool) {
	if m.cursor < 0 || m.cursor >= len(m.filteredBooks) {
		return pbc.Book{}, false
	}
	return m.filteredBooks[m.cursor], true
}

func (m *libraryModel) cursorDown() {
	if m.cursor < len(m.filteredBooks)-1 {
		m.cursor++
	}
	m.adjustScroll()
}

func (m *libraryModel) cursorUp() {
	if m.cursor > 0 {
		m.cursor--
	}
	m.adjustScroll()
}

// listCapacity is how many books fit in the list at the current height.
// Rendering and scroll adjustment both use it, so the selection stays on
// screen after a resize.
func (m libraryModel) listCapacity() int {
	_, h := termSize(m.width, m.height)
	return max((h-libraryChromeRows)/m.rowHeight(), 1)
}

func (m libraryModel) rowHeight() int {
	if m.compact {
		return 1
	}
	return bookRowHeight
}

func (m *libraryModel) adjustScroll() {
	m.scrollOffset = scrollWindow(m.scrollOffset, m.cursor, len(m.filteredBooks), m.listCapacity())
}

func (m libraryModel) View() string {
	if tooSmall(m.width, m.height) {
		return tooSmallView(m.width, m.height)
	}
	w, h := termSize(m.width, m.height)

	header := []string{
		titleRow(w, "PocketBook Cloud", m.countLabel()),
		m.filterRow(w),
	}
	footer := []string{
		"",
		statusRow(w, m.statusMsg, m.statusIsError),
		m.hintRow(w),
	}
	return frame(w, h, header, m.bodyRows(w), footer)
}

// countLabel is the header's right-hand summary.
func (m libraryModel) countLabel() string {
	if len(m.books) == 0 {
		return ""
	}
	label := fmt.Sprintf("%d books", len(m.books))
	if len(m.books) == 1 {
		label = "1 book"
	}
	if m.filter != "" {
		label = fmt.Sprintf("%d of %d", len(m.filteredBooks), len(m.books))
	}
	if m.loading {
		label += " · refreshing"
	}
	return label
}

func (m libraryModel) filterRow(width int) string {
	const label = "Filter: "
	switch {
	case m.filterMode:
		room := max(width-lipgloss.Width(label)-1, 0)
		row := mutedStyle.Render(label) + textStyle.Render(fitTail(m.filter, room)) + accentStyle.Render("▏")
		return fitLine(row+mutedStyle.Render("  enter keep · esc close"), width)
	case m.filter != "":
		room := max(width-lipgloss.Width(label), 0)
		shown := fmt.Sprintf("  %d of %d shown", len(m.filteredBooks), len(m.books))
		return fitLine(mutedStyle.Render(label)+textStyle.Render(fitTail(m.filter, room))+mutedStyle.Render(shown), width)
	}
	return ""
}

func (m libraryModel) hintRow(width int) string {
	switch {
	case m.filterMode:
		return hintLine(width,
			keyHint{"enter", "keep"},
			keyHint{"esc", "close"},
			keyHint{"backspace", "delete"},
		)
	case len(m.books) == 0 && m.err != nil:
		return hintLine(width,
			keyHint{"R", "retry"},
			keyHint{"S", "settings"},
			keyHint{"L", "log out"},
			keyHint{"q", "quit"},
		)
	}
	return hintLine(width,
		keyHint{"r", "read"},
		keyHint{"S", "settings"},
		keyHint{"enter", "details"},
		keyHint{"d", "download"},
		keyHint{"/", "filter"},
		keyHint{"R", "refresh"},
		keyHint{"L", "log out"},
		keyHint{"q", "quit"},
	)
}

// bodyRows returns the rows between the header and footer: the book list, or
// a message for loading, errors, an empty library, or no filter matches.
func (m libraryModel) bodyRows(width int) []string {
	const indent = "  "
	switch {
	case m.loading && len(m.books) == 0:
		return []string{"", mutedStyle.Render(indent + "Loading your library…")}

	case len(m.books) == 0 && m.err != nil:
		rows := []string{"", errorStyle.Render(indent + "Could not load your library")}
		for _, line := range wrapText(m.err.Error(), width-len(indent)) {
			rows = append(rows, textStyle.Render(indent+line))
		}
		return rows

	case len(m.books) == 0:
		return []string{
			"",
			mutedStyle.Render(indent + "Your library is empty."),
			mutedStyle.Render(indent + "Press R to refresh."),
		}

	case len(m.filteredBooks) == 0:
		return []string{
			"",
			mutedStyle.Render(indent + fmt.Sprintf("No books match %q.", m.filter)),
			mutedStyle.Render(indent + "Press / to change the filter."),
		}
	}
	return m.listRows(width)
}

func (m libraryModel) listRows(width int) []string {
	capacity := m.listCapacity()
	start := scrollWindow(m.scrollOffset, m.cursor, len(m.filteredBooks), capacity)
	end := min(start+capacity, len(m.filteredBooks))

	rows := make([]string, 0, (end-start)*m.rowHeight())
	for i := start; i < end; i++ {
		rows = append(rows, bookRows(m.filteredBooks[i], i == m.cursor, width, m.compact)...)
	}
	return rows
}

// bookRows renders a title and an optional metadata row.
func bookRows(book pbc.Book, selected bool, width int, compact bool) []string {
	icon := "  "
	switch {
	case book.Favorite:
		icon = "★ "
	case book.IsAudioBook:
		icon = "♪ "
	}
	titleText := truncate(cleanBookText(displayTitle(book.Title)), width-4)
	titleRow := mutedStyle.Render("  "+icon) + textStyle.Render(titleText)
	if selected {
		titleRow = selectedMark.Render("› ") + selectedStyle.Width(width-2).Render(icon+titleText)
	}
	if compact {
		return []string{titleRow}
	}

	author := book.MetaData.Authors
	if author == "" {
		author = "Unknown author"
	}
	meta := joinNonEmpty(" · ",
		author,
		strings.ToUpper(book.Format),
		fmt.Sprintf("%d%%", book.ReadPercent),
		humanBytes(book.Bytes),
	)

	metaText := truncate(meta, width-4)

	if !selected {
		return []string{
			titleRow,
			mutedStyle.Render("    " + metaText),
		}
	}
	return []string{
		titleRow,
		selectedMark.Render("  ") + selectedMeta.Width(width-2).Render("  "+metaText),
	}
}

// displayTitle returns the book title, or "Untitled" when it is empty.
func displayTitle(title string) string {
	if title == "" {
		return "Untitled"
	}
	return title
}

// joinNonEmpty joins the non-empty parts with sep.
func joinNonEmpty(sep string, parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, sep)
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
