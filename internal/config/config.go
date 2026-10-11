package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Config struct {
	Token        string    `json:"token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	Provider     string    `json:"provider,omitempty"`
	ShopID       string    `json:"shop_id,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
}

// renewWindow is how long before expiry the stored session is renewed, so the
// app does not depend on a request failing first.
const renewWindow = 12 * time.Hour

func ConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", "pocketbook-tui")
}

func ConfigPath() string {
	return filepath.Join(ConfigDir(), "config.json")
}

func Load() (*Config, error) {
	path := ConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	return &cfg, nil
}

func (c *Config) Save() error {
	dir := ConfigDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	path := ConfigPath()
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	return nil
}

func (c *Config) IsLoggedIn() bool {
	return c.Token != ""
}

// SetSession stores a signed-in session and its expiry. An empty refresh token
// keeps the stored one, because the server may not issue a new one.
func (c *Config) SetSession(access, refresh string, expiresAt time.Time) {
	c.Token = access
	if refresh != "" {
		c.RefreshToken = refresh
	}
	c.ExpiresAt = expiresAt
}

// NeedsRenewal reports whether the stored session should be renewed before it
// is used. A session with no refresh token cannot be renewed, and a session
// with no stored expiry is renewed once so the expiry becomes known.
func (c *Config) NeedsRenewal(now time.Time) bool {
	if c.Token == "" || c.RefreshToken == "" {
		return false
	}
	if c.ExpiresAt.IsZero() {
		return true
	}
	return !now.Before(c.ExpiresAt.Add(-renewWindow))
}

// ClearAuth removes stored tokens, forcing re-login.
func (c *Config) ClearAuth() {
	c.Token = ""
	c.RefreshToken = ""
	c.ExpiresAt = time.Time{}
}
