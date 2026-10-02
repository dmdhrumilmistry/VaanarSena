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

// NewServiceAccount authenticates as the service account in credsFile.
func NewServiceAccount(ctx context.Context, credsFile, base string, scopes ...string) (*Client, error) {
	data, err := os.ReadFile(credsFile)
	if err != nil {
		return nil, err
	}
	return ServiceAccountFromJSON(ctx, data, base, scopes...)
}

// ServiceAccountFromJSON authenticates as the service account itself. Only
// service account keys are accepted (JWTConfigFromJSON rejects other
// credential types, such as external account configurations).
func ServiceAccountFromJSON(ctx context.Context, data []byte, base string, scopes ...string) (*Client, error) {
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
	return DelegatedFromJSON(ctx, data, subject, base, scopes...)
}

// DelegatedFromJSON is NewDelegated with the key already in memory.
func DelegatedFromJSON(ctx context.Context, data []byte, subject, base string, scopes ...string) (*Client, error) {
	cfg, err := googleoauth.JWTConfigFromJSON(data, scopes...)
	if err != nil {
		return nil, err
	}
	cfg.Subject = subject
	return &Client{HTTP: withTimeout(cfg.Client(ctx)), Base: base}, nil
}

// ServiceAccountInfo is the non-secret identity in a service account key.
type ServiceAccountInfo struct {
	Type        string `json:"type"`
	ProjectID   string `json:"project_id"`
	ClientEmail string `json:"client_email"`
}

// ParseServiceAccount validates a service account key and returns its identity.
func ParseServiceAccount(data []byte) (*ServiceAccountInfo, error) {
	var info ServiceAccountInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("not a JSON key file: %w", err)
	}
	if info.Type != "service_account" {
		return nil, fmt.Errorf("key type is %q; create a service account key (type service_account)", info.Type)
	}
	if _, err := googleoauth.JWTConfigFromJSON(data); err != nil {
		return nil, fmt.Errorf("invalid service account key: %w", err)
	}
	return &info, nil
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
