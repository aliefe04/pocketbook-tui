package config

import (
	"testing"
	"time"
)

func TestNeedsRenewalOnlyForASessionThatCanBeRenewed(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		cfg  Config
		want bool
	}{
		{"signed out", Config{}, false},
		{"no refresh token", Config{Token: "access"}, false},
		{"unknown expiry", Config{Token: "access", RefreshToken: "refresh"}, true},
		{"expired", Config{Token: "access", RefreshToken: "refresh", ExpiresAt: now.Add(-time.Minute)}, true},
		{"inside the renewal window", Config{Token: "access", RefreshToken: "refresh", ExpiresAt: now.Add(time.Hour)}, true},
		{"still fresh", Config{Token: "access", RefreshToken: "refresh", ExpiresAt: now.Add(20 * 24 * time.Hour)}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.NeedsRenewal(now); got != tc.want {
				t.Fatalf("NeedsRenewal = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSetSessionKeepsTheStoredRefreshTokenWhenNoneIsIssued(t *testing.T) {
	expiry := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	cfg := Config{Token: "old", RefreshToken: "refresh"}
	cfg.SetSession("new", "", expiry)
	if cfg.Token != "new" || cfg.RefreshToken != "refresh" || !cfg.ExpiresAt.Equal(expiry) {
		t.Fatalf("session = %+v", cfg)
	}
	cfg.SetSession("newer", "rotated", expiry)
	if cfg.RefreshToken != "rotated" {
		t.Fatalf("a rotated refresh token was not stored: %+v", cfg)
	}
}

func TestClearAuthForgetsTheExpiry(t *testing.T) {
	cfg := Config{Token: "access", RefreshToken: "refresh", ExpiresAt: time.Now()}
	cfg.ClearAuth()
	if cfg.IsLoggedIn() || !cfg.ExpiresAt.IsZero() || cfg.RefreshToken != "" {
		t.Fatalf("signed out session still holds data: %+v", cfg)
	}
}
