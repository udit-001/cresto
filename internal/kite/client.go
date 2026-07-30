package kite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"cresto/internal/mcp"
)

// Client is the Zerodha Kite MCP integration. It hides the session-based
// auth flow (initialize → login tool → authorize URL), session file storage,
// the MCP protocol, and holdings parsing behind a small interface.
//
// Unlike Groww (which uses OAuth 2.1 + PKCE), Kite uses a simpler model:
// initialize creates a session, the login tool returns an authorize URL,
// the user authenticates at Zerodha, and the session becomes authenticated
// server-side. The session ID is persisted to disk and reused for all
// subsequent MCP calls. No callback listener, no token exchange.
//
// Kite exposes both get_holdings (equity) and get_mf_holdings (mutual
// funds) — the latter is not available on Groww's MCP.
type Client struct {
	sessionPath string

	mcpEndpoint string
}

// Option configures a Client for testing.
type Option func(*Client)

func WithMCPEndpoint(url string) Option { return func(c *Client) { c.mcpEndpoint = url } }

const defaultMCPEndpoint = "https://mcp.kite.trade/mcp"

// New creates a Kite MCP client that stores the session ID at sessionPath.
func New(sessionPath string, opts ...Option) *Client {
	c := &Client{
		sessionPath: sessionPath,
		mcpEndpoint: defaultMCPEndpoint,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// ErrNotConnected means the session file is missing or the session was
// rejected by the MCP server. The UI should prompt the user to connect.
var ErrNotConnected = errors.New("kite: not connected (session missing or invalid)")

// sessionFile is the on-disk JSON shape for the persisted session ID.
type sessionFile struct {
	SessionID string    `json:"session_id"`
	CreatedAt time.Time `json:"created_at"`
	Expired   bool      `json:"expired,omitempty"`
}

// Connected reports whether a session file exists on disk and is not marked
// expired. Note: the session may still be server-side invalid even if this
// returns true — the expiry flag is set when a live fetch fails.
func (c *Client) Connected() bool {
	sess, err := c.loadSession()
	if err != nil {
		return false
	}
	return !sess.Expired
}

// HasExpiredSession reports whether a session file exists but was marked
// expired by a failed fetch. Mirrors Groww's HasExpiredToken so the settings
// and portfolio pages can show "session expired — reconnect" instead of
// "not connected" or a confusing Disconnect button.
func (c *Client) HasExpiredSession() bool {
	sess, err := c.loadSession()
	if err != nil {
		return false
	}
	return sess.Expired
}

// MarkExpired flags the saved session as expired without deleting it, so the
// UI can distinguish "never connected" from "session expired."
func (c *Client) MarkExpired() {
	sess, err := c.loadSession()
	if err != nil {
		return
	}
	if sess.Expired {
		return
	}
	sess.Expired = true
	_ = c.saveSession(sess)
}

// StartAuth begins the Kite session auth flow. It initializes an MCP
// session, calls the login tool to get an authorize URL, saves the session
// ID to disk, and returns the URL for the browser. Unlike Groww, there is
// no OAuth callback — the user authenticates at Zerodha and the session
// becomes authenticated server-side. After authenticating, the user
// returns to Cresto and the session ID is used for data calls.
func (c *Client) StartAuth() (string, error) {
	client := mcp.New(c.mcpEndpoint, mcp.WithProtocolVersion("2025-03-26"))

	init, err := client.Initialize(context.Background(), nil)
	if err != nil {
		return "", fmt.Errorf("kite initialize: %w", err)
	}
	if init.SessionID == "" {
		return "", fmt.Errorf("kite initialize: no session ID in response")
	}

	headers := map[string]string{"Mcp-Session-Id": init.SessionID}

	// Call the login tool to get the authorize URL.
	raw, err := client.CallTool(context.Background(), headers, "login", nil)
	if err != nil {
		return "", fmt.Errorf("kite login tool: %w", err)
	}

	authURL := extractAuthorizeURL(raw)
	if authURL == "" {
		return "", fmt.Errorf("kite login tool: no authorize URL in response: %s", raw)
	}

	// Persist the session ID so subsequent calls can use it.
	if err := c.saveSession(&sessionFile{
		SessionID: init.SessionID,
		CreatedAt: time.Now(),
	}); err != nil {
		return "", fmt.Errorf("save session: %w", err)
	}

	return authURL, nil
}

// HoldingsResult holds the parsed equity holdings plus raw MCP text.
type HoldingsResult struct {
	Holdings  []Holding
	RawText   string
	FetchedAt time.Time
}

// Holdings fetches the user's equity holdings via get_holdings.
func (c *Client) Holdings(ctx context.Context) (*HoldingsResult, error) {
	raw, err := c.callRaw(ctx, "get_holdings", nil)
	if err != nil {
		return nil, err
	}
	return parseHoldings(raw), nil
}

// MFHoldingsResult holds the parsed mutual fund holdings plus raw MCP text.
type MFHoldingsResult struct {
	Holdings  []MFHolding
	RawText   string
	FetchedAt time.Time
}

// MFHoldings fetches the user's mutual fund holdings via get_mf_holdings.
// This tool is Kite-specific — Groww's MCP does not expose MF holdings.
func (c *Client) MFHoldings(ctx context.Context) (*MFHoldingsResult, error) {
	raw, err := c.callRaw(ctx, "get_mf_holdings", nil)
	if err != nil {
		return nil, err
	}
	return parseMFHoldings(raw), nil
}

// TradesResult holds the parsed trades plus raw MCP text.
type TradesResult struct {
	Trades    []Trade
	RawText   string
	FetchedAt time.Time
}

// Trades fetches the user's trading history via get_trades. Useful for tax
// filing (realized P&L) and trade tracking. Groww's MCP does not expose
// trade history — this is Kite-specific.
func (c *Client) Trades(ctx context.Context) (*TradesResult, error) {
	raw, err := c.callRaw(ctx, "get_trades", nil)
	if err != nil {
		return nil, err
	}
	return parseTrades(raw), nil
}

// Disconnect deletes the session file. Safe to call when not connected.
func (c *Client) Disconnect() error {
	err := os.Remove(c.sessionPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove session: %w", err)
	}
	return nil
}

// callRaw loads the session, calls the named MCP tool, and returns the raw
// text response. Shared by Holdings, MFHoldings, and Trades — each public
// method parses the raw text into its own typed result. Returns
// ErrNotConnected if the session is missing or the server rejects it.
func (c *Client) callRaw(ctx context.Context, toolName string, args map[string]interface{}) (string, error) {
	sess, err := c.loadSession()
	if err != nil {
		return "", ErrNotConnected
	}

	client := mcp.New(c.mcpEndpoint, mcp.WithProtocolVersion("2025-03-26"))
	headers := map[string]string{"Mcp-Session-Id": sess.SessionID}

	raw, err := client.CallTool(ctx, headers, toolName, args)
	if err != nil {
		if errors.Is(err, mcp.ErrUnauthorized) {
			c.MarkExpired()
			return "", ErrNotConnected
		}
		if isSessionExpired(err.Error()) {
			c.MarkExpired()
			return "", ErrNotConnected
		}
		return "", fmt.Errorf("kite tools/call %q: %w", toolName, err)
	}

	if isSessionExpired(raw) {
		c.MarkExpired()
		return "", ErrNotConnected
	}

	return raw, nil
}

// --- session file I/O ---

func (c *Client) loadSession() (*sessionFile, error) {
	data, err := os.ReadFile(c.sessionPath)
	if err != nil {
		return nil, err
	}
	var sess sessionFile
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, fmt.Errorf("parse session: %w", err)
	}
	if sess.SessionID == "" {
		return nil, fmt.Errorf("empty session ID in file")
	}
	return &sess, nil
}

func (c *Client) saveSession(sess *sessionFile) error {
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}
	if err := os.WriteFile(c.sessionPath, data, 0600); err != nil {
		return fmt.Errorf("write session: %w", err)
	}
	return nil
}

// --- helpers ---

// authorizeURLRe matches the Zerodha authorize URL embedded in the login
// tool's text response.
var authorizeURLRe = regexp.MustCompile(`https://mcp\.kite\.trade/authorize[^\s)\]>"']+`)

func extractAuthorizeURL(text string) string {
	m := authorizeURLRe.FindString(text)
	return m
}

// isSessionExpired checks whether the raw text indicates the session is not
// authenticated or has expired. Kite returns these as tool errors, not HTTP
// errors.
func isSessionExpired(raw string) bool {
	if raw == "" {
		return false
	}
	lower := strings.ToLower(raw)
	return strings.Contains(lower, "please log in first") ||
		strings.Contains(lower, "invalid session id")
}

// SaveSessionForTest writes a session file so Connected returns true.
// Test-only.
func (c *Client) SaveSessionForTest() error {
	return c.saveSession(&sessionFile{
		SessionID: "test-session-id",
		CreatedAt: time.Now(),
	})
}
