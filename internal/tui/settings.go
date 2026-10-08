package tui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/aliefe/pocketbook-tui/internal/config"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type settingOption struct {
	category, label, field, description string
	choices                             []string
	minimum, maximum, step              int
	boolean, key                        bool
}

var settingsOptions = []settingOption{
	{category: "Appearance", label: "Theme", field: "Theme", choices: config.Themes, description: "Auto follows your terminal. Forced themes set text and background."},
	{category: "Appearance", label: "Accent", field: "Accent", choices: config.Accents},
	{category: "Reading", label: "Reading mode", field: "ReadingMode", choices: config.ReadingModes, description: "Arrow keys turn pages or move one row. PgUp/PgDn always turn pages."},
	{category: "Reading", label: "Reading width", field: "ReadingWidth", minimum: 0, maximum: 160, step: 10, description: "0 uses a centered 72-column reading width. Margins shrink on small terminals."},
	{category: "Reading", label: "Side margin", field: "HorizontalMargin", maximum: 12, step: 1},
	{category: "Reading", label: "Top/bottom margin", field: "VerticalMargin", maximum: 4, step: 1},
	{category: "Reading", label: "Line spacing", field: "LineSpacing", maximum: 2, step: 1},
	{category: "Reading", label: "Paragraph spacing", field: "ParagraphSpacing", maximum: 3, step: 1, description: "Extra blank rows between paragraphs."},
	{category: "Reading", label: "Alignment", field: "Alignment", choices: config.Alignments},
	{category: "Reading", label: "Header", field: "ShowHeader", boolean: true},
	{category: "Reading", label: "Footer", field: "ShowFooter", boolean: true},
	{category: "Reading", label: "Progress", field: "Progress", choices: config.ProgressModes, description: "Page numbers refer to this terminal layout, not device pages."},
	{category: "Reading", label: "Page overlap", field: "PageOverlap", maximum: 3, step: 1, description: "Repeat this many displayed rows on the next page."},
	{category: "Images", label: "Protocol", field: "ImageBackend", choices: config.ImageBackends, description: "Auto uses Kitty native graphics when available; blocks uses portable half-block characters."},
	{category: "Images", label: "Photo display", field: "ImageMode", choices: config.ImageModes, description: "Portable color or grayscale blocks, captions only, or hidden. I opens the image viewer."},
	{category: "Images", label: "Inline height", field: "ImageHeight", minimum: 0, maximum: 60, step: 2, description: "Maximum image rows in the reading flow (0 = auto). The viewer fits your terminal."},
	{category: "Controls", label: "Mouse wheel", field: "Mouse", boolean: true, description: "Wheel follows page/scroll mode. Disable to keep terminal selection."},
	{category: "Controls", label: "Next page key", field: "NextPage", key: true},
	{category: "Controls", label: "Previous page key", field: "PrevPage", key: true},
	{category: "Controls", label: "Line down key", field: "LineDown", key: true},
	{category: "Controls", label: "Line up key", field: "LineUp", key: true},
	{category: "Library", label: "Sort order", field: "LibrarySort", choices: config.LibrarySorts},
	{category: "Library", label: "Compact rows", field: "CompactLibrary", boolean: true},
	{category: "Cloud", label: "Check on open", field: "CloudAutoRefresh", boolean: true, description: "Manual C refresh remains available when this is off."},
	{category: "Cloud", label: "Sync on exit", field: "CloudSync", boolean: true, description: "Off saves locally only. It never sends a Cloud bookmark."},
	{category: "Cloud", label: "Request timeout", field: "CloudTimeoutSecs", minimum: 5, maximum: 120, step: 5, description: "Seconds per native Cloud request. Slow checks never block reading."},
}

