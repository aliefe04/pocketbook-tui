package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestRenewTokenTradesAStoredSessionForANewOne(t *testing.T) {
	var path, auth, contentType, grant, refresh string
	c := nativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		path, auth, contentType = r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Content-Type")
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		grant, refresh = r.Form.Get("grant_type"), r.Form.Get("refresh_token")
		_, _ = io.WriteString(w, `{"access_token":"new-access","token_type":"Bearer","expires_in":3600,"refresh_token":"new-refresh"}`)
	})

	token, err := c.RenewToken(context.Background(), "old-access", "stored-refresh")
	if err != nil {
		t.Fatalf("RenewToken: %v", err)
	}
	if path != "/auth/renew-token" || auth != "Bearer old-access" {
		t.Errorf("request = path %q auth %q", path, auth)
	}
	if contentType != "application/x-www-form-urlencoded" || grant != "refresh_token" || refresh != "stored-refresh" {
		t.Errorf("form = content type %q grant %q refresh %q", contentType, grant, refresh)
	}
	if token.AccessToken != "new-access" || token.RefreshToken != "new-refresh" {
		t.Errorf("token = %+v", token)
	}
	if token.ExpiresIn.Before(time.Now().Add(30 * time.Minute)) {
		t.Errorf("expiry %v is not in the future", token.ExpiresIn)
	}
}

func TestRenewTokenKeepsTheStoredRefreshTokenWhenTheServerOmitsOne(t *testing.T) {
	c := nativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"access_token":"new-access","expires_in":60}`)
	})
	token, err := c.RenewToken(context.Background(), "old-access", "stored-refresh")
	if err != nil {
		t.Fatalf("RenewToken: %v", err)
	}
	if token.RefreshToken != "stored-refresh" {
		t.Errorf("refresh token = %q, want the stored one", token.RefreshToken)
	}
}

func TestRenewTokenReportsARejectedSession(t *testing.T) {
	c := nativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	_, err := c.RenewToken(context.Background(), "old-access", "stale-refresh")
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.Status != http.StatusForbidden {
		t.Fatalf("error = %v, want a 403 the caller can recognize", err)
	}
}

func TestRenewTokenWithoutAStoredSessionDoesNotCallTheServer(t *testing.T) {
	called := false
	c := nativeServer(t, func(w http.ResponseWriter, r *http.Request) { called = true })
	if _, err := c.RenewToken(context.Background(), "", "refresh"); err == nil {
		t.Fatal("renewal without an access token was accepted")
	}
	if _, err := c.RenewToken(context.Background(), "access", ""); err == nil {
		t.Fatal("renewal without a refresh token was accepted")
	}
	if called {
		t.Fatal("a request without a stored session reached the server")
	}
}
