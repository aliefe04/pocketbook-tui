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
