package client

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/merlindeep/claude-cost-viewer/internal/usage"
)

// Codex request parameters. Like the Claude endpoint this one is undocumented:
// it is what the "Usage" page in the ChatGPT web app and the Codex CLI read.
//
//   - Method/URL: GET https://chatgpt.com/backend-api/wham/usage
//   - Authorization: Bearer <oauth-token>   (from ~/.codex/auth.json)
//   - chatgpt-account-id: <account-id>       (from the same file)
//
// Unlike Anthropic's endpoint it does not reject a plain User-Agent, but one is
// sent for good citizenship. The response carries the rolling and weekly
// rate-limit windows plus the plan type.
const (
	// CodexBaseURL is the Codex usage endpoint.
	CodexBaseURL = "https://chatgpt.com/backend-api/wham/usage"
	// CodexUserAgent identifies the client to the endpoint.
	CodexUserAgent = "ccview (+https://github.com/merlindeep/claude-cost-viewer)"
	// CodexAccountHeader carries the ChatGPT account id.
	CodexAccountHeader = "chatgpt-account-id"
)

// CodexClient performs Codex usage requests.
type CodexClient struct {
	HTTP      *http.Client
	BaseURL   string
	UserAgent string
}

// CodexOption customizes a [CodexClient].
type CodexOption func(*CodexClient)

// WithCodexHTTPClient overrides the underlying *http.Client (useful in tests).
func WithCodexHTTPClient(h *http.Client) CodexOption {
	return func(c *CodexClient) { c.HTTP = h }
}

// WithCodexBaseURL overrides the endpoint URL (useful in tests).
func WithCodexBaseURL(u string) CodexOption {
	return func(c *CodexClient) { c.BaseURL = u }
}

// NewCodex returns a CodexClient wired to the real endpoint.
func NewCodex(opts ...CodexOption) *CodexClient {
	c := &CodexClient{
		HTTP:      &http.Client{Timeout: DefaultTimeout},
		BaseURL:   CodexBaseURL,
		UserAgent: CodexUserAgent,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Fetch requests the current Codex usage snapshot. accountID may be empty, in
// which case the account header is omitted. On success it returns the decoded
// payload and the raw body; on a non-200 response it returns the raw body with
// an [*APIError]. The raw body is always returned when available so callers can
// surface it in debug output.
func (c *CodexClient) Fetch(ctx context.Context, token, accountID string) (*usage.Codex, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if accountID != "" {
		req.Header.Set(CodexAccountHeader, accountID)
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("request codex usage endpoint: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, body, &APIError{Status: resp.StatusCode, Body: Snippet(body)}
	}

	u, err := usage.ParseCodex(body)
	if err != nil {
		return nil, body, err
	}
	return u, body, nil
}
