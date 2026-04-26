package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	pbc "github.com/micronull/pocketbook-cloud-client"
)

const (
	DefaultClientID     = "qNAx1RDb"
	DefaultClientSecret = "K3YYSjCgDJNoWKdGVOyO1mrROp3MMZqqRNXNXTmh"
)

type Client struct {
	inner *pbc.Client
}

func New() *Client {
	return &Client{
		inner: pbc.New(
			pbc.WithClientID(DefaultClientID),
			pbc.WithClientSecret(DefaultClientSecret),
		),
	}
}

func (c *Client) Providers(ctx context.Context, username string) ([]pbc.Provider, error) {
	return c.inner.Providers(ctx, username)
}

func (c *Client) Login(ctx context.Context, req pbc.LoginRequest) (pbc.Token, error) {
	return c.inner.Login(ctx, req)
}

func (c *Client) Books(ctx context.Context, token string, limit, offset int) (pbc.Books, error) {
	return c.inner.Books(ctx, token, limit, offset)
}

func (c *Client) DownloadBook(ctx context.Context, url, destPath string, onProgress func(downloaded, total int64)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: status %d", resp.StatusCode)
	}

	dir := filepath.Dir(destPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer out.Close()

	total := resp.ContentLength
	var downloaded int64

	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			_, werr := out.Write(buf[:n])
			if werr != nil {
				return fmt.Errorf("write file: %w", werr)
			}
			downloaded += int64(n)
			if onProgress != nil {
				onProgress(downloaded, total)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read body: %w", err)
		}
	}

	return nil
}
