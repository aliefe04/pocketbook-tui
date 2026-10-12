package tui

import (
	"testing"
	"time"

	pbc "github.com/micronull/pocketbook-cloud-client"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/aliefe/pocketbook-tui/internal/config"
)

func TestCachedProvidersSkipTheServerWait(t *testing.T) {
	m := newLoginModel(nil, &config.Config{})
	email := "reader@example.com"
	m.cached[email] = []pbc.Provider{{Alias: "pocketbook", Name: "PocketBook"}}
	m.emailInput.SetValue(email)

	next, cmd := m.Update(enterKey)
	m = next.(loginModel)
	if m.state != loginStateProvider || len(m.providers) != 1 || cmd != nil {
		t.Fatalf("state = %v, providers = %d, cmd = %v", m.state, len(m.providers), cmd)
	}
	assertContains(t, m.View(), "PocketBook")
}

func TestPrefetchDuringTypingStartsOneLookupPerEmail(t *testing.T) {
	m := newLoginModel(nil, &config.Config{})
	next, cmd := m.Update(emailIdleMsg{email: "not-an-email"})
	m = next.(loginModel)
	if cmd != nil || m.inflight != "" {
		t.Fatal("an incomplete address started a lookup")
	}
	next, cmd = m.Update(emailIdleMsg{email: "reader@example.com"})
	m = next.(loginModel)
	if cmd == nil || m.inflight != "reader@example.com" {
		t.Fatal("a complete address did not start a lookup")
	}
	next, again := m.Update(emailIdleMsg{email: "reader@example.com"})
	m = next.(loginModel)
	if again != nil {
		t.Fatal("the same address started a second lookup")
	}
}

func TestPrefetchResultForAnotherEmailStaysQuiet(t *testing.T) {
	m := newLoginModel(nil, &config.Config{})
	m.state = loginStateEmail
	next, _ := m.Update(providersMsg{email: "other@example.com", providers: []pbc.Provider{{Alias: "a", Name: "Other"}}})
	m = next.(loginModel)
	if m.state != loginStateEmail {
		t.Fatalf("a background lookup changed the screen to %v", m.state)
	}
	if len(m.cached["other@example.com"]) != 1 {
		t.Fatal("the background result was not kept for the next enter")
	}
}

func TestEscapeCancelsAProviderLookupWithoutQuitting(t *testing.T) {
	m := newLoginModel(nil, &config.Config{})
	m.state = loginStateLoading
	m.pending = "reader@example.com"
	cancelled := false
	m.cancel = func() { cancelled = true }

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(loginModel)
	if !cancelled || m.state != loginStateEmail || cmd == nil {
		t.Fatalf("cancel = %v, state = %v, cmd = %v", cancelled, m.state, cmd)
	}
}

func TestSlowLookupShowsElapsedSecondsAndACancelHint(t *testing.T) {
	m := newLoginModel(nil, &config.Config{})
	m.width, m.height = 40, 14
	m.state = loginStateLoading
	m.started = time.Now().Add(-6 * time.Second)
	view := m.View()
	assertContains(t, view, "6s")
	assertContains(t, view, "esc")
}

// signedInConfig is a config that already knows the provider of the last
// sign-in, the way a returning reader has it.
func signedInConfig() *config.Config {
	return &config.Config{Provider: "pocketbook", ProviderName: "PocketBook", ShopID: "1"}
}

func TestSavedProviderSkipsTheProviderWait(t *testing.T) {
	m := newLoginModel(nil, signedInConfig())
	m.emailInput.SetValue("reader@example.com")

	next, cmd := m.Update(enterKey)
	m = next.(loginModel)
	if m.state != loginStateProvider {
		t.Fatalf("state = %v, want the provider step without a wait", m.state)
	}
	if len(m.providers) != 1 || m.providers[0].Alias != "pocketbook" || m.providers[0].ShopID != "1" {
		t.Fatalf("providers = %+v", m.providers)
	}
	if cmd == nil || m.inflight != "reader@example.com" {
		t.Fatalf("cmd = %v, inflight = %q, want a background refresh", cmd, m.inflight)
	}
	assertContains(t, m.View(), "PocketBook")
}

