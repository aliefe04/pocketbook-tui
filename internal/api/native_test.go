package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// nativeServer serves h and returns a client whose native endpoints are on it.
func nativeServer(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewWithNativeTransport(srv.URL+"/", srv.Client())
}

func TestNativePositionReadsTheDocumentPosition(t *testing.T) {
	var path, hash, auth string
	c := nativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		hash = r.URL.Query().Get("fast_hash")
		auth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"status":"ok","hash":"opaque-resource-id","baseUrl":"https://signed.example/x?sig=secret",`+
			`"position":{"pointer":"#epubcfi(/6/24!/4/4/1)","pointer_pb":"#epubcfi(/6/24!/4/4/1)","percent":3,"updated":"2026-10-08T10:11:12Z"}}`)
	})

	pos, err := c.NativePosition(context.Background(), "tok", "HASH1")
	if err != nil {
		t.Fatalf("NativePosition: %v", err)
	}
	if path != "/reader/documentInformation" || hash != "HASH1" || auth != "Bearer tok" {
		t.Errorf("request = path %q hash %q auth %q", path, hash, auth)
	}
	if pos.Pointer != "#epubcfi(/6/24!/4/4/1)" || pos.PointerPb != pos.Pointer || pos.Percent != 3 {
		t.Errorf("position = %+v", pos)
	}
	if pos.Updated.IsZero() {
		t.Errorf("updated time was not read")
	}
}

func TestNativePositionWithoutAPositionIsEmpty(t *testing.T) {
	c := nativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"status":"ok","hash":"opaque"}`)
	})
	pos, err := c.NativePosition(context.Background(), "tok", "HASH1")
	if err != nil {
		t.Fatalf("NativePosition: %v", err)
	}
	if pos != (NativePosition{}) {
		t.Errorf("position = %+v, want the zero value", pos)
	}
}

func TestSaveNativePositionSendsOffsAndBothPointers(t *testing.T) {
	var path, hash, contentType string
	var body map[string]any
	c := nativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		hash = r.URL.Query().Get("fast_hash")
		contentType = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		// The endpoint answers with plain text, not JSON.
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "OK")
	})

	const pointer = "#epubcfi(/6/22!/4/2/1:0)"
	if err := c.SaveNativePosition(context.Background(), "tok", "HASH1", 3, pointer); err != nil {
		t.Fatalf("SaveNativePosition rejected a plain OK: %v", err)
	}
	if path != "/books/read-position" || hash != "HASH1" || contentType != "application/json" {
		t.Errorf("request = path %q hash %q content type %q", path, hash, contentType)
	}
	if body["offs"] != float64(3) || body["pointer"] != pointer || body["pointer_pb"] != pointer {
		t.Errorf("body = %v, want offs 3 with the same CFI in both pointer fields", body)
	}
}

func TestNativeEndpointsReportStatusWithoutTheResponseBody(t *testing.T) {
	c := nativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "SECRET-RESPONSE-BODY")
	})

	_, err := c.NativePosition(context.Background(), "tok", "HASH1")
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("NativePosition error = %v, want the status", err)
	}
	if strings.Contains(err.Error(), "SECRET") {
		t.Errorf("error leaked the response body: %v", err)
	}

	err = c.SaveNativePosition(context.Background(), "tok", "HASH1", 3, "#epubcfi(/6/2!/4/2/1:0)")
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("SaveNativePosition error = %v, want the status and no body", err)
	}
}

func TestNativeUnauthorizedStatusKeepsTheReauthenticationSignal(t *testing.T) {
	c := nativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	_, err := c.NativePosition(context.Background(), "tok", "HASH1")
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("error = %v, want the 401 status the UI checks for", err)
	}
}

func TestNativeRedirectsAreRefusedAndTheTokenIsNotSent(t *testing.T) {
	var targetCalls int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalls++
		_, _ = io.WriteString(w, `{"position":{"pointer":"#epubcfi(/6/2!/4/2/1:0)"}}`)
	}))
	defer target.Close()

	c := nativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusFound)
	})

	_, err := c.NativePosition(context.Background(), "tok", "HASH1")
	if err == nil || !strings.Contains(err.Error(), "redirect refused") {
		t.Fatalf("error = %v, want a refused redirect", err)
	}
	if targetCalls != 0 {
		t.Errorf("the redirect target was called %d times; the bearer token must not follow", targetCalls)
	}
}

func TestNativeErrorsOmitTheRequestURL(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL + "/"
	srv.Close() // nothing listens at base any more

	c := NewWithNativeTransport(base, nil)
	_, err := c.NativePosition(context.Background(), "tok", "HASH-SECRET")
	if err == nil {
		t.Fatalf("NativePosition against a closed server succeeded")
	}
	if strings.Contains(err.Error(), "HASH-SECRET") || strings.Contains(err.Error(), "fast_hash") {
		t.Errorf("error carries the request URL: %v", err)
	}
}

func TestNativeRequestsNeedAToken(t *testing.T) {
	c := nativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("a request was sent without a token")
	})
	if _, err := c.NativePosition(context.Background(), "", "HASH1"); err == nil {
		t.Errorf("NativePosition without a token succeeded")
	}
	if err := c.SaveNativePosition(context.Background(), "", "HASH1", 3, "#epubcfi(/6/2!/4/2/1:0)"); err == nil {
		t.Errorf("SaveNativePosition without a token succeeded")
	}
}

func TestSaveNativePositionRejectsPercentOutsideRange(t *testing.T) {
	c := nativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("a request was sent for an invalid percentage")
	})
	for _, p := range []int{-1, 101} {
		if err := c.SaveNativePosition(context.Background(), "tok", "HASH1", p, "#epubcfi(/6/2!/4/2/1:0)"); err == nil {
			t.Errorf("percent %d was accepted", p)
		}
	}
}

func TestNativeRequestsKeepTheCallersDeadline(t *testing.T) {
	c := nativeServer(t, func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := c.NativePosition(ctx, "tok", "HASH1")
	if err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("NativePosition error = %v, want the caller's deadline", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("request took %v, want it to end at the caller's 50ms deadline", elapsed)
	}
}
