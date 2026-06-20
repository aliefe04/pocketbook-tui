package tui

import (
	"context"
	"fmt"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/micronull/pocketbook-cloud-client"

	"github.com/aliefe/pocketbook-tui/internal/api"
	"github.com/aliefe/pocketbook-tui/internal/config"
)

type loginState int

const (
	loginStateEmail loginState = iota
	loginStatePassword
	loginStateProvider
	loginStateLoading
	loginStateError
)

type loginModel struct {
	state      loginState
	emailInput textinput.Model
	passInput  textinput.Model
	providers  []pocketbook_cloud_client.Provider
	selected   int
	err        error
	client     *api.Client
	cfg        *config.Config
	width      int
	height     int
}

func newLoginModel(client *api.Client, cfg *config.Config) loginModel {
	m := loginModel{
		client: client,
		cfg:    cfg,
		state:  loginStateEmail,
	}

	m.emailInput = textinput.New()
	m.emailInput.Placeholder = "your@email.com"
	m.emailInput.Focus()
	m.emailInput.Width = 40
	m.emailInput.PromptStyle = lipgloss.NewStyle().Foreground(PrimaryColor)

	m.passInput = textinput.New()
	m.passInput.Placeholder = "password"
	m.passInput.EchoMode = textinput.EchoPassword
	m.passInput.EchoCharacter = '•'
	m.passInput.Width = 40
	m.passInput.PromptStyle = lipgloss.NewStyle().Foreground(PrimaryColor)

	return m
}

func (m loginModel) Init() tea.Cmd {
	return textinput.Blink
}

// resizeInputs adjusts text input widths based on terminal width.
func (m *loginModel) resizeInputs() {
	if m.width == 0 {
		return
	}
	// Box: border(2) + padding(2) + margin(2) + label "Email:"(6) + prompt(2) = ~14
	w := m.width - 20
	if w < 10 {
		w = 10
	}
	if w > 60 {
		w = 60
	}
	m.emailInput.Width = w
	m.passInput.Width = w
}

type providersMsg struct {
	providers []pocketbook_cloud_client.Provider
	err       error
}

type loginSuccessMsg struct {
	token    pocketbook_cloud_client.Token
	provider string
	shopID   string
}

type loginErrMsg struct{ err error }

func (m loginModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resizeInputs()
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit

		case "tab", "down":
			if m.state == loginStateProvider && len(m.providers) > 0 {
				m.selected = (m.selected + 1) % len(m.providers)
				return m, nil
			}

		case "up":
			if m.state == loginStateProvider && len(m.providers) > 0 {
				m.selected--
				if m.selected < 0 {
					m.selected = len(m.providers) - 1
				}
				return m, nil
			}

		case "enter":
			return m.handleEnter()
		}

	case providersMsg:
		m.state = loginStateProvider
		if msg.err != nil {
			m.state = loginStateError
			m.err = msg.err
			return m, nil
		}
		m.providers = msg.providers
		if len(m.providers) == 0 {
			m.state = loginStateError
			m.err = fmt.Errorf("no providers found for this email")
		}
		return m, nil

	case loginSuccessMsg:
		m.cfg.Token = msg.token.AccessToken
		m.cfg.RefreshToken = msg.token.RefreshToken
		m.cfg.Provider = msg.provider
		m.cfg.ShopID = msg.shopID
		if err := m.cfg.Save(); err != nil {
			m.state = loginStateError
			m.err = err
			return m, nil
		}
		return m, func() tea.Msg {
			return LoginSuccessMsg{}
		}

	case loginErrMsg:
		m.state = loginStateError
		m.err = msg.err
		return m, nil
	}

	var cmd tea.Cmd
	if m.state == loginStateEmail {
		m.emailInput, cmd = m.emailInput.Update(msg)
	} else if m.state == loginStatePassword {
		m.passInput, cmd = m.passInput.Update(msg)
	}
	return m, cmd
}

