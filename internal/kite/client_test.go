package kite

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConnected_NoSessionFile(t *testing.T) {
	c := New(filepath.Join(t.TempDir(), "missing.json"))
	if c.Connected() {
		t.Errorf("Connected() = true with no session file, want false")
	}
}

func TestConnected_WithSession(t *testing.T) {
	dir := t.TempDir()
	c := New(filepath.Join(dir, "session.json"))
	if err := c.SaveSessionForTest(); err != nil {
		t.Fatalf("SaveSessionForTest: %v", err)
	}
	if !c.Connected() {
		t.Errorf("Connected() = false with session file, want true")
	}
}

func TestDisconnect_DeletesSession(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
	c := New(path)
	if err := c.SaveSessionForTest(); err != nil {
		t.Fatalf("SaveSessionForTest: %v", err)
	}
	if err := c.Disconnect(); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("session file should be deleted after Disconnect")
	}
}

func TestDisconnect_NoSessionIsNoop(t *testing.T) {
	c := New(filepath.Join(t.TempDir(), "never_existed.json"))
	if err := c.Disconnect(); err != nil {
		t.Errorf("Disconnect with no session should not error: %v", err)
	}
}

func TestExtractAuthorizeURL(t *testing.T) {
	text := `IMPORTANT: Please display this warning to the user before proceeding:

⚠️ **WARNING: AI systems are unpredictable and non-deterministic.**

After showing the warning above, provide the user with this login link: [Login to Kite](https://mcp.kite.trade/authorize?session_id=kitemcp-abc123%7C1785322453.token)

After completing the login in your browser, let me know and I'll continue with your request.`

	url := extractAuthorizeURL(text)
	if url == "" {
		t.Fatalf("extractAuthorizeURL returned empty string")
	}
	if url != "https://mcp.kite.trade/authorize?session_id=kitemcp-abc123%7C1785322453.token" {
		t.Errorf("extractAuthorizeURL = %q, want the full authorize URL", url)
	}
}

func TestExtractAuthorizeURL_NotFound(t *testing.T) {
	if extractAuthorizeURL("no URL here") != "" {
		t.Errorf("extractAuthorizeURL should return empty string when no URL found")
	}
}

func TestIsLoginRequired(t *testing.T) {
	if !isLoginRequired("Please log in first using the login tool") {
		t.Errorf("isLoginRequired should return true for 'Please log in first'")
	}
	if isLoginRequired("here are your holdings") {
		t.Errorf("isLoginRequired should return false for normal text")
	}
	if isLoginRequired("") {
		t.Errorf("isLoginRequired should return false for empty string")
	}
}

func TestParseHoldings_Empty(t *testing.T) {
	result := parseHoldings("")
	if len(result.Holdings) != 0 {
		t.Errorf("empty input should yield 0 holdings")
	}
}

func TestParseHoldings_NonJSONFallback(t *testing.T) {
	raw := "Your holdings:\n1. RELIANCE - 10 shares"
	result := parseHoldings(raw)
	if len(result.Holdings) != 0 {
		t.Errorf("non-JSON should yield 0 parsed holdings")
	}
	if result.RawText != raw {
		t.Errorf("RawText should be preserved")
	}
}

func TestParseHoldings_Array(t *testing.T) {
	raw := `[{"tradingsymbol":"RELIANCE","quantity":10,"average_price":2500,"pnl":1000}]`
	result := parseHoldings(raw)
	if len(result.Holdings) != 1 {
		t.Fatalf("got %d holdings, want 1", len(result.Holdings))
	}
	if result.Holdings[0].TradingSymbol != "RELIANCE" {
		t.Errorf("symbol = %q, want RELIANCE", result.Holdings[0].TradingSymbol)
	}
}

func TestParseMFHoldings_Empty(t *testing.T) {
	result := parseMFHoldings("")
	if len(result.Holdings) != 0 {
		t.Errorf("empty input should yield 0 MF holdings")
	}
}

func TestParseMFHoldings_Array(t *testing.T) {
	raw := `[{"scheme_name":"SBI Bluechip Fund","folio":"12345","quantity":100,"pnl":5000}]`
	result := parseMFHoldings(raw)
	if len(result.Holdings) != 1 {
		t.Fatalf("got %d MF holdings, want 1", len(result.Holdings))
	}
	if result.Holdings[0].SchemeName != "SBI Bluechip Fund" {
		t.Errorf("scheme = %q, want SBI Bluechip Fund", result.Holdings[0].SchemeName)
	}
}

func TestParseTrades_Empty(t *testing.T) {
	result := parseTrades("")
	if len(result.Trades) != 0 {
		t.Errorf("empty input should yield 0 trades")
	}
}

func TestParseTrades_Array(t *testing.T) {
	raw := `[{"trade_id":"T1","order_id":"O1","tradingsymbol":"RELIANCE","transaction_type":"BUY","quantity":10,"price":2500,"trade_value":25000,"fill_timestamp":"2026-07-29 10:15:30","product":"CNC"}]`
	result := parseTrades(raw)
	if len(result.Trades) != 1 {
		t.Fatalf("got %d trades, want 1", len(result.Trades))
	}
	tr := result.Trades[0]
	if tr.TradingSymbol != "RELIANCE" {
		t.Errorf("symbol = %q, want RELIANCE", tr.TradingSymbol)
	}
	if tr.TransactionType != "BUY" {
		t.Errorf("type = %q, want BUY", tr.TransactionType)
	}
	if tr.Price != 2500 {
		t.Errorf("price = %v, want 2500", tr.Price)
	}
}

func TestParseTrades_NonJSONFallback(t *testing.T) {
	result := parseTrades("No trades today")
	if len(result.Trades) != 0 {
		t.Errorf("non-JSON should yield 0 trades")
	}
	if result.RawText != "No trades today" {
		t.Errorf("RawText should be preserved")
	}
}

func TestHoldingDisplayName(t *testing.T) {
	h := Holding{TradingSymbol: "TCS"}
	if h.DisplayName() != "TCS" {
		t.Errorf("DisplayName = %q, want TCS", h.DisplayName())
	}
}

func TestSaveAndLoadSession(t *testing.T) {
	dir := t.TempDir()
	c := New(filepath.Join(dir, "session.json"))

	sess := &sessionFile{
		SessionID: "test-session-123",
		CreatedAt: time.Now(),
	}
	if err := c.saveSession(sess); err != nil {
		t.Fatalf("saveSession: %v", err)
	}

	loaded, err := c.loadSession()
	if err != nil {
		t.Fatalf("loadSession: %v", err)
	}
	if loaded.SessionID != "test-session-123" {
		t.Errorf("loaded SessionID = %q, want test-session-123", loaded.SessionID)
	}
}
