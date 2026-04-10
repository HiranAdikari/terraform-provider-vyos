// Package vyos provides a pure-Go client for the VyOS HTTPS REST API.
// It is independent of any Terraform SDK and can be used as a standalone library.
package vyos

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const defaultHTTPTimeout = 30 * time.Second

// Client is the VyOS REST API client. It is safe for concurrent use.
type Client struct {
	httpClient *http.Client
	endpoint   string // e.g. https://172.22.100.50
	apiKey     string
}

// apiResponse is the common envelope returned by all VyOS API endpoints.
type apiResponse struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   *string         `json:"error"`
}

// NewClient returns a ready-to-use VyOS API client.
// Set insecure=true when VyOS uses a self-signed TLS certificate.
func NewClient(endpoint, apiKey string, insecure bool) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
	}
	return &Client{
		httpClient: &http.Client{
			Timeout:   defaultHTTPTimeout,
			Transport: transport,
		},
		endpoint: endpoint,
		apiKey:   apiKey,
	}
}

// Configure sends a single set/delete/comment operation to /configure.
// value may be empty for nodes that take no value (e.g. boolean presence nodes).
func (c *Client) Configure(ctx context.Context, op string, path []string, value string) error {
	body := map[string]any{
		"key":  c.apiKey,
		"op":   op,
		"path": path,
	}
	if value != "" {
		body["value"] = value
	}
	return c.doVoid(ctx, "/configure", body)
}

// ConfigureSection sends a set/load operation to /configure-section.
// section is a map representing the config sub-tree to write under path.
// Use op="set" to merge/update, op="load" to replace.
func (c *Client) ConfigureSection(ctx context.Context, op string, path []string, section map[string]any) error {
	body := map[string]any{
		"key":     c.apiKey,
		"op":      op,
		"path":    path,
		"section": section,
	}
	return c.doVoid(ctx, "/configure-section", body)
}

// ShowConfig retrieves the config sub-tree at path.
// Returns (nil, false, nil) when the path exists but is empty, or when VyOS
// reports the path does not exist (treated as not-found, not an error).
func (c *Client) ShowConfig(ctx context.Context, path []string) (map[string]any, bool, error) {
	body := map[string]any{
		"key":          c.apiKey,
		"op":           "showConfig",
		"path":         path,
		"configFormat": "json",
	}

	raw, err := c.do(ctx, "/retrieve", body)
	if err != nil {
		// VyOS returns HTTP 400 when the config path is empty / doesn't exist.
		// Treat that as "not found" rather than a hard error.
		if isNotFound(err) {
			return nil, false, nil
		}
		return nil, false, err
	}

	// data can be null, a JSON object, or an empty string.
	if raw == nil || string(raw) == "null" || string(raw) == `""` {
		return nil, false, nil
	}

	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		// data might be a bare string (e.g. single-value node); treat as not-a-tree.
		return nil, true, nil
	}
	return result, true, nil
}

// Exists checks whether a config path is present.
func (c *Client) Exists(ctx context.Context, path []string) (bool, error) {
	body := map[string]any{
		"key":  c.apiKey,
		"op":   "exists",
		"path": path,
	}

	raw, err := c.do(ctx, "/retrieve", body)
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, err
	}
	if raw == nil {
		return false, nil
	}
	var exists bool
	if err := json.Unmarshal(raw, &exists); err != nil {
		return false, fmt.Errorf("decode exists response: %w", err)
	}
	return exists, nil
}

// SaveConfig persists the running config to /config/config.boot.
func (c *Client) SaveConfig(ctx context.Context) error {
	body := map[string]any{
		"key": c.apiKey,
		"op":  "save",
	}
	return c.doVoid(ctx, "/config-file", body)
}

// ─── internal helpers ─────────────────────────────────────────────────────────

// notFoundError wraps a VyOS "path not found / empty" API error so callers can
// distinguish it from unexpected failures.
type notFoundError struct{ msg string }

func (e *notFoundError) Error() string { return e.msg }

func isNotFound(err error) bool {
	_, ok := err.(*notFoundError)
	return ok
}

// do performs a POST to endpoint+path and returns the raw data payload.
func (c *Client) do(ctx context.Context, apiPath string, body map[string]any) (json.RawMessage, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+apiPath, bytes.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	var apiResp apiResponse
	if err := json.Unmarshal(respBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("decode API response (HTTP %d): %w", resp.StatusCode, err)
	}

	if !apiResp.Success {
		msg := "unknown error"
		if apiResp.Error != nil {
			msg = *apiResp.Error
		}
		// VyOS uses HTTP 400 for "path empty / not found" conditions.
		if resp.StatusCode == http.StatusBadRequest && looksLikeNotFound(msg) {
			return nil, &notFoundError{msg: msg}
		}
		return nil, fmt.Errorf("VyOS API error (HTTP %d): %s", resp.StatusCode, msg)
	}

	return apiResp.Data, nil
}

// doVoid calls do and discards the data payload.
func (c *Client) doVoid(ctx context.Context, apiPath string, body map[string]any) error {
	_, err := c.do(ctx, apiPath, body)
	return err
}

// looksLikeNotFound returns true when the VyOS error message indicates that the
// requested config path is empty or does not exist.
func looksLikeNotFound(msg string) bool {
	notFoundPhrases := []string{
		"Configuration under specified path is empty",
		"specified path is empty",
		"path is empty",
		"does not exist",
		"No such file or directory",
	}
	for _, phrase := range notFoundPhrases {
		if containsFold(msg, phrase) {
			return true
		}
	}
	return false
}

func containsFold(s, substr string) bool {
	sLow := toLower(s)
	subLow := toLower(substr)
	return contains(sLow, subLow)
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := range s {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

func contains(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	if len(substr) > len(s) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