func (m loginModel) handleEnter() (tea.Model, tea.Cmd) {
	switch m.state {
	case loginStateEmail:
		if m.emailInput.Value() == "" {
			return m, nil
		}
		m.state = loginStateLoading
		return m, func() tea.Msg {
			providers, err := m.client.Providers(context.Background(), m.emailInput.Value())
			return providersMsg{providers: providers, err: err}
		}

	case loginStatePassword:
		if m.passInput.Value() == "" {
			return m, nil
		}
		m.state = loginStateLoading
		return m, func() tea.Msg {
			if m.selected >= len(m.providers) {
				return loginErrMsg{err: fmt.Errorf("no provider selected")}
			}
			p := m.providers[m.selected]
			token, err := m.client.Login(context.Background(), pocketbook_cloud_client.LoginRequest{
				ShopID:   p.ShopID,
				UserName: m.emailInput.Value(),
				Password: m.passInput.Value(),
				Provider: p.Alias,
			})
			if err != nil {
				return loginErrMsg{err: err}
			}
			return loginSuccessMsg{token: token, provider: p.Alias, shopID: p.ShopID}
		}

	case loginStateProvider:
		if len(m.providers) > 0 {
			m.state = loginStatePassword
			m.passInput.Focus()
			return m, textinput.Blink
		}

	case loginStateError:
		m.state = loginStateEmail
		m.err = nil
		m.emailInput.Focus()
		return m, textinput.Blink
	}

	return m, nil
}

func (m loginModel) View() string {
	if m.width == 0 || m.height == 0 {
		// Terminal size not yet known, render unstyled
		return m.renderContent()
	}
	content := m.renderContent()
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, content)
}

func (m loginModel) renderContent() string {
	var content string

	switch m.state {
	case loginStateEmail:
		content = lipgloss.JoinVertical(lipgloss.Left,
			TitleStyle.Render("PocketBook Cloud"),
			SubtitleStyle.Render("Enter your email to continue"),
			BoxStyle.Render(
				lipgloss.JoinVertical(lipgloss.Left,
					"Email:",
					m.emailInput.View(),
				),
			),
			HelpStyle.Render("enter: continue • esc: quit"),
		)

	case loginStatePassword:
		content = lipgloss.JoinVertical(lipgloss.Left,
			TitleStyle.Render("PocketBook Cloud"),
			SubtitleStyle.Render(fmt.Sprintf("Email: %s", m.emailInput.Value())),
			BoxStyle.Render(
				lipgloss.JoinVertical(lipgloss.Left,
					"Password:",
					m.passInput.View(),
				),
			),
			HelpStyle.Render("enter: login • esc: quit"),
		)

	case loginStateProvider:
		var list string
		for i, p := range m.providers {
			style := ItemStyle
			if i == m.selected {
				style = SelectedItemStyle.Copy().Foreground(PrimaryColor)
				list += style.Render("> " + p.Name) + "\n"
			} else {
				list += style.Render("  "+p.Name) + "\n"
			}
		}
		content = lipgloss.JoinVertical(lipgloss.Left,
			TitleStyle.Render("PocketBook Cloud"),
			SubtitleStyle.Render("Select your account provider"),
			BoxStyle.Render(list),
			HelpStyle.Render("↑/↓: select • enter: confirm • esc: quit"),
		)

	case loginStateLoading:
		content = lipgloss.JoinVertical(lipgloss.Left,
			TitleStyle.Render("PocketBook Cloud"),
			BoxStyle.Render("Loading..."),
		)

	case loginStateError:
		content = lipgloss.JoinVertical(lipgloss.Left,
			TitleStyle.Render("PocketBook Cloud"),
			BoxStyle.Render(
				lipgloss.JoinVertical(lipgloss.Left,
					ErrorStyle.Render("Error"),
					m.err.Error(),
				),
			),
			HelpStyle.Render("enter: retry • esc: quit"),
		)
	}

	return content
}

// LoginSuccessMsg is sent when login is successful.
type LoginSuccessMsg struct{}
