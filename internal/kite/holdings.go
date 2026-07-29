package kite

import (
	"encoding/json"
	"strings"
	"time"
)

// Holding represents a single equity holding from the Kite MCP.
// Field names follow Kite's API conventions; the parser handles
// variations in the response shape.
type Holding struct {
	TradingSymbol   string  `json:"tradingsymbol"`
	Quantity        float64 `json:"quantity"`
	AveragePrice    float64 `json:"average_price"`
	LastPrice       float64 `json:"last_price"`
	PnL             float64 `json:"pnl"`
	DayChange       float64 `json:"day_change"`
	DayChangePct    float64 `json:"day_change_percentage"`
	Product         string  `json:"product"`
	Exchange        string  `json:"exchange"`
	ISIN            string  `json:"isin"`
	InstrumentToken int     `json:"instrument_token"`
	InvestedValue   float64 `json:"invested_value"`
	CurrentValue    float64 `json:"current_value"`
}

// DisplayName returns the trading symbol.
func (h Holding) DisplayName() string { return h.TradingSymbol }

// MFHolding represents a single mutual fund holding from the Kite MCP.
type MFHolding struct {
	SchemeName    string  `json:"scheme_name"`
	Folio         string  `json:"folio"`
	Quantity      float64 `json:"quantity"`
	AveragePrice  float64 `json:"average_price"`
	LastPrice     float64 `json:"last_price"`
	PnL           float64 `json:"pnl"`
	DayChangePct  float64 `json:"day_change_percentage"`
	ISIN          string  `json:"isin"`
	InvestedValue float64 `json:"invested_value"`
	CurrentValue  float64 `json:"current_value"`
}

// Trade represents a single executed trade from the Kite MCP.
// Field names follow Kite Connect API conventions.
type Trade struct {
	TradeID         string  `json:"trade_id"`
	OrderID         string  `json:"order_id"`
	TradingSymbol   string  `json:"tradingsymbol"`
	Exchange        string  `json:"exchange"`
	TransactionType string  `json:"transaction_type"`
	Quantity        float64 `json:"quantity"`
	Price           float64 `json:"price"`
	TradeValue      float64 `json:"trade_value"`
	FillTimestamp   string  `json:"fill_timestamp"`
	Product         string  `json:"product"`
}

// parseHoldings extracts structured equity holdings from the MCP tool's
// text response. Kite's response shape is unknown until we test with a
// live authenticated session — this parser handles common shapes (array,
// wrapped object) and falls back to raw text.
func parseHoldings(rawText string) *HoldingsResult {
	result := &HoldingsResult{
		RawText:   rawText,
		FetchedAt: time.Now(),
	}

	trimmed := strings.TrimSpace(rawText)
	if trimmed == "" {
		return result
	}

	// Try as a JSON array.
	var holdings []Holding
	if err := json.Unmarshal([]byte(trimmed), &holdings); err == nil && len(holdings) > 0 {
		result.Holdings = holdings
		return result
	}

	// Try as a JSON object with common wrapper keys.
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &obj); err == nil {
		for _, key := range []string{"holdings", "data", "result"} {
			if inner, ok := obj[key]; ok {
				if err := json.Unmarshal(inner, &holdings); err == nil && len(holdings) > 0 {
					result.Holdings = holdings
					return result
				}
			}
		}
	}

	return result
}

// parseMFHoldings extracts structured MF holdings from the MCP tool's
// text response. Same flexible parsing strategy as parseHoldings.
func parseMFHoldings(rawText string) *MFHoldingsResult {
	result := &MFHoldingsResult{
		RawText:   rawText,
		FetchedAt: time.Now(),
	}

	trimmed := strings.TrimSpace(rawText)
	if trimmed == "" {
		return result
	}

	var holdings []MFHolding
	if err := json.Unmarshal([]byte(trimmed), &holdings); err == nil && len(holdings) > 0 {
		result.Holdings = holdings
		return result
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &obj); err == nil {
		for _, key := range []string{"mf_holding", "mf_holdings", "data", "result"} {
			if inner, ok := obj[key]; ok {
				if err := json.Unmarshal(inner, &holdings); err == nil && len(holdings) > 0 {
					result.Holdings = holdings
					return result
				}
			}
		}
	}

	return result
}

// parseTrades extracts structured trades from the MCP tool's text response.
// Same flexible parsing strategy as parseHoldings.
func parseTrades(rawText string) *TradesResult {
	result := &TradesResult{
		RawText:   rawText,
		FetchedAt: time.Now(),
	}

	trimmed := strings.TrimSpace(rawText)
	if trimmed == "" {
		return result
	}

	var trades []Trade
	if err := json.Unmarshal([]byte(trimmed), &trades); err == nil && len(trades) > 0 {
		result.Trades = trades
		return result
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &obj); err == nil {
		for _, key := range []string{"trades", "data", "result"} {
			if inner, ok := obj[key]; ok {
				if err := json.Unmarshal(inner, &trades); err == nil && len(trades) > 0 {
					result.Trades = trades
					return result
				}
			}
		}
	}

	return result
}
