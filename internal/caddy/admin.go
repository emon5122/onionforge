package caddy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// Admin is a client for Caddy's admin API on a unix socket.
type Admin struct {
	Socket string
}

func (a Admin) client() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", a.Socket)
			},
		},
	}
}

func (a Admin) do(ctx context.Context, method, path, contentType string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, "http://localhost"+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := a.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return out, fmt.Errorf("caddy admin %s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// Load replaces Caddy's running configuration with a Caddyfile. Caddy
// applies it gracefully and keeps the old config if the new one fails.
func (a Admin) Load(ctx context.Context, caddyfile string) error {
	_, err := a.do(ctx, http.MethodPost, "/load", "text/caddyfile", []byte(caddyfile))
	return err
}

// Config returns the active JSON configuration.
func (a Admin) Config(ctx context.Context) ([]byte, error) {
	return a.do(ctx, http.MethodGet, "/config/", "", nil)
}
