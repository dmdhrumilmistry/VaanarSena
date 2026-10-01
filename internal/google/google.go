// Package google holds a small authenticated JSON client shared by the
// Android Management API and Admin SDK drivers.
package google

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"golang.org/x/oauth2"
	googleoauth "golang.org/x/oauth2/google"
)

// Client calls a Google REST API with service account credentials.
type Client struct {
	HTTP *http.Client
	Base string
}

// NewServiceAccount authenticates as the service account itself. Only
// service account keys are accepted (JWTConfigFromJSON rejects other
// credential types, such as external account configurations).
func NewServiceAccount(ctx context.Context, credsFile, base string, scopes ...string) (*Client, error) {
	data, err := os.ReadFile(credsFile)
	if err != nil {
		return nil, err
	}
	cfg, err := googleoauth.JWTConfigFromJSON(data, scopes...)
	if err != nil {
		return nil, err
	}
	return &Client{HTTP: withTimeout(oauth2.NewClient(ctx, cfg.TokenSource(ctx))), Base: base}, nil
}

// NewDelegated impersonates a Workspace user via domain-wide delegation.
func NewDelegated(ctx context.Context, credsFile, subject, base string, scopes ...string) (*Client, error) {
	data, err := os.ReadFile(credsFile)
	if err != nil {
		return nil, err
	}
	cfg, err := googleoauth.JWTConfigFromJSON(data, scopes...)
	if err != nil {
		return nil, err
	}
	cfg.Subject = subject
	return &Client{HTTP: withTimeout(cfg.Client(ctx)), Base: base}, nil
}

func withTimeout(c *http.Client) *http.Client {
	c.Timeout = 30 * time.Second
	return c
}

// APIError is a non-2xx response.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string { return fmt.Sprintf("google API %d: %s", e.Status, e.Body) }

// Do sends a JSON request and decodes a JSON response into out (if non-nil).
func (c *Client) Do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		if len(raw) > 2048 {
			raw = raw[:2048]
		}
		return &APIError{Status: resp.StatusCode, Body: string(bytes.TrimSpace(raw))}
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}
