package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"strings"

	"github.com/aliefe/pocketbook-tui/internal/api"
	"github.com/aliefe/pocketbook-tui/internal/config"
	"github.com/aliefe/pocketbook-tui/internal/reader"
)

type screen int

const (
	screenLogin screen = iota
	screenLibrary
	screenDetail
	screenReader
	screenResume
	screenSettings
)

type App struct {
	screen         screen
	login          tea.Model
	library        tea.Model
	detail         tea.Model
	reader         tea.Model
	resume         tea.Model
	settings       tea.Model
	settingsOrigin screen
	prefs          config.Preferences
	prefsErr       error
	client         *api.Client
	cfg            *config.Config
	width          int
	height         int
}

func NewApp() (*App, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}

	// lipgloss otherwise queries the terminal background lazily, in the middle
	// of rendering. Asking now, before Bubble Tea takes over the terminal,
	// keeps the adaptive colors deterministic.
	terminalDarkBackground = lipgloss.HasDarkBackground()

	client := api.New()

	app := &App{
		client: client,
		cfg:    cfg,
	}
	app.prefs, app.prefsErr = config.LoadPreferences()
	applyTheme(app.prefs)

	if cfg.IsLoggedIn() {
		app.screen = screenLibrary
		app.library = newLibraryModel(client, cfg)
	} else {
		app.screen = screenLogin
		app.login = newLoginModel(client, cfg)
	}
	app.applyPreferences(app.prefs)

	return app, nil
}

func (a *App) Init() tea.Cmd {
	var cmd tea.Cmd
	switch a.screen {
	case screenLogin:
		cmd = a.login.Init()
	case screenLibrary:
		cmd = a.library.Init()
	case screenDetail:
		cmd = a.detail.Init()
	}
	return tea.Batch(cmd, mouseCommand(a.preferences().Mouse))
}

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "S" && (a.screen == screenReader || a.screen == screenLibrary || a.screen == screenDetail) {
			if library, ok := a.library.(libraryModel); ok && a.screen == screenLibrary && library.filterMode {
				return a, a.dispatch(msg)
			}
			if r, ok := a.reader.(readerModel); ok && a.screen == screenReader && r.phase == phaseSyncing {
				return a, nil
			}
			a.settingsOrigin = a.screen
			a.settings = a.sized(settingsModel{draft: a.preferences(), err: a.prefsErr})
			a.screen = screenSettings
			return a, nil
		}
	case settingsClosedMsg:
		a.screen = a.settingsOrigin
		if msg.saved {
			a.prefsErr = nil
			a.applyPreferences(msg.prefs)
			return a, mouseCommand(msg.prefs.Mouse)
		}
		return a, nil
	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		return a, a.broadcast(msg)

	case LoginSuccessMsg:
		a.screen = screenLibrary
		a.library = a.sized(newLibraryModel(a.client, a.cfg))
		a.applyPreferences(a.preferences())
		return a, a.library.Init()

	case ShowDetailMsg:
		a.screen = screenDetail
		a.detail = a.sized(newDetailModel(msg.Book, a.client, a.cfg))
		return a, a.detail.Init()

	case BackToLibraryMsg:
		a.screen = screenLibrary
		// Falls through to the dispatch below, so the library can clear the
		// status it showed for the book it opened.

	case OpenBookMsg:
		if msg.Err == nil {
			return a.openReader(msg)
		}
		if isUnauthorized(msg.Err) {
			return a, a.reauthenticate()
		}
		return a, a.deliverOpenError(msg)

	case UnauthorizedMsg:
		return a, a.reauthenticate()

	case readerLeftMsg:
		// The reader's session is over. The library shows the notice, and a
		// confirmed Cloud state updates the book in the list.
		a.screen = screenLibrary
		return a, update(&a.library, msg)

	case booksLoadedMsg:
		// The library loads in the background, so its result must reach it even
		// when another screen is showing.
		return a, update(&a.library, msg)

	case downloadProgressMsg:
		// Downloads can start from the library or the detail screen; the result
		// goes to both so each shows it when it is visible again.
		return a, tea.Batch(update(&a.library, msg), update(&a.detail, msg))

	case resumeDecisionMsg:
		if msg.cancelled {
			return a.cancelOpen(msg.open.Origin)
		}
		return a.enterReader(msg.open, &msg.chosen)

	case refreshMsg:
		// The background Cloud read belongs to the reader, which may no longer be
		// showing. The reader's session check decides whether the result applies.
		return a, update(&a.reader, msg)
	}

	return a, a.dispatch(msg)
}

// dispatch forwards msg to the active screen.
func (a *App) dispatch(msg tea.Msg) tea.Cmd {
	switch a.screen {
	case screenLogin:
		return update(&a.login, msg)
	case screenLibrary:
		return update(&a.library, msg)
	case screenDetail:
		return update(&a.detail, msg)
	case screenReader:
		return update(&a.reader, msg)
	case screenResume:
		return update(&a.resume, msg)
	case screenSettings:
		return update(&a.settings, msg)
	}
	return nil
}

// broadcast forwards msg to every screen that exists, so each one knows the
// terminal size even when it is not showing.
func (a *App) broadcast(msg tea.Msg) tea.Cmd {
	return tea.Batch(
		update(&a.login, msg),
		update(&a.library, msg),
		update(&a.detail, msg),
		update(&a.reader, msg),
		update(&a.resume, msg),
		update(&a.settings, msg),
	)
}

