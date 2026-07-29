package groww

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"cresto/internal/mcp"
)

// Client is the Groww MCP integration. It hides OAuth 2.1 + PKCE, the
// transient localhost:52155 callback listener, token file storage, the
// MCP Streamable HTTP protocol, and holdings parsing behind four methods.
//
// Groww's DCR endpoint ignores the registration body and returns a shared
// production client with a hardcoded redirect-URI allowlist. The only
// localhost redirect Groww permits is http://localhost:52155/oauth/callback,
// so StartAuth runs a transient HTTP listener on that port for the ~30s
// the user spends authenticating on groww.in. Cresto itself can run on any
// port — the listener is internal to the auth flow.
//
// Tokens expire daily at 6 AM IST with no refresh token (Groww's design).
// Re-connect by calling StartAuth again.
type Client struct {
	tokenPath string

	mcpEndpoint  string
	authorizeURL string
	tokenURL     string
	registerURL  string
	callbackPort int
}

// Option configures a Client for testing (point endpoints at a test server).
type Option func(*Client)

func WithMCPEndpoint(url string) Option    { return func(c *Client) { c.mcpEndpoint = url } }
func WithAuthorizeURL(url string) Option   { return func(c *Client) { c.authorizeURL = url } }
func WithTokenURL(url string) Option       { return func(c *Client) { c.tokenURL = url } }
func WithRegisterURL(url string) Option    { return func(c *Client) { c.registerURL = url } }
func WithCallbackPort(port int) Option     { return func(c *Client) { c.callbackPort = port } }

// Default endpoint constants — discovered by probing the live Groww MCP.
const (
	defaultMCPEndpoint  = "https://mcp.groww.in/mcp/"
	defaultAuthorizeURL = "https://groww.in/oauth/authorize"
	defaultTokenURL     = "https://api.groww.in/oauth2/v1/token"
	defaultRegisterURL  = "https://api.groww.in/oauth2/v1/register"
	defaultCallbackPort = 52155
)

