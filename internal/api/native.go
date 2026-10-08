package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// nativeBaseURL is the host and path of the PocketBook Cloud reader API, the
// one the native web reader uses.
const nativeBaseURL = "https://cloud.pocketbook.digital/api/v1.0/"

// defaultNativeTimeout bounds one native request, including reading its body,
// when the caller's context has no deadline. A caller's deadline is kept as given.
const defaultNativeTimeout = 30 * time.Second

// maxNativeBody bounds a native response. The endpoints answer in well under a
// kilobyte, so anything larger is refused.
const maxNativeBody = 1 << 20

// NativePosition is the reading position that the PocketBook Cloud account holds
// for one book. The zero value means the account has no position.
type NativePosition struct {
	Pointer   string // EPUB CFI with a leading "#", or empty
	PointerPb string // the same CFI as Pointer, as the native reader sends it
	Percent   int
	Updated   time.Time // zero when the server gave no readable time
}

// HTTPError is a refusal by a native endpoint. It names the operation and the
// status. It never includes the response body, tokens, or the request URL.
type HTTPError struct {
	Op     string
	Status int
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("%s: HTTP %d %s", e.Op, e.Status, http.StatusText(e.Status))
}

// errRedirectRefused is returned for every redirect. The bearer token is only
// ever sent to the endpoint the client was given.
var errRedirectRefused = errors.New("redirect refused")

func refuseRedirect(*http.Request, []*http.Request) error {
	return errRedirectRefused
}

// nativeTransport sends authenticated requests to the native reader API.
type nativeTransport struct {
	base   string
	client *http.Client
}

// newNativeTransport returns a transport for base. A nil hc uses a client with
// no timeout of its own, so each request is bounded by its context. An injected
// client keeps its own timeout. Either way the client refuses redirects, so the
// bearer token cannot follow a redirect to another host.
func newNativeTransport(base string, hc *http.Client) nativeTransport {
	if hc == nil {
		hc = &http.Client{}
	}
	copied := *hc
	copied.CheckRedirect = refuseRedirect
	return nativeTransport{base: base, client: &copied}
}

// endpoint returns the URL of a native path with fast_hash set in the query.
func (t nativeTransport) endpoint(fastHash string, segments ...string) (string, error) {
	u, err := url.Parse(t.base)
	if err != nil {
		return "", fmt.Errorf("native base URL: %w", err)
	}
	u = u.JoinPath(segments...)
	q := u.Query()
	q.Set("fast_hash", fastHash)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// send performs one request and returns its body. Any status other than 200 is
// an *HTTPError. A nil body sends no request body. A context with a deadline
// keeps it; one without is bounded by defaultNativeTimeout.
func (t nativeTransport) send(ctx context.Context, op, method, endpoint, token string, body []byte) ([]byte, error) {
	if token == "" {
		return nil, fmt.Errorf("%s: not signed in", op)
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultNativeTimeout)
		defer cancel()
	}

	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, rd)
	if err != nil {
		return nil, fmt.Errorf("%s: build request", op)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json, text/plain")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, requestError(op, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxNativeBody+1))
	if err != nil {
		return nil, fmt.Errorf("%s: read response", op)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &HTTPError{Op: op, Status: resp.StatusCode}
	}
	if len(data) > maxNativeBody {
		return nil, fmt.Errorf("%s: response too large", op)
	}
	return data, nil
}

// requestError describes a failed request without its URL, which carries the
// file hash. The url.Error wrapper is removed; the cause is kept.
func requestError(op string, err error) error {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		err = uerr.Err
	}
	return fmt.Errorf("%s: request failed: %w", op, err)
}

// NativePosition reads the account's reading position for the book with the
// given file hash. A book with no position returns the zero NativePosition.
// The response is decoded only as far as the position is needed.
func (c *Client) NativePosition(ctx context.Context, token, fastHash string) (NativePosition, error) {
	const op = "read position"
	endpoint, err := c.native.endpoint(fastHash, "reader", "documentInformation")
	if err != nil {
		return NativePosition{}, fmt.Errorf("%s: %w", op, err)
	}
	data, err := c.native.send(ctx, op, http.MethodGet, endpoint, token, nil)
	if err != nil {
		return NativePosition{}, err
	}

	var doc struct {
		Position *struct {
			Pointer   string          `json:"pointer"`
			PointerPb string          `json:"pointer_pb"`
			Percent   int             `json:"percent"`
			Updated   json.RawMessage `json:"updated"`
		} `json:"position"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return NativePosition{}, fmt.Errorf("%s: unexpected response", op)
	}
	if doc.Position == nil {
		return NativePosition{}, nil
	}
	p := doc.Position
	return NativePosition{
		Pointer:   p.Pointer,
		PointerPb: p.PointerPb,
		Percent:   p.Percent,
		Updated:   parseNativeTime(p.Updated),
	}, nil
}

// SaveNativePosition writes the account's reading position for a book. The
// endpoint answers with the plain text "OK", and only its status is checked. A
// success is not proof that the position was kept, so callers read it back with
// NativePosition before reporting it as synced.
func (c *Client) SaveNativePosition(ctx context.Context, token, fastHash string, percent int, pointer string) error {
	const op = "save position"
	if percent < 0 || percent > 100 {
		return fmt.Errorf("%s: percent %d is outside 0..100", op, percent)
	}
	endpoint, err := c.native.endpoint(fastHash, "books", "read-position")
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	body, err := json.Marshal(struct {
		Offs      int    `json:"offs"`
		Pointer   string `json:"pointer"`
		PointerPb string `json:"pointer_pb"`
	}{Offs: percent, Pointer: pointer, PointerPb: pointer})
	if err != nil {
		return fmt.Errorf("%s: encode request", op)
	}
	_, err = c.native.send(ctx, op, http.MethodPost, endpoint, token, body)
	return err
}

// parseNativeTime reads a timestamp from a native response. An unreadable or
// missing time is the zero time, not an error, because the position itself is
// still usable.
func parseNativeTime(raw json.RawMessage) time.Time {
	var s string
	if json.Unmarshal(raw, &s) != nil || s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
