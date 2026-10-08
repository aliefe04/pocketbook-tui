package config

import (
	"os"
	"strings"
	"testing"
)

// useHome points the config directory at a fresh temp home for one test.
func useHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

func writePreferencesFile(t *testing.T, body string) {
	t.Helper()
	if err := os.MkdirAll(ConfigDir(), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(PreferencesPath(), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadPreferencesKeepsExplicitFalse(t *testing.T) {
	useHome(t)
	writePreferencesFile(t, `{"show_header": false, "show_footer": false, "cloud_sync": false}`)

	prefs, err := LoadPreferences()
	if err != nil {
		t.Fatalf("LoadPreferences: %v", err)
	}
	if prefs.ShowHeader || prefs.ShowFooter || prefs.CloudSync {
		t.Fatalf("explicit false was lost: header=%v footer=%v sync=%v",
			prefs.ShowHeader, prefs.ShowFooter, prefs.CloudSync)
	}
	if prefs.Theme != "auto" {
		t.Fatalf("unset field did not keep its default: theme=%q", prefs.Theme)
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	useHome(t)

	want := DefaultPreferences()
	want.Theme = "sepia"
	want.Accent = "rose"
	want.ReadingWidth = 48
	want.HorizontalMargin = 3
	want.ParagraphSpacing = 2
	want.Alignment = "justify"
	want.ShowFooter = false
	want.Progress = "both"
	want.ReadingMode = "scroll"
	want.PageOverlap = 2
	want.Mouse = true
	want.NavigationKeys = NavigationKeys{NextPage: "m", PrevPage: "h", LineDown: "x", LineUp: "w"}
	want.LibrarySort = "author"
	want.CompactLibrary = true
	want.CloudAutoRefresh = false
	want.CloudTimeoutSecs = 90

	if err := want.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(PreferencesPath())
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0600 {
		t.Fatalf("preferences mode = %o, want 0600", mode)
	}

	got, err := LoadPreferences()
	if err != nil {
		t.Fatalf("LoadPreferences: %v", err)
	}
	if got != want {
		t.Fatalf("round trip changed settings:\n got %+v\nwant %+v", got, want)
	}

	entries, err := os.ReadDir(ConfigDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("temporary file left behind: %s", e.Name())
		}
	}
}

func TestSaveDoesNotTouchAuthConfig(t *testing.T) {
	useHome(t)
	cfg := &Config{Token: "t", RefreshToken: "r", Provider: "p", ShopID: "s"}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(ConfigPath())
	if err != nil {
		t.Fatal(err)
	}

	prefs := DefaultPreferences()
	prefs.Theme = "dark"
	if err := prefs.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	after, err := os.ReadFile(ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("saving preferences changed config.json")
	}
}

func TestInvalidFileReportsErrorAndIsNotOverwritten(t *testing.T) {
	useHome(t)
	const body = `{"reading_width": 5}`
	writePreferencesFile(t, body)

	prefs, err := LoadPreferences()
	if err == nil {
		t.Fatal("LoadPreferences accepted reading_width 5")
	}
	if prefs != DefaultPreferences() {
		t.Fatalf("invalid file should fall back to defaults, got %+v", prefs)
	}

	data, err := os.ReadFile(PreferencesPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != body {
		t.Fatalf("invalid preferences file was rewritten: %q", data)
	}
}

func TestUnparseablePreferencesFileReportsError(t *testing.T) {
	useHome(t)
	writePreferencesFile(t, `{"theme": `)

	if _, err := LoadPreferences(); err == nil {
		t.Fatal("LoadPreferences accepted malformed JSON")
	}
}

func TestFailedSaveLeavesPreviousFileIntact(t *testing.T) {
	useHome(t)
	good := DefaultPreferences()
	good.Theme = "light"
	if err := good.Save(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(PreferencesPath())
	if err != nil {
		t.Fatal(err)
	}

	bad := good
	bad.Theme = "neon"
	if err := bad.Save(); err == nil {
		t.Fatal("Save accepted an invalid theme")
	}

	after, err := os.ReadFile(PreferencesPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("failed save changed the existing file")
	}
}

func TestValidateRanges(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Preferences)
		ok     bool
	}{
		{"width auto", func(p *Preferences) { p.ReadingWidth = 0 }, true},
		{"width minimum", func(p *Preferences) { p.ReadingWidth = 20 }, true},
		{"width maximum", func(p *Preferences) { p.ReadingWidth = 160 }, true},
		{"width below minimum", func(p *Preferences) { p.ReadingWidth = 19 }, false},
		{"width above maximum", func(p *Preferences) { p.ReadingWidth = 161 }, false},
		{"margin above maximum", func(p *Preferences) { p.HorizontalMargin = 13 }, false},
		{"vertical margin above maximum", func(p *Preferences) { p.VerticalMargin = 5 }, false},
		{"line spacing above maximum", func(p *Preferences) { p.LineSpacing = 3 }, false},
		{"paragraph spacing above maximum", func(p *Preferences) { p.ParagraphSpacing = 4 }, false},
		{"overlap above maximum", func(p *Preferences) { p.PageOverlap = 4 }, false},
		{"timeout below minimum", func(p *Preferences) { p.CloudTimeoutSecs = 4 }, false},
		{"timeout above maximum", func(p *Preferences) { p.CloudTimeoutSecs = 121 }, false},
		{"unknown accent", func(p *Preferences) { p.Accent = "teal" }, false},
		{"unknown sort", func(p *Preferences) { p.LibrarySort = "year" }, false},
		{"unknown progress", func(p *Preferences) { p.Progress = "chapters" }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := DefaultPreferences()
			tc.mutate(&p)
			if err := p.Validate(); (err == nil) != tc.ok {
				t.Fatalf("Validate() error = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

func TestNavigationKeyRules(t *testing.T) {
	cases := []struct {
		name string
		keys NavigationKeys
		ok   bool
	}{
		{"defaults", NavigationKeys{"space", "b", "j", "k"}, true},
		{"ctrl letter", NavigationKeys{"ctrl+f", "ctrl+b", "j", "k"}, true},
		{"reserved quit key", NavigationKeys{"q", "b", "j", "k"}, false},
		{"reserved escape", NavigationKeys{"esc", "b", "j", "k"}, false},
		{"reserved arrow", NavigationKeys{"space", "up", "j", "k"}, false},
		{"reserved page key", NavigationKeys{"space", "pgdown", "j", "k"}, false},
		{"reserved ctrl c", NavigationKeys{"space", "b", "ctrl+c", "k"}, false},
		{"duplicate", NavigationKeys{"space", "b", "j", "j"}, false},
		{"empty", NavigationKeys{"", "b", "j", "k"}, false},
		{"multi-character name", NavigationKeys{"tab", "b", "j", "k"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := DefaultPreferences()
			p.NavigationKeys = tc.keys
			if err := p.Validate(); (err == nil) != tc.ok {
				t.Fatalf("Validate() error = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

func TestBubbleteaSpaceNameIsAccepted(t *testing.T) {
	useHome(t)
	writePreferencesFile(t, `{"navigation_keys": {"next_page": " ", "prev_page": "b", "line_down": "j", "line_up": "k"}}`)

	prefs, err := LoadPreferences()
	if err != nil {
		t.Fatalf("LoadPreferences: %v", err)
	}
	if prefs.NavigationKeys.NextPage != "space" {
		t.Fatalf("space was not normalized, got %q", prefs.NavigationKeys.NextPage)
	}
}
