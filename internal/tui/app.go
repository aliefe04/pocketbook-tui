package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/aliefe/pocketbook-tui/internal/api"
	"github.com/aliefe/pocketbook-tui/internal/config"
)

type screen int

const (
	screenLogin screen = iota
	screenLibrary
	screenDetail
	screenReader
)

type App struct {
	screen  screen
	login   tea.Model
	library tea.Model
	detail  tea.Model
	reader  tea.Model
	client  *api.Client
	cfg     *config.Config
	width   int
	height  int
}

func NewApp() (*App, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}

	client := api.New()

	app := &App{
		client: client,
		cfg:    cfg,
	}

	if cfg.IsLoggedIn() {
		app.screen = screenLibrary
		app.library = newLibraryModel(client, cfg)
	} else {
		app.screen = screenLogin
		app.login = newLoginModel(client, cfg)
	}

	return app, nil
}

func (a *App) Init() tea.Cmd {
	switch a.screen {
	case screenLogin:
		return a.login.Init()
	case screenLibrary:
		return a.library.Init()
	case screenDetail:
		return a.detail.Init()
	}
	return nil
}

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		var cmds []tea.Cmd
		if a.login != nil {
			_, cmd := a.login.Update(msg)
			cmds = append(cmds, cmd)
		}
		if a.library != nil {
			_, cmd := a.library.Update(msg)
			cmds = append(cmds, cmd)
		}
		if a.detail != nil {
			_, cmd := a.detail.Update(msg)
			cmds = append(cmds, cmd)
		}
		if a.reader != nil {
			_, cmd := a.reader.Update(msg)
			cmds = append(cmds, cmd)
		}
		return a, tea.Batch(cmds...)

	case LoginSuccessMsg:
		a.screen = screenLibrary
		a.library = newLibraryModel(a.client, a.cfg)
		return a, a.library.Init()

	case ShowDetailMsg:
		a.screen = screenDetail
		a.detail = newDetailModel(msg.Book, a.client, a.cfg)
		return a, a.detail.Init()

	case BackToLibraryMsg:
		a.screen = screenLibrary
		return a, nil

	case OpenBookMsg:
		if msg.Err != nil {
			// If download failed due to 401, re-login
			if isUnauthorized(msg.Err) {
				a.cfg.ClearAuth()
				a.cfg.Save()
				a.screen = screenLogin
				a.login = newLoginModel(a.client, a.cfg)
				return a, a.login.Init()
			}
			a.screen = screenLibrary
			return a, nil
		}
		a.screen = screenReader
		a.reader = newReaderModel(msg.Content, msg.BookHash, msg.BookTitle, msg.Position, a.width, a.height)
		return a, a.reader.Init()

	case UnauthorizedMsg:
		a.cfg.ClearAuth()
		a.cfg.Save()
		a.screen = screenLogin
		a.login = newLoginModel(a.client, a.cfg)
		return a, a.login.Init()
	}

	var cmd tea.Cmd
	switch a.screen {
	case screenLogin:
		a.login, cmd = a.login.Update(msg)
	case screenLibrary:
		a.library, cmd = a.library.Update(msg)
	case screenDetail:
		a.detail, cmd = a.detail.Update(msg)
	case screenReader:
		a.reader, cmd = a.reader.Update(msg)
	}

	return a, cmd
}

func (a *App) View() string {
	switch a.screen {
	case screenLogin:
		return a.login.View()
	case screenLibrary:
		return a.library.View()
	case screenDetail:
		return a.detail.View()
	case screenReader:
		return a.reader.View()
	}
	return ""
}
