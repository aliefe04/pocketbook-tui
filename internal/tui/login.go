package tui

import (
	"context"
	"fmt"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
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

// loginIndent is the left indent of every login row.
const loginIndent = "  "

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
	m.emailInput.PromptStyle = accentStyle

	m.passInput = textinput.New()
	m.passInput.Placeholder = "password"
	m.passInput.EchoMode = textinput.EchoPassword
	m.passInput.EchoCharacter = '•'
	m.passInput.PromptStyle = accentStyle

	m.resizeInputs()
	return m
}

func (m loginModel) Init() tea.Cmd {
	return textinput.Blink
}

// resizeInputs sizes both text inputs to the terminal width, leaving room for
// the indent and prompt.
func (m *loginModel) resizeInputs() {
	w, _ := termSize(m.width, m.height)
	inputWidth := min(max(w-6, 10), 50)
	m.emailInput.Width = inputWidth
	m.passInput.Width = inputWidth
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

// LoginSuccessMsg is sent when login is successful.
type LoginSuccessMsg struct{}

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
		m.selected = 0
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
	if tooSmall(m.width, m.height) {
		return tooSmallView(m.width, m.height)
	}
	w, h := termSize(m.width, m.height)

	header := []string{titleRow(w, "PocketBook Cloud", "Sign in")}
	body, hints := m.stateBody(w, h, len(header))
	footer := []string{"", hintLine(w, hints...)}
	return frame(w, h, header, body, footer)
}

// stateBody returns the rows for the current state and the key hints to show
// under them. headerRows is the number of rows above the body, so the provider
// list can size its scroll window.
func (m loginModel) stateBody(width, height, headerRows int) ([]string, []keyHint) {
	quit := keyHint{"esc", "quit"}
	switch m.state {
	case loginStateEmail:
		return []string{
			"",
			labelStyle.Render(loginIndent + "Email"),
			loginIndent + m.emailInput.View(),
		}, []keyHint{{"enter", "continue"}, quit}

	case loginStatePassword:
		return []string{
			"",
			mutedStyle.Render(loginIndent + truncate("Email: "+m.emailInput.Value(), width-len(loginIndent))),
			"",
			labelStyle.Render(loginIndent + "Password"),
			loginIndent + m.passInput.View(),
		}, []keyHint{{"enter", "sign in"}, quit}

	case loginStateProvider:
		prelude := []string{"", mutedStyle.Render(loginIndent + "Select your account provider")}
		footerRows := 2
		capacity := max(height-headerRows-len(prelude)-footerRows, 1)
		start := scrollWindow(0, m.selected, len(m.providers), capacity)
		end := min(start+capacity, len(m.providers))

		body := prelude
		for i := start; i < end; i++ {
			body = append(body, providerRow(m.providers[i].Name, i == m.selected, width))
		}
		return body, []keyHint{{"↑/↓", "select"}, {"enter", "confirm"}, quit}

	case loginStateLoading:
		return []string{"", mutedStyle.Render(loginIndent + "Contacting PocketBook Cloud…")}, nil

	case loginStateError:
		text := "unknown error"
		if m.err != nil {
			text = m.err.Error()
		}
		body := []string{"", errorStyle.Render(loginIndent + "Could not continue")}
		for _, line := range wrapText(text, width-len(loginIndent)) {
			body = append(body, textStyle.Render(loginIndent+line))
		}
		return body, []keyHint{{"enter", "retry"}, quit}
	}
	return nil, nil
}

// providerRow renders one provider in the selection list.
func providerRow(name string, selected bool, width int) string {
	name = truncate(name, width-4)
	if selected {
		return selectedMark.Render("› ") + selectedStyle.Width(width-2).Render(name)
	}
	return loginIndent + textStyle.Render(name)
}