func TestSavedProviderWithoutANameShowsTheAlias(t *testing.T) {
	m := newLoginModel(nil, &config.Config{Provider: "pocketbook", ShopID: "1"})
	m.emailInput.SetValue("reader@example.com")
	next, _ := m.Update(enterKey)
	m = next.(loginModel)
	if m.state != loginStateProvider || len(m.providers) != 1 {
		t.Fatalf("state = %v, providers = %d", m.state, len(m.providers))
	}
	if m.providers[0].Name != "pocketbook" {
		t.Fatalf("name = %q, want the alias", m.providers[0].Name)
	}
}

func TestProviderStepSaysItIsCheckingServers(t *testing.T) {
	m := newLoginModel(nil, signedInConfig())
	m.emailInput.SetValue("reader@example.com")
	next, _ := m.Update(enterKey)
	m = next.(loginModel)
	for _, size := range [][2]int{{20, 8}, {40, 14}, {60, 20}} {
		m.width, m.height = size[0], size[1]
		view := m.View()
		assertFits(t, view, size[0], size[1])
		assertContains(t, view, "Checking")
	}

	m.width, m.height = 60, 20
	next, _ = m.Update(providersMsg{email: "reader@example.com", providers: []pbc.Provider{
		{Alias: "pocketbook", Name: "PocketBook", ShopID: "1"},
	}})
	m = next.(loginModel)
	assertNotContains(t, m.View(), "Checking available servers")
}

func TestProviderRefreshKeepsTheSelection(t *testing.T) {
	m := newLoginModel(nil, signedInConfig())
	m.emailInput.SetValue("reader@example.com")
	next, _ := m.Update(enterKey)
	m = next.(loginModel)

	next, _ = m.Update(providersMsg{email: "reader@example.com", providers: []pbc.Provider{
		{Alias: "other", Name: "Other Store", ShopID: "2"},
		{Alias: "pocketbook", Name: "PocketBook", ShopID: "1"},
	}})
	m = next.(loginModel)
	if len(m.providers) != 2 || m.selected != 1 {
		t.Fatalf("providers = %d, selected = %d, want the saved provider still selected", len(m.providers), m.selected)
	}
}

func TestProviderRefreshDropsAStaleSavedProvider(t *testing.T) {
	m := newLoginModel(nil, signedInConfig())
	m.width, m.height = 60, 20
	m.emailInput.SetValue("reader@example.com")
	next, _ := m.Update(enterKey)
	m = next.(loginModel)

	next, _ = m.Update(providersMsg{email: "reader@example.com", providers: []pbc.Provider{
		{Alias: "other", Name: "Other Store", ShopID: "2"},
	}})
	m = next.(loginModel)
	if len(m.providers) != 1 || m.selected != 0 || m.providers[0].Alias != "other" {
		t.Fatalf("providers = %+v, selected = %d", m.providers, m.selected)
	}
	assertContains(t, m.View(), "Other Store")
}

func TestProviderRefreshDoesNotDisturbThePasswordStep(t *testing.T) {
	m := newLoginModel(nil, signedInConfig())
	m.emailInput.SetValue("reader@example.com")
	next, _ := m.Update(enterKey)
	m = next.(loginModel)
	next, _ = m.Update(enterKey)
	m = next.(loginModel)
	if m.state != loginStatePassword {
		t.Fatalf("state = %v, want the password step", m.state)
	}

	next, _ = m.Update(providersMsg{email: "reader@example.com", providers: []pbc.Provider{
		{Alias: "other", Name: "Other Store", ShopID: "2"},
	}})
	m = next.(loginModel)
	if m.state != loginStatePassword || len(m.providers) != 1 || m.providers[0].Alias != "pocketbook" {
		t.Fatalf("state = %v, providers = %+v", m.state, m.providers)
	}
}

func TestSuccessfulLoginStoresTheProviderName(t *testing.T) {
	isolateHome(t)
	cfg := &config.Config{}
	m := newLoginModel(nil, cfg)
	next, _ := m.Update(loginSuccessMsg{
		token:        pbc.Token{AccessToken: "access", RefreshToken: "refresh", ExpiresIn: time.Now().Add(time.Hour)},
		provider:     "pocketbook",
		providerName: "PocketBook",
		shopID:       "1",
	})
	m = next.(loginModel)
	if m.cfg.ProviderName != "PocketBook" || m.cfg.Provider != "pocketbook" || m.cfg.ShopID != "1" {
		t.Fatalf("config = provider %q, name %q, shop %q", m.cfg.Provider, m.cfg.ProviderName, m.cfg.ShopID)
	}
}
