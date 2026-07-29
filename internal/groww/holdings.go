package groww

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// Holding represents a single stock holding from the Groww MCP.
type Holding struct {
	TradingSymbol   string  `json:"trading_symbol"`
	Title           string  `json:"title"`
	Quantity        float64 `json:"quantity"`
	AvgPrice        float64 `json:"average_price"`
	InvestedValue   float64 `json:"invested_value"`
	CurrentValue    float64 `json:"current_value"`
	PnL             float64 `json:"p&l"`
	PnLPercent      float64 `json:"p&l_percent"`
	ISIN            string  `json:"symbol_isin"`
	TradableExchanges []string `json:"tradable_exchanges"`
}

// DisplayName returns the company name if available, otherwise the symbol.
func (h Holding) DisplayName() string {
	if h.Title != "" {
		return h.Title
	}
	return h.TradingSymbol
}

// DisplaySymbol returns the trading symbol for the compact column.
func (h Holding) DisplaySymbol() string {
	return h.TradingSymbol
}

// Exchanges returns a comma-separated list of tradable exchanges.
func (h Holding) Exchanges() string {
	if len(h.TradableExchanges) == 0 {
		return ""
	}
	return strings.Join(h.TradableExchanges, ", ")
}

// CurrentPrice derives the current price from current_value / quantity
// (Groww doesn't return LTP directly).
func (h Holding) CurrentPrice() float64 {
	if h.Quantity > 0 && h.CurrentValue > 0 {
		return h.CurrentValue / h.Quantity
	}
	return 0
}

// growwHoldingRaw is the intermediate shape matching Groww's JSON. Amount
// fields come as strings like "3.64 Thousands" or "-977.02" or "-26.88%",
// so we capture them as raw JSON and parse after.
type growwHoldingRaw struct {
	TradingSymbol      string          `json:"trading_symbol"`
	Title              string          `json:"title"`
	Quantity           float64         `json:"quantity"`
	AveragePrice       float64         `json:"average_price"`
	InvestedValue      json.RawMessage `json:"invested_value"`
	CurrentValue       json.RawMessage `json:"current_value"`
	PnL                json.RawMessage `json:"p&l"`
	PnLPercent         json.RawMessage `json:"p&l_percent"`
	SymbolISIN         string          `json:"symbol_isin"`
	TradableExchanges  []string        `json:"tradable_exchanges"`
}

// parseGrowwAmount converts Groww's string amounts to float64.
// Handles: "3.64 Thousands" → 3640, "-977.02" → -977.02, "673.6" → 673.6,
// "-26.88%" → -26.88, and bare numbers.
func parseGrowwAmount(raw json.RawMessage) float64 {
	if len(raw) == 0 {
		return 0
	}
	// Try as a plain number first.
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return f
	}
	// It's a string — strip quotes and parse.
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0
	}
	return parseAmountString(s)
}

// parseAmountString parses strings like "3.64 Thousands", "-977.02", "-26.88%".
func parseAmountString(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	s = strings.TrimSuffix(s, "%")
	s = strings.TrimSpace(s)

	multiplier := 1.0
	if strings.HasSuffix(s, "Thousands") {
		multiplier = 1000
		s = strings.TrimSuffix(s, "Thousands")
		s = strings.TrimSpace(s)
	} else if strings.HasSuffix(s, "Lakhs") {
		multiplier = 100000
		s = strings.TrimSuffix(s, "Lakhs")
		s = strings.TrimSpace(s)
	} else if strings.HasSuffix(s, "Crores") {
		multiplier = 10000000
		s = strings.TrimSuffix(s, "Crores")
		s = strings.TrimSpace(s)
	}

	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return f * multiplier
}

// parseHoldings extracts structured holdings from the MCP tool's text
// response. Groww wraps holdings in {"result": {"holdings": [...]}}.
// We handle that path, plus unwrapped arrays and objects, with a fallback
// to raw text for the UI to display in a <pre> block.
func parseHoldings(rawText string) *HoldingsResult {
	result := &HoldingsResult{
		RawText:   rawText,
		FetchedAt: time.Now(),
	}

	trimmed := strings.TrimSpace(rawText)
	if trimmed == "" {
		return result
	}

	// Try parsing as JSON. Groww's response is an object, but we also
	// handle bare arrays for robustness.
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
		// Not an object — try as a top-level array.
		holdings := parseHoldingsArray(json.RawMessage(trimmed))
		if len(holdings) > 0 {
			result.Holdings = holdings
		}
		return result
	}

	// Path 1: {"result": {"holdings": [...]}} — Groww's actual shape.
	if resultRaw, ok := obj["result"]; ok {
		var resultObj map[string]json.RawMessage
		if err := json.Unmarshal(resultRaw, &resultObj); err == nil {
			if holdingsRaw, ok := resultObj["holdings"]; ok {
				holdings := parseHoldingsArray(holdingsRaw)
				if len(holdings) > 0 {
					result.Holdings = holdings
					return result
				}
			}
		}
	}

	// Path 2: {"holdings": [...]} — direct wrapper.
	for _, key := range []string{"holdings", "data", "positions", "stocks"} {
		if inner, ok := obj[key]; ok {
			holdings := parseHoldingsArray(inner)
			if len(holdings) > 0 {
				result.Holdings = holdings
				return result
			}
		}
	}

	// Path 3: top-level array [{...}, {...}].
	holdings := parseHoldingsArray(json.RawMessage(trimmed))
	if len(holdings) > 0 {
		result.Holdings = holdings
	}

	return result
}

// parseHoldingsArray unmarshals a JSON array of raw holdings into typed
// Holdings, converting string amounts to float64 along the way.
func parseHoldingsArray(raw json.RawMessage) []Holding {
	var raws []growwHoldingRaw
	if err := json.Unmarshal(raw, &raws); err != nil {
		return nil
	}
	holdings := make([]Holding, 0, len(raws))
	for _, r := range raws {
		holdings = append(holdings, Holding{
			TradingSymbol:     r.TradingSymbol,
			Title:             r.Title,
			Quantity:          r.Quantity,
			AvgPrice:          r.AveragePrice,
			InvestedValue:     parseGrowwAmount(r.InvestedValue),
			CurrentValue:      parseGrowwAmount(r.CurrentValue),
			PnL:               parseGrowwAmount(r.PnL),
			PnLPercent:        parseGrowwAmount(r.PnLPercent),
			ISIN:              r.SymbolISIN,
			TradableExchanges: r.TradableExchanges,
		})
	}
	return holdings
}
