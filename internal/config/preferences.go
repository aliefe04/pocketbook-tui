package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
)

// Preferences are user settings from the in-app settings screen. They live in
// their own file, so authentication and saved reading positions are never
// touched by a settings change.
type Preferences struct {
	Theme            string         `json:"theme"`
	Accent           string         `json:"accent"`
	ReadingMode      string         `json:"reading_mode"`
	ReadingWidth     int            `json:"reading_width"`
	HorizontalMargin int            `json:"horizontal_margin"`
	VerticalMargin   int            `json:"vertical_margin"`
	LineSpacing      int            `json:"line_spacing"`
	ParagraphSpacing int            `json:"paragraph_spacing"`
	Alignment        string         `json:"alignment"`
	ShowHeader       bool           `json:"show_header"`
	ShowFooter       bool           `json:"show_footer"`
	Progress         string         `json:"progress"`
	PageOverlap      int            `json:"page_overlap"`
	Mouse            bool           `json:"mouse"`
	NavigationKeys   NavigationKeys `json:"navigation_keys"`
	LibrarySort      string         `json:"library_sort"`
	CompactLibrary   bool           `json:"compact_library"`
	CloudAutoRefresh bool           `json:"cloud_auto_refresh"`
	CloudSync        bool           `json:"cloud_sync"`
	CloudTimeoutSecs int            `json:"cloud_timeout_seconds"`
}

// NavigationKeys are the reader keys for paging and line movement. Each value
// is a canonical key name: "space" for the space bar, a single printable
// character, or "ctrl+<letter>".
type NavigationKeys struct {
	NextPage string `json:"next_page"`
	PrevPage string `json:"prev_page"`
	LineDown string `json:"line_down"`
	LineUp   string `json:"line_up"`
}

// Option lists. The settings screen cycles through these in order.
var (
	Themes        = []string{"auto", "dark", "light", "sepia"}
	Accents       = []string{"blue", "green", "amber", "purple", "rose"}
	ReadingModes  = []string{"page", "scroll"}
	Alignments    = []string{"left", "justify"}
	ProgressModes = []string{"percent", "pages", "both", "none"}
	LibrarySorts  = []string{"recent", "title", "author"}
	ReservedKeys  = []string{"q", "esc", "ctrl+c", "C", "?", "S", "P", "n", "p", "g", "G", "o", "c", "l", "r", "enter", "pgup", "pgdown", "up", "down", "left", "right", "d", "u"}
)

const defaultKeyName = "space"

const (
	ReadingWidthMax         = 160
	ReadingWidthMin         = 20
	HorizontalMarginMax     = 12
	VerticalMarginMax       = 4
	LineSpacingMax          = 2
	ParagraphSpacingMax     = 3
	PageOverlapMax          = 3
	CloudTimeoutMinSecs     = 5
	CloudTimeoutMaxSecs     = 120
	defaultCloudTimeoutSecs = 30
)

// DefaultPreferences returns the settings used when no preferences file exists
// and the baseline that a partial file is decoded onto.
func DefaultPreferences() Preferences {
	return Preferences{
		Theme:            "auto",
		Accent:           "blue",
		ReadingMode:      "page",
		ReadingWidth:     0,
		HorizontalMargin: 0,
		VerticalMargin:   0,
		LineSpacing:      0,
		ParagraphSpacing: 0,
		Alignment:        "left",
		ShowHeader:       true,
		ShowFooter:       true,
		Progress:         "percent",
		PageOverlap:      0,
		Mouse:            false,
		NavigationKeys: NavigationKeys{
			NextPage: "space",
			PrevPage: "b",
			LineDown: "j",
			LineUp:   "k",
		},
		LibrarySort:      "recent",
		CompactLibrary:   false,
		CloudAutoRefresh: true,
		CloudSync:        true,
		CloudTimeoutSecs: defaultCloudTimeoutSecs,
	}
}

func PreferencesPath() string {
	return filepath.Join(ConfigDir(), "preferences.json")
}

// LoadPreferences reads the preferences file. A missing file yields defaults
// with no error. A file that cannot be read, parsed, or validated yields
// defaults together with an error, and the file is left unchanged so the
// caller can show the problem without blocking login or reading.
func LoadPreferences() (Preferences, error) {
	prefs := DefaultPreferences()
	data, err := os.ReadFile(PreferencesPath())
	if err != nil {
		if os.IsNotExist(err) {
			return prefs, nil
		}
		return DefaultPreferences(), fmt.Errorf("read preferences: %w", err)
	}

	if err := json.Unmarshal(data, &prefs); err != nil {
		return DefaultPreferences(), fmt.Errorf("parse preferences: %w", err)
	}
	prefs.NavigationKeys.normalize()
	if err := prefs.Validate(); err != nil {
		return DefaultPreferences(), fmt.Errorf("invalid preferences: %w", err)
	}
	return prefs, nil
}

