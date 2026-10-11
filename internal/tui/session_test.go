package tui

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aliefe/pocketbook-tui/internal/api"
	"github.com/aliefe/pocketbook-tui/internal/config"
)

// renewServer answers one successful session renewal and reports the request.
func renewServer(t *testing.T, access, refresh string) (*api.Client, *string, *string) {
	t.Helper()
	var path, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		_, _ = io.WriteString(w, fmt.Sprintf(`{"access_token":%q,"expires_in":3600,"refresh_token":%q}`, access, refresh))
	}))
	t.Cleanup(srv.Close)
	return api.NewWithNativeTransport(srv.URL+"/", srv.Client()), &path, &auth
}

func TestStoredSessionRenewsWithoutAskingForThePassword(t *testing.T) {
	isolateHome(t)
	client, path, auth := renewServer(t, "fresh-access", "fresh-refresh")
	cfg := &config.Config{Token: "old-access", RefreshToken: "old-refresh", ExpiresAt: time.Now().Add(-time.Minute)}
	app := &App{client: client, cfg: cfg, screen: screenLibrary, prefs: config.DefaultPreferences()}
	app.library = newLibraryModel(client, cfg)

	next, cmd := app.Update(UnauthorizedMsg{})
	app = next.(*App)
	if cmd == nil {
		t.Fatal("a rejected session did not attempt a renewal")
	}
	next, _ = app.Update(cmd())
	app = next.(*App)

	if *path != "/auth/renew-token" || *auth != "Bearer old-access" {
		t.Fatalf("renewal request = %q %q", *path, *auth)
	}
	if app.screen == screenLogin {
		t.Fatal("the password was requested although the session could be renewed")
	}
	if cfg.Token != "fresh-access" || cfg.RefreshToken != "fresh-refresh" || cfg.ExpiresAt.IsZero() {
		t.Fatalf("session = %+v", cfg)
	}
	saved, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Token != "fresh-access" || saved.RefreshToken != "fresh-refresh" {
		t.Fatalf("the renewed session was not stored for the next run: %+v", saved)
	}
}

func TestARejectedRenewalAsksForThePassword(t *testing.T) {
	isolateHome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	client := api.NewWithNativeTransport(srv.URL+"/", srv.Client())
	cfg := &config.Config{Token: "old-access", RefreshToken: "stale-refresh"}
	app := &App{client: client, cfg: cfg, screen: screenLibrary, prefs: config.DefaultPreferences()}
	app.library = newLibraryModel(client, cfg)

	next, cmd := app.Update(UnauthorizedMsg{})
	app = next.(*App)
	next, _ = app.Update(cmd())
	app = next.(*App)

	if app.screen != screenLogin {
		t.Fatal("a rejected renewal left the app signed in")
	}
	if cfg.IsLoggedIn() || cfg.RefreshToken != "" {
		t.Fatalf("stale credentials were kept: %+v", cfg)
	}
}

func TestAnOfflineRenewalKeepsTheStoredSession(t *testing.T) {
	isolateHome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	client := api.NewWithNativeTransport(srv.URL+"/", srv.Client())
	srv.Close() // the network is gone
	cfg := &config.Config{Token: "access", RefreshToken: "refresh"}
	app := &App{client: client, cfg: cfg, screen: screenLibrary, prefs: config.DefaultPreferences()}
	app.library = newLibraryModel(client, cfg)

	next, cmd := app.Update(UnauthorizedMsg{})
	app = next.(*App)
	next, _ = app.Update(cmd())
	app = next.(*App)

	if app.screen == screenLogin || !cfg.IsLoggedIn() {
		t.Fatal("a network failure signed the user out")
	}
}

func TestStartupRenewsAStoredSessionBeforeItExpires(t *testing.T) {
	isolateHome(t)
	// A session past its renewal window is renewed at startup, so the app never
	// waits for a rejected request before keeping the session alive.
	due := &config.Config{Token: "access", RefreshToken: "refresh", ExpiresAt: time.Now().Add(-time.Hour)}
	app := &App{cfg: due, screen: screenLibrary, prefs: config.DefaultPreferences()}
	app.library = newLibraryModel(nil, due)
	_ = app.Init()
	if !app.renewTried {
		t.Fatal("a session past its renewal window was not renewed at startup")
	}

	fresh := &config.Config{Token: "access", RefreshToken: "refresh", ExpiresAt: time.Now().Add(30 * 24 * time.Hour)}
	app = &App{cfg: fresh, screen: screenLibrary, prefs: config.DefaultPreferences()}
	app.library = newLibraryModel(nil, fresh)
	_ = app.Init()
	if app.renewTried {
		t.Fatal("a fresh session was renewed at startup")
	}
}

func TestLibraryUnauthorizedKeepsCredentialsForRenewal(t *testing.T) {
	isolateHome(t)
	cfg := &config.Config{Token: "access", RefreshToken: "refresh"}
	m := newLibraryModel(nil, cfg)
	next, cmd := m.Update(booksLoadedMsg{err: fmt.Errorf("list books: 401 unauthorized")})
	m = next.(libraryModel)
	if cfg.Token != "access" || cfg.RefreshToken != "refresh" {
		t.Fatal("the library cleared the stored session before a renewal")
	}
	if cmd == nil {
		t.Fatal("no reauthorization message was sent")
	}
	if _, ok := cmd().(UnauthorizedMsg); !ok {
		t.Fatalf("message = %T, want UnauthorizedMsg", cmd())
	}
}
