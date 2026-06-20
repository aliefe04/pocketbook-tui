package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	Token        string `json:"token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Provider     string `json:"provider,omitempty"`
	ShopID       string `json:"shop_id,omitempty"`
}

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

// ClearAuth removes stored tokens, forcing re-login.
func (c *Config) ClearAuth() {
	c.Token = ""
	c.RefreshToken = ""
}
