package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

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

	// A provider lookup takes several seconds on the server, so the result is
	// fetched while the user is still typing and kept per email.
	cached   map[string][]pocketbook_cloud_client.Provider
	inflight string
	pending  string
	started  time.Time
	cancel   context.CancelFunc
}

// providersTimeout bounds one provider lookup. The server answers in about
// eight seconds, so a slow network still ends with a readable error.
const providersTimeout = 45 * time.Second

// lookupDelay is how long typing must pause before the provider lookup starts.
const lookupDelay = 350 * time.Millisecond

// emailIdleMsg fires after typing pauses in the email field.
type emailIdleMsg struct{ email string }

// lookupTickMsg repaints the elapsed time while a lookup is running.
type lookupTickMsg struct{}

func newLoginModel(client *api.Client, cfg *config.Config) loginModel {
	m := loginModel{
		client: client,
		cfg:    cfg,
		state:  loginStateEmail,
		cached: make(map[string][]pocketbook_cloud_client.Provider),
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
	email     string
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
			if m.state == loginStateLoading {
				if m.cancel != nil {
					m.cancel()
					m.cancel = nil
				}
				m.inflight, m.pending = "", ""
				m.state = loginStateEmail
				m.emailInput.Focus()
				return m, textinput.Blink
			}
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
		if m.inflight == msg.email {
			m.inflight, m.cancel = "", nil
		}
		if msg.err == nil {
			m.cached[msg.email] = msg.providers
		}
		if msg.email != m.pending || m.state != loginStateLoading {
			// A prefetch result. The user is still typing, so the list waits.
			return m, nil
		}
		m.pending = ""
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
			return m, nil
		}
		m.state = loginStateProvider
		return m, nil

	case emailIdleMsg:
		return m.startLookup(msg.email)

	case lookupTickMsg:
		if m.state != loginStateLoading {
			return m, nil
		}
		return m, tickLookup()

	case loginSuccessMsg:
		m.cfg.SetSession(msg.token.AccessToken, msg.token.RefreshToken, msg.token.ExpiresIn)
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
		if key, ok := msg.(tea.KeyMsg); ok && key.Type == tea.KeyRunes {
			// The provider lookup takes seconds on the server, so it starts as
			// soon as typing pauses instead of on the next Enter.
			email := strings.TrimSpace(m.emailInput.Value())
			cmd = tea.Batch(cmd, tea.Tick(lookupDelay, func(time.Time) tea.Msg {
				return emailIdleMsg{email: email}
			}))
		}
	} else if m.state == loginStatePassword {
		m.passInput, cmd = m.passInput.Update(msg)
	}
	return m, cmd
}

// startLookup begins a provider lookup in the background. The result arrives as
// a providersMsg. A lookup already running for the same email is not repeated.
func (m loginModel) startLookup(email string) (tea.Model, tea.Cmd) {
	if !looksLikeEmail(email) || m.inflight == email {
		return m, nil
	}
	if _, ok := m.cached[email]; ok {
		return m, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), providersTimeout)
	m.cancel, m.inflight = cancel, email
	client := m.client
	return m, func() tea.Msg {
		providers, err := client.Providers(ctx, email)
		return providersMsg{providers: providers, err: err, email: email}
	}
}

// tickLookup repaints the elapsed time while a lookup runs.
func tickLookup() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return lookupTickMsg{} })
}

// looksLikeEmail reports whether the text is worth sending to the provider
// lookup.
func looksLikeEmail(value string) bool {
	value = strings.TrimSpace(value)
	at := strings.IndexByte(value, '@')
	return at > 0 && strings.Contains(value[at+1:], ".")
}

func (m loginModel) handleEnter() (tea.Model, tea.Cmd) {
	switch m.state {
	case loginStateEmail:
		email := strings.TrimSpace(m.emailInput.Value())
		if email == "" {
			return m, nil
		}
		m.pending = email
		if providers, ok := m.cached[email]; ok {
			// The lookup already finished while the user was typing.
			m.providers, m.selected, m.state = providers, 0, loginStateProvider
			return m, nil
		}
		m.state = loginStateLoading
		m.started = time.Now()
		next, cmd := m.startLookup(email)
		return next, tea.Batch(cmd, tickLookup())

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
		text := "Contacting PocketBook Cloud…"
		if elapsed := int(time.Since(m.started).Seconds()); elapsed >= 3 {
			text = fmt.Sprintf("Contacting PocketBook Cloud… %ds", elapsed)
		}
		return []string{"", mutedStyle.Render(loginIndent + text)}, []keyHint{{"esc", "cancel"}}

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
