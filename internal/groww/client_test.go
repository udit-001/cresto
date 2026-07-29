package groww

import (
	"encoding/json"
	"cresto/internal/mcp"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseHoldings_GrowwShape(t *testing.T) {
	raw := `{
		"result_timestamp": "2026-07-29 Wednesday 04:07:43 PM IST",
		"result": {
			"holdings": [
				{
					"average_price": 165.23,
					"trading_symbol": "IREDA",
					"quantity": 22.0,
					"invested_value": "3.64 Thousands",
					"title": "Indian Renewable Energy Development Agency Ltd.",
					"current_value": "2.66 Thousands",
					"p&l": "-977.02",
					"p&l_percent": "-26.88%",
					"symbol_isin": "INE202E01016",
					"tradable_exchanges": ["NSE", "BSE"]
				},
				{
					"average_price": 332.27,
					"trading_symbol": "JIOFIN",
					"quantity": 150.0,
					"invested_value": "49.84 Thousands",
					"title": "JIO Financial Services Ltd.",
					"current_value": "37.42 Thousands",
					"p&l": "-12.42 Thousands",
					"p&l_percent": "-24.92%",
					"symbol_isin": "INE758E01017",
					"tradable_exchanges": ["NSE", "BSE"]
				}
			],
			"total_invested_value": "74.19 Thousands",
			"total_current_value": "62.29 Thousands"
		}
	}`
	result := parseHoldings(raw)
	if len(result.Holdings) != 2 {
		t.Fatalf("got %d holdings, want 2", len(result.Holdings))
	}

	h := result.Holdings[0]
	if h.TradingSymbol != "IREDA" {
		t.Errorf("first holding symbol = %q, want IREDA", h.TradingSymbol)
	}
	if h.Title != "Indian Renewable Energy Development Agency Ltd." {
		t.Errorf("first holding title = %q", h.Title)
	}
	if h.Quantity != 22 {
		t.Errorf("quantity = %v, want 22", h.Quantity)
	}
	if h.AvgPrice != 165.23 {
		t.Errorf("avg_price = %v, want 165.23", h.AvgPrice)
	}
	if h.InvestedValue != 3640 {
		t.Errorf("invested_value = %v, want 3640 (3.64 Thousands)", h.InvestedValue)
	}
	if h.CurrentValue != 2660 {
		t.Errorf("current_value = %v, want 2660 (2.66 Thousands)", h.CurrentValue)
	}
	if h.PnL != -977.02 {
		t.Errorf("p&l = %v, want -977.02", h.PnL)
	}
	if h.PnLPercent != -26.88 {
		t.Errorf("p&l_percent = %v, want -26.88", h.PnLPercent)
	}
	if h.ISIN != "INE202E01016" {
		t.Errorf("isin = %q, want INE202E01016", h.ISIN)
	}
	if len(h.TradableExchanges) != 2 || h.TradableExchanges[0] != "NSE" {
		t.Errorf("exchanges = %v, want [NSE, BSE]", h.TradableExchanges)
	}

	// Second holding: "12.42 Thousands" → -12420
	h2 := result.Holdings[1]
	if h2.PnL != -12420 {
		t.Errorf("second p&l = %v, want -12420 (-12.42 Thousands)", h2.PnL)
	}
}

func TestParseAmountString(t *testing.T) {
	tests := []struct {
		input string
		want  float64
	}{
		{"3.64 Thousands", 3640},
		{"49.84 Thousands", 49840},
		{"-12.42 Thousands", -12420},
		{"-977.02", -977.02},
		{"673.6", 673.6},
		{"-26.88%", -26.88},
		{"43.02%", 43.02},
		{"1.5 Lakhs", 150000},
		{"2.0 Crores", 20000000},
		{"", 0},
		{"0", 0},
	}
	for _, tt := range tests {
		got := parseAmountString(tt.input)
		if got != tt.want {
			t.Errorf("parseAmountString(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestParseGrowwAmount_Number(t *testing.T) {
	raw := json.RawMessage(`165.23`)
	got := parseGrowwAmount(raw)
	if got != 165.23 {
		t.Errorf("parseGrowwAmount(165.23) = %v, want 165.23", got)
	}
}

func TestParseGrowwAmount_StringThousands(t *testing.T) {
	raw := json.RawMessage(`"3.64 Thousands"`)
	got := parseGrowwAmount(raw)
	if got != 3640 {
		t.Errorf("parseGrowwAmount(\"3.64 Thousands\") = %v, want 3640", got)
	}
}

func TestParseGrowwAmount_Empty(t *testing.T) {
	got := parseGrowwAmount(nil)
	if got != 0 {
		t.Errorf("parseGrowwAmount(nil) = %v, want 0", got)
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

func TestParseHoldings_DirectArray(t *testing.T) {
	raw := `[{"trading_symbol":"TCS","quantity":10,"average_price":3500,"p&l":"500"}]`
	result := parseHoldings(raw)
	if len(result.Holdings) != 1 {
		t.Fatalf("got %d holdings, want 1", len(result.Holdings))
	}
	if result.Holdings[0].TradingSymbol != "TCS" {
		t.Errorf("symbol = %q, want TCS", result.Holdings[0].TradingSymbol)
	}
	if result.Holdings[0].PnL != 500 {
		t.Errorf("p&l = %v, want 500", result.Holdings[0].PnL)
	}
}

func TestHoldingDisplayName_PrefersTitle(t *testing.T) {
	h := Holding{Title: "JIO Financial Services Ltd.", TradingSymbol: "JIOFIN"}
	if h.DisplayName() != "JIO Financial Services Ltd." {
		t.Errorf("DisplayName = %q, want company title", h.DisplayName())
	}
}

func TestHoldingDisplayName_FallsBackToSymbol(t *testing.T) {
	h := Holding{TradingSymbol: "TCS"}
	if h.DisplayName() != "TCS" {
		t.Errorf("DisplayName = %q, want TCS", h.DisplayName())
	}
}

func TestHoldingCurrentPrice(t *testing.T) {
	h := Holding{CurrentValue: 2660, Quantity: 22}
	if got := h.CurrentPrice(); got != 120.9090909090909 {
		t.Errorf("CurrentPrice = %v, want ~120.91", got)
	}
}

func TestHoldingCurrentPrice_ZeroQuantity(t *testing.T) {
	h := Holding{CurrentValue: 1000, Quantity: 0}
	if got := h.CurrentPrice(); got != 0 {
		t.Errorf("CurrentPrice with zero quantity = %v, want 0", got)
	}
}

func TestHoldingExchanges(t *testing.T) {
	h := Holding{TradableExchanges: []string{"NSE", "BSE"}}
	if h.Exchanges() != "NSE, BSE" {
		t.Errorf("Exchanges = %q, want 'NSE, BSE'", h.Exchanges())
	}
}

func TestConnected_NoTokenFile(t *testing.T) {
	c := New(filepath.Join(t.TempDir(), "missing.json"))
	if c.Connected() {
		t.Errorf("Connected() = true with no token file, want false")
	}
}

func TestConnected_ValidToken(t *testing.T) {
	dir := t.TempDir()
	c := New(filepath.Join(dir, "token.json"))
	if err := c.saveToken(&tokenFile{
		AccessToken: "test-token",
		ExpiresAt:   time.Now().Add(1 * time.Hour),
		ObtainedAt:  time.Now(),
	}); err != nil {
		t.Fatalf("saveToken: %v", err)
	}
	if !c.Connected() {
		t.Errorf("Connected() = false with valid token, want true")
	}
}

func TestConnected_ExpiredToken(t *testing.T) {
	dir := t.TempDir()
	c := New(filepath.Join(dir, "token.json"))
	if err := c.saveToken(&tokenFile{
		AccessToken: "test-token",
		ExpiresAt:   time.Now().Add(-1 * time.Hour),
		ObtainedAt:  time.Now().Add(-2 * time.Hour),
	}); err != nil {
		t.Fatalf("saveToken: %v", err)
	}
	if c.Connected() {
		t.Errorf("Connected() = true with expired token, want false")
	}
	if !c.HasExpiredToken() {
		t.Errorf("HasExpiredToken() = false with expired token file, want true")
	}
}

func TestHasExpiredToken_NoFile(t *testing.T) {
	c := New(filepath.Join(t.TempDir(), "missing.json"))
	if c.HasExpiredToken() {
		t.Errorf("HasExpiredToken() = true with no token file, want false")
	}
}

func TestHasExpiredToken_ValidToken(t *testing.T) {
	dir := t.TempDir()
	c := New(filepath.Join(dir, "token.json"))
	if err := c.saveToken(&tokenFile{
		AccessToken: "test-token",
		ExpiresAt:   time.Now().Add(1 * time.Hour),
	}); err != nil {
		t.Fatalf("saveToken: %v", err)
	}
	if c.HasExpiredToken() {
		t.Errorf("HasExpiredToken() = true with valid token, want false")
	}
}

func TestDisconnect_DeletesToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token.json")
	c := New(path)
	if err := c.saveToken(&tokenFile{AccessToken: "x", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("saveToken: %v", err)
	}
	if err := c.Disconnect(); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("token file should be deleted after Disconnect")
	}
}

func TestDisconnect_NoTokenIsNoop(t *testing.T) {
	c := New(filepath.Join(t.TempDir(), "never_existed.json"))
	if err := c.Disconnect(); err != nil {
		t.Errorf("Disconnect with no token should not error: %v", err)
	}
}

func TestFindHoldingsTool(t *testing.T) {
	tools := []mcp.Tool{
		{Name: "get_quote", Description: "Get live quote"},
		{Name: "get_holdings", Description: "Get DEMAT holdings"},
		{Name: "place_order", Description: "Place an order"},
	}
	if got := mcp.FindTool(tools, "holding"); got != "get_holdings" {
		t.Errorf("FindTool = %q, want get_holdings", got)
	}
}

func TestFindHoldingsTool_NotFound(t *testing.T) {
	tools := []mcp.Tool{
		{Name: "get_quote"},
		{Name: "place_order"},
	}
	if got := mcp.FindTool(tools, "holding"); got != "" {
		t.Errorf("FindTool = %q, want empty string", got)
	}
}