// Explicit accessors avoid reflection and keep configuration types checked.
func settingValue(p *config.Preferences, field string) string {
	switch field {
	case "Theme":
		return p.Theme
	case "Accent":
		return p.Accent
	case "ReadingMode":
		return p.ReadingMode
	case "ReadingWidth":
		return strconv.Itoa(p.ReadingWidth)
	case "HorizontalMargin":
		return strconv.Itoa(p.HorizontalMargin)
	case "VerticalMargin":
		return strconv.Itoa(p.VerticalMargin)
	case "LineSpacing":
		return strconv.Itoa(p.LineSpacing)
	case "ParagraphSpacing":
		return strconv.Itoa(p.ParagraphSpacing)
	case "Alignment":
		return p.Alignment
	case "ShowHeader":
		return strconv.FormatBool(p.ShowHeader)
	case "ShowFooter":
		return strconv.FormatBool(p.ShowFooter)
	case "Progress":
		return p.Progress
	case "PageOverlap":
		return strconv.Itoa(p.PageOverlap)
	case "Mouse":
		return strconv.FormatBool(p.Mouse)
	case "ImageBackend":
		if p.ImageBackend == "" {
			return "auto"
		}
		return p.ImageBackend
	case "ImageMode":
		return p.ImageMode
	case "ImageHeight":
		return strconv.Itoa(p.ImageHeight)
	case "LibrarySort":
		return p.LibrarySort
	case "CompactLibrary":
		return strconv.FormatBool(p.CompactLibrary)
	case "CloudAutoRefresh":
		return strconv.FormatBool(p.CloudAutoRefresh)
	case "CloudSync":
		return strconv.FormatBool(p.CloudSync)
	case "CloudTimeoutSecs":
		return strconv.Itoa(p.CloudTimeoutSecs)
	case "NextPage":
		return p.NavigationKeys.NextPage
	case "PrevPage":
		return p.NavigationKeys.PrevPage
	case "LineDown":
		return p.NavigationKeys.LineDown
	case "LineUp":
		return p.NavigationKeys.LineUp
	}
	return ""
}
func setSettingValue(p *config.Preferences, field, value string) {
	n, _ := strconv.Atoi(value)
	b := value == "true"
	switch field {
	case "Theme":
		p.Theme = value
	case "Accent":
		p.Accent = value
	case "ReadingMode":
		p.ReadingMode = value
	case "ReadingWidth":
		p.ReadingWidth = n
	case "HorizontalMargin":
		p.HorizontalMargin = n
	case "VerticalMargin":
		p.VerticalMargin = n
	case "LineSpacing":
		p.LineSpacing = n
	case "ParagraphSpacing":
		p.ParagraphSpacing = n
	case "Alignment":
		p.Alignment = value
	case "ShowHeader":
		p.ShowHeader = b
	case "ShowFooter":
		p.ShowFooter = b
	case "Progress":
		p.Progress = value
	case "PageOverlap":
		p.PageOverlap = n
	case "Mouse":
		p.Mouse = b
	case "ImageBackend":
		p.ImageBackend = value
	case "ImageMode":
		p.ImageMode = value
	case "ImageHeight":
		p.ImageHeight = n
	case "LibrarySort":
		p.LibrarySort = value
	case "CompactLibrary":
		p.CompactLibrary = b
	case "CloudAutoRefresh":
		p.CloudAutoRefresh = b
	case "CloudSync":
		p.CloudSync = b
	case "CloudTimeoutSecs":
		p.CloudTimeoutSecs = n
	case "NextPage":
		p.NavigationKeys.NextPage = value
	case "PrevPage":
		p.NavigationKeys.PrevPage = value
	case "LineDown":
		p.NavigationKeys.LineDown = value
	case "LineUp":
		p.NavigationKeys.LineUp = value
	}
}

type settingsModel struct {
	draft                 config.Preferences
	cursor, width, height int
	recording             bool
	err                   error
}
type settingsClosedMsg struct {
	saved bool
	prefs config.Preferences
}