// sized gives a newly created screen the current terminal size, so it lays out
// correctly right away instead of waiting for the next resize event.
func (a *App) sized(m tea.Model) tea.Model {
	if a.width == 0 && a.height == 0 {
		return m
	}
	updated, _ := m.Update(tea.WindowSizeMsg{Width: a.width, Height: a.height})
	return updated
}

// openReader decides where a book that opened successfully starts. When the
// local save and the Cloud bookmark are on different lines, the user chooses,
// with Cloud selected first. When they share a line, an exact Cloud bookmark is
// used, so its native pointer is kept and nothing is rewritten on close.
// Otherwise the local save is used, or the Cloud bookmark when there is none.
func (a *App) openReader(msg OpenBookMsg) (tea.Model, tea.Cmd) {
	local := localResumePoint(msg.Position, msg.Content, msg.PositionSavedAt)
	if local != nil && msg.Cloud != nil && !local.sameSpot(*msg.Cloud) {
		return a.showResumeChoice(msg, *local, *msg.Cloud)
	}
	if msg.Cloud != nil && !msg.Cloud.approx {
		return a.enterReader(msg, msg.Cloud)
	}
	if local != nil {
		return a.enterReader(msg, local)
	}
	return a.enterReader(msg, msg.Cloud)
}

// showResumeChoice shows the source choice. The screen that started the open
// stays the origin, so a cancel returns there.
func (a *App) showResumeChoice(msg OpenBookMsg, local, cloud resumePoint) (tea.Model, tea.Cmd) {
	a.screen = screenResume
	a.resume = a.sized(newResumeModel(msg, local, cloud))
	return a, nil
}

// newReaderSession builds the reader for a book that opened, at chosen, or at
// the beginning when chosen is nil. It shows any notice about how the book was
// started. Cloud is not read here; showReader starts that check.
func newReaderSession(msg OpenBookMsg, chosen *resumePoint, width, height int) readerModel {
	var pos *reader.Position
	if chosen != nil {
		pos = chosen.position(msg.BookHash)
	}
	r := newReaderModel(msg.Content, msg.BookHash, msg.BookTitle, pos, width, height)
	if chosen != nil && chosen.isImage {
		r.locateImage(chosen.chapter, chosen.imageIndex)
	}
	if chosen != nil && chosen.source == sourceCloud && !chosen.isImage {
		r.withinLinePart = 0
	}
	r.attach(msg.Sync, chosen)
	if text, isErr := resumeNotice(msg, chosen); text != "" {
		r.setStatus(text, isErr)
	}
	return r
}

// enterReader opens the reader at chosen, or at the beginning when chosen is nil.
func (a *App) enterReader(msg OpenBookMsg, chosen *resumePoint) (tea.Model, tea.Cmd) {
	return a.showReader(newReaderSession(msg, chosen, a.width, a.height))
}

// showReader makes r the open reader and starts its background Cloud check. The
// reader it replaces ends its requests first, so their results are ignored and
// their reads are cancelled.
func (a *App) showReader(r readerModel) (tea.Model, tea.Cmd) {
	if old, ok := a.reader.(readerModel); ok {
		old.invalidate()
	}
	r.applyPreferences(a.preferences())
	var cmd tea.Cmd
	if r.prefs.CloudAutoRefresh {
		r, cmd = r.startRefresh()
	}
	a.screen = screenReader
	a.reader = r
	return a, cmd
}

// cancelOpen returns to the screen that started an open. Nothing is read or
// saved.
func (a *App) cancelOpen(origin screen) (tea.Model, tea.Cmd) {
	if origin == screenDetail {
		a.screen = screenDetail
	} else {
		a.screen = screenLibrary
	}
	return a, tea.Batch(update(&a.library, openCancelledMsg{}), update(&a.detail, openCancelledMsg{}))
}

// deliverOpenError shows a failed open on the screen that started it. The
// current screen does not change. A details screen that now shows another
// book does not take the error.
func (a *App) deliverOpenError(msg OpenBookMsg) tea.Cmd {
	switch msg.Origin {
	case screenLibrary:
		return update(&a.library, msg)
	case screenDetail:
		if d, ok := a.detail.(detailModel); ok && d.book.FastHash == msg.BookHash {
			return update(&a.detail, msg)
		}
	}
	return nil
}

// reauthenticate clears the stored session and shows the login screen.
func (a *App) reauthenticate() tea.Cmd {
	a.cfg.ClearAuth()
	a.cfg.Save()
	a.screen = screenLogin
	a.login = a.sized(newLoginModel(a.client, a.cfg))
	return a.login.Init()
}

// update forwards msg to one screen model and stores the updated model.
func update(model *tea.Model, msg tea.Msg) tea.Cmd {
	if *model == nil {
		return nil
	}
	var cmd tea.Cmd
	*model, cmd = (*model).Update(msg)
	return cmd
}

func (a *App) View() string {
	var view string
	switch a.screen {
	case screenLogin:
		view = a.login.View()
	case screenLibrary:
		view = a.library.View()
	case screenDetail:
		view = a.detail.View()
	case screenReader:
		view = a.reader.View()
	case screenResume:
		view = a.resume.View()
	case screenSettings:
		view = a.settings.View()
	}
	if a.prefsErr != nil && a.screen != screenSettings {
		rows := strings.Split(view, "\n")
		if len(rows) > 0 {
			w, _ := termSize(a.width, a.height)
			rows[0] = errorStyle.Render(truncate("Preferences error. S:settings", w))
			view = strings.Join(rows, "\n")
		}
	}
	if a.preferences().Theme != "auto" {
		view = lipgloss.NewStyle().Background(colorBackground).Render(view)
	}
	return view
}