// New creates a Groww MCP client that stores tokens at tokenPath.
func New(tokenPath string, opts ...Option) *Client {
	c := &Client{
		tokenPath:    tokenPath,
		mcpEndpoint:  defaultMCPEndpoint,
		authorizeURL: defaultAuthorizeURL,
		tokenURL:     defaultTokenURL,
		registerURL:  defaultRegisterURL,
		callbackPort: defaultCallbackPort,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// ErrNotConnected means the token file is missing, expired, or was rejected
// by the MCP server. The UI should prompt the user to reconnect.
var ErrNotConnected = errors.New("groww: not connected (token missing or expired)")

// tokenFile is the on-disk JSON shape for the persisted access token.
type tokenFile struct {
	AccessToken string    `json:"access_token"`
	TokenType   string    `json:"token_type"`
	ExpiresAt   time.Time `json:"expires_at"`
	ObtainedAt  time.Time `json:"obtained_at"`
}

// Connected reports whether a valid (non-expired) token exists on disk.
// The MCP server may still reject the token — Holdings handles that by
// returning ErrNotConnected on a 401.
func (c *Client) Connected() bool {
	tok, err := c.loadToken()
	if err != nil {
		return false
	}
	return time.Now().Before(tok.ExpiresAt)
}

// HasExpiredToken reports whether a token file exists on disk but is past
// its expiry. Used by the UI to distinguish "never connected" from "your
// session expired — reconnect." Returns false when no token file exists.
func (c *Client) HasExpiredToken() bool {
	tok, err := c.loadToken()
	if err != nil {
		return false
	}
	return time.Now().After(tok.ExpiresAt)
}

// StartAuth begins the OAuth flow. It registers via DCR (fetching the shared
// client credentials), generates PKCE, starts a transient HTTP listener on
// localhost:52155 to receive the callback, and returns the Groww authorize
// URL for the browser. The transient listener exchanges the code for a
// token, persists it, and redirects the browser to returnURL.
//
// The listener auto-shuts-down after handling one callback or after 5 minutes.
// If port 52155 is already in use (another MCP client, mcp-remote, etc.),
// StartAuth returns an error — the user must free the port first.
func (c *Client) StartAuth(returnURL string) (string, error) {
	return c.startOAuth(returnURL)
}

// HoldingsResult holds the parsed holdings plus the raw MCP text for fallback.
type HoldingsResult struct {
	Holdings  []Holding
	RawText   string
	FetchedAt time.Time
}

// Holdings fetches the user's stock holdings via the Groww MCP. It loads the
// persisted token, initialises the MCP session, discovers the holdings tool
// via tools/list, and calls it. Returns ErrNotConnected if the token is
// missing, expired, or rejected.
func (c *Client) Holdings(ctx context.Context) (*HoldingsResult, error) {
	tok, err := c.loadToken()
	if err != nil || time.Now().After(tok.ExpiresAt) {
		return nil, ErrNotConnected
	}

	client := mcp.New(c.mcpEndpoint)

	init, err := client.Initialize(ctx, map[string]string{"Authorization": "Bearer " + tok.AccessToken})
	if err != nil {
		if errors.Is(err, mcp.ErrUnauthorized) {
			return nil, ErrNotConnected
		}
		return nil, fmt.Errorf("groww mcp initialize: %w", err)
	}

	headers := map[string]string{"Authorization": "Bearer " + tok.AccessToken}
	if init.SessionID != "" {
		headers["Mcp-Session-Id"] = init.SessionID
	}

	tools, err := client.ListTools(ctx, headers)
	if err != nil {
		if errors.Is(err, mcp.ErrUnauthorized) {
			return nil, ErrNotConnected
		}
		return nil, fmt.Errorf("groww mcp tools/list: %w", err)
	}

	toolName := mcp.FindTool(tools, "holding")
	if toolName == "" {
		names := make([]string, len(tools))
		for i, t := range tools {
			names[i] = t.Name
		}
		return nil, fmt.Errorf("groww mcp: no holdings tool found (available: %v)", names)
	}

	raw, err := client.CallTool(ctx, headers, toolName, nil)
	if err != nil {
		if errors.Is(err, mcp.ErrUnauthorized) {
			return nil, ErrNotConnected
		}
		return nil, fmt.Errorf("groww mcp tools/call %q: %w", toolName, err)
	}

	return parseHoldings(raw), nil
}

// Disconnect deletes the token file. Safe to call when not connected.
func (c *Client) Disconnect() error {
	err := os.Remove(c.tokenPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove token: %w", err)
	}
	return nil
}

// loadToken reads and parses the token file. Returns an error if the file
// is missing or unparseable.
func (c *Client) loadToken() (*tokenFile, error) {
	data, err := os.ReadFile(c.tokenPath)
	if err != nil {
		return nil, err
	}
	var tok tokenFile
	if err := json.Unmarshal(data, &tok); err != nil {
		return nil, fmt.Errorf("parse token: %w", err)
	}
	return &tok, nil
}

// saveToken writes the token file with restrictive permissions.
func (c *Client) saveToken(tok *tokenFile) error {
	data, err := json.MarshalIndent(tok, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal token: %w", err)
	}
	if err := os.WriteFile(c.tokenPath, data, 0600); err != nil {
		return fmt.Errorf("write token: %w", err)
	}
	return nil
}

// SaveExpiredTokenForTest writes an expired token to disk so HasExpiredToken
// returns true. Test-only — used by web tests to exercise the session-expired
// surface state without a real OAuth flow.
func (c *Client) SaveExpiredTokenForTest() error {
	return c.saveToken(&tokenFile{
		AccessToken: "expired-test-token",
		ExpiresAt:   time.Now().Add(-1 * time.Hour),
		ObtainedAt:  time.Now().Add(-2 * time.Hour),
	})
}