func (m settingsModel) Init() tea.Cmd { return nil }
func (m settingsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		key := msg.String()
		if key == "ctrl+c" {
			return m, tea.Quit
		}
		if m.recording {
			if key == "esc" {
				m.recording = false
				return m, nil
			}
			if msg.Paste {
				m.err = fmt.Errorf("press one key, not pasted text")
				return m, nil
			}
			candidate := m.draft
			setSettingValue(&candidate, settingsOptions[m.cursor].field, config.KeyName(key))
			if err := candidate.Validate(); err != nil {
				m.err = err
				return m, nil
			}
			m.draft, m.recording, m.err = candidate, false, nil
			return m, nil
		}
		switch key {
		case "esc":
			return m, func() tea.Msg { return settingsClosedMsg{} }
		case "s":
			if err := m.draft.Save(); err != nil {
				m.err = err
				return m, nil
			}
			p := m.draft
			return m, func() tea.Msg { return settingsClosedMsg{saved: true, prefs: p} }
		case "r":
			m.draft = config.DefaultPreferences()
			m.err = nil
		case "up", "k":
			m.cursor = max(m.cursor-1, 0)
		case "down", "j":
			m.cursor = min(m.cursor+1, len(settingsOptions)-1)
		case "tab":
			current := settingsOptions[m.cursor].category
			for n := 1; n <= len(settingsOptions); n++ {
				i := (m.cursor + n) % len(settingsOptions)
				if settingsOptions[i].category != current {
					m.cursor = i
					break
				}
			}
		case "enter":
			if settingsOptions[m.cursor].key {
				m.recording = true
				m.err = nil
			}
		case "left", "right":
			o := settingsOptions[m.cursor]
			delta := 1
			if key == "left" {
				delta = -1
			}
			value := settingValue(&m.draft, o.field)
			if o.boolean {
				value = strconv.FormatBool(value != "true")
			} else if len(o.choices) > 0 {
				i := slices.Index(o.choices, value)
				value = o.choices[(i+delta+len(o.choices))%len(o.choices)]
			} else if !o.key {
				n, _ := strconv.Atoi(value)
				n = min(max(n+delta*o.step, o.minimum), o.maximum)
				if o.field == "ReadingWidth" && n > 0 && n < 20 {
					if delta > 0 {
						n = 20
					} else {
						n = 0
					}
				}
				if o.field == "ImageHeight" && n > 0 && n < 4 {
					if delta > 0 {
						n = 4
					} else {
						n = 0
					}
				}
				value = strconv.Itoa(n)
			}
			setSettingValue(&m.draft, o.field, value)
			m.err = nil
		}
	}
	return m, nil
}
func (m settingsModel) View() string {
	w, h := termSize(m.width, m.height)
	if tooSmall(w, h) {
		return tooSmallView(w, h)
	}
	o := settingsOptions[m.cursor]
	bodyRows := max(h-3, 1)
	descriptionRows := 0
	if h >= 14 && o.description != "" {
		descriptionRows = 2
	}
	listRows := bodyRows - descriptionRows
	start := max(m.cursor-listRows/2, 0)
	start = min(start, max(len(settingsOptions)-listRows, 0))
	var body []string
	for i := start; i < len(settingsOptions) && len(body) < listRows; i++ {
		option := settingsOptions[i]
		mark := "  "
		style := textStyle
		if i == m.cursor {
			mark = "› "
			style = selectedStyle
		}
		value := settingValue(&m.draft, option.field)
		label := truncate(option.label, max(w-len(value)-4, 1))
		body = append(body, style.Width(w).Render(mark+label+strings.Repeat(" ", max(w-lipgloss.Width(mark+label)-len(value)-1, 1))+value))
	}
	if m.err != nil {
		body[0] = errorStyle.Render(truncate(m.err.Error(), w))
	} else if m.recording {
		body[0] = accentStyle.Render(truncate("Press new key. Esc cancels", w))
	}
	if descriptionRows > 0 {
		description := wrapText(o.description, w)
		body = append(body, description[:min(len(description), descriptionRows)]...)
	}
	footer := []string{truncate("←→ change · s save", w), truncate("esc cancel · tab next · r reset", w)}
	return frame(w, h, []string{titleRow(w, "Settings: "+o.category, fmt.Sprintf("%d/%d", m.cursor+1, len(settingsOptions)))}, body, footer)
}

func (a *App) preferences() config.Preferences {
	if a.prefs.Theme == "" {
		return config.DefaultPreferences()
	}
	return a.prefs
}
func mouseCommand(enabled bool) tea.Cmd {
	if enabled {
		return tea.EnableMouseCellMotion
	}
	return tea.DisableMouse
}
func (a *App) applyPreferences(p config.Preferences) {
	a.prefs = p
	applyTheme(p)
	if m, ok := a.library.(libraryModel); ok {
		m.sortOrder = p.LibrarySort
		m.compact = p.CompactLibrary
		m.setBooks(m.fetched)
		a.library = m
	}
	if m, ok := a.reader.(readerModel); ok {
		m.applyPreferences(p)
		a.reader = m
	}
}
func (m *readerModel) applyPreferences(p config.Preferences) {
	var photo *imageAddress
	oldPart := m.withinLinePart
	if m.layout != nil && len(m.layout.rows) > 0 {
		row := m.layout.rows[m.rowIndex()]
		if row.photo {
			address := imageAddress{row.chapter, row.imageIndex}
			photo = &address
		}
	}
	m.prefs = p
	m.pageMode = p.ReadingMode == "page"
	m.cloudTimeout = time.Duration(p.CloudTimeoutSecs) * time.Second
	m.withinLinePart = 0
	m.ensureLayout()
	if photo != nil {
		if p.ImageMode == "hidden" {
			m.withinLinePart = oldPart
		} else {
			m.locateImage(photo.chapter, photo.index)
		}
	}
	m.imageCache = nil
	if m.showImage {
		m.ensureImageCache()
	}
	if !p.CloudSync && m.phase != phaseSyncing {
		m.phase = phaseIdle
		m.conflict = nil
	}
}