// Validate reports the first setting that is out of range or not allowed.
func (p Preferences) Validate() error {
	if !slices.Contains(Themes, p.Theme) {
		return fmt.Errorf("theme must be one of %s", strings.Join(Themes, ", "))
	}
	if !slices.Contains(Accents, p.Accent) {
		return fmt.Errorf("accent must be one of %s", strings.Join(Accents, ", "))
	}
	if !slices.Contains(ReadingModes, p.ReadingMode) {
		return fmt.Errorf("reading_mode must be one of %s", strings.Join(ReadingModes, ", "))
	}
	if p.ReadingWidth != 0 && (p.ReadingWidth < ReadingWidthMin || p.ReadingWidth > ReadingWidthMax) {
		return fmt.Errorf("reading_width must be 0 (auto) or %d to %d", ReadingWidthMin, ReadingWidthMax)
	}
	if err := inRange("horizontal_margin", p.HorizontalMargin, 0, HorizontalMarginMax); err != nil {
		return err
	}
	if err := inRange("vertical_margin", p.VerticalMargin, 0, VerticalMarginMax); err != nil {
		return err
	}
	if err := inRange("line_spacing", p.LineSpacing, 0, LineSpacingMax); err != nil {
		return err
	}
	if err := inRange("paragraph_spacing", p.ParagraphSpacing, 0, ParagraphSpacingMax); err != nil {
		return err
	}
	if !slices.Contains(Alignments, p.Alignment) {
		return fmt.Errorf("alignment must be one of %s", strings.Join(Alignments, ", "))
	}
	if !slices.Contains(ProgressModes, p.Progress) {
		return fmt.Errorf("progress must be one of %s", strings.Join(ProgressModes, ", "))
	}
	if err := inRange("page_overlap", p.PageOverlap, 0, PageOverlapMax); err != nil {
		return err
	}
	if !slices.Contains(LibrarySorts, p.LibrarySort) {
		return fmt.Errorf("library_sort must be one of %s", strings.Join(LibrarySorts, ", "))
	}
	if err := inRange("cloud_timeout_seconds", p.CloudTimeoutSecs, CloudTimeoutMinSecs, CloudTimeoutMaxSecs); err != nil {
		return err
	}
	return p.NavigationKeys.validate()
}

// Save validates p and writes it atomically: the data goes to a temporary file
// in the config directory, which is renamed over preferences.json only after
// the write and sync succeed. A failed save leaves the previous file intact.
func (p *Preferences) Save() error {
	if err := p.Validate(); err != nil {
		return err
	}
	dir := ConfigDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal preferences: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, ".preferences-*.tmp")
	if err != nil {
		return fmt.Errorf("write preferences: %w", err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write preferences: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write preferences: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write preferences: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write preferences: %w", err)
	}
	if err := os.Rename(tmpName, PreferencesPath()); err != nil {
		return fmt.Errorf("write preferences: %w", err)
	}
	committed = true
	return nil
}

// KeyName returns the canonical name for a key reported by bubbletea. The
// space key is reported as " " but is stored as "space".
func KeyName(s string) string {
	if s == " " {
		return defaultKeyName
	}
	return s
}

func (n *NavigationKeys) normalize() {
	n.NextPage = KeyName(n.NextPage)
	n.PrevPage = KeyName(n.PrevPage)
	n.LineDown = KeyName(n.LineDown)
	n.LineUp = KeyName(n.LineUp)
}

func (n NavigationKeys) validate() error {
	keys := []struct{ field, key string }{
		{"next_page", n.NextPage},
		{"prev_page", n.PrevPage},
		{"line_down", n.LineDown},
		{"line_up", n.LineUp},
	}
	seen := make(map[string]string, len(keys))
	for _, k := range keys {
		if err := validKey(k.key); err != nil {
			return fmt.Errorf("navigation_keys.%s: %w", k.field, err)
		}
		if other, dup := seen[k.key]; dup {
			return fmt.Errorf("navigation_keys.%s and navigation_keys.%s both use %q", other, k.field, k.key)
		}
		seen[k.key] = k.field
	}
	return nil
}

// validKey accepts a canonical key name that the reader can receive and that
// no other screen command uses.
func validKey(k string) error {
	if k == "" {
		return fmt.Errorf("key is empty")
	}
	if slices.Contains(ReservedKeys, k) {
		return fmt.Errorf("key %q is used by another command", k)
	}
	if k == defaultKeyName {
		return nil
	}
	if r := []rune(k); len(r) == 1 && unicode.IsGraphic(r[0]) && !unicode.IsSpace(r[0]) {
		return nil
	}
	if rest, ok := strings.CutPrefix(k, "ctrl+"); ok && len(rest) == 1 && rest[0] >= 'a' && rest[0] <= 'z' {
		return nil
	}
	return fmt.Errorf("unsupported key %q", k)
}

func inRange(name string, v, lo, hi int) error {
	if v < lo || v > hi {
		return fmt.Errorf("%s must be %d to %d", name, lo, hi)
	}
	return nil
}
