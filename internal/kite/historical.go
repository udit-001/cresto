package kite

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// Candle represents a single OHLCV data point from Kite's historical data API.
type Candle struct {
	Date   string  `json:"date"`
	Open   float64 `json:"open"`
	High   float64 `json:"high"`
	Low    float64 `json:"low"`
	Close  float64 `json:"close"`
	Volume float64 `json:"volume"`
}

// HistoricalResult holds parsed candle data plus the raw MCP text.
type HistoricalResult struct {
	Candles   []Candle
	RawText   string
	FetchedAt time.Time
}

// HistoricalData fetches historical candle data for an instrument via Kite
// MCP's get_historical_data tool. The instrumentToken comes from the
// holdings response (Holding.InstrumentToken). Interval is a Kite interval
// string (e.g. "day", "minute", "5minute"). from and to are the date range.
func (c *Client) HistoricalData(ctx context.Context, instrumentToken int, interval string, from, to time.Time) (*HistoricalResult, error) {
	args := map[string]interface{}{
		"instrument_token": instrumentToken,
		"interval":         interval,
		"from_date":        from.Format("2006-01-02 15:04:05"),
		"to_date":          to.Format("2006-01-02") + " 23:59:59",
	}
	raw, err := c.callRaw(ctx, "get_historical_data", args)
	if err != nil {
		return nil, err
	}
	return parseHistoricalData(raw), nil
}

// parseHistoricalData extracts structured candles from the MCP tool's text
// response. Kite Connect returns candles as arrays of
// [timestamp, open, high, low, close, volume]. The MCP tool may also wrap
// this in {"candles": [...]} or {"data": {"candles": [...]}}. This parser
// handles all three shapes and falls back to raw text on parse failure.
func parseHistoricalData(rawText string) *HistoricalResult {
	result := &HistoricalResult{
		RawText:   rawText,
		FetchedAt: time.Now(),
	}

	trimmed := strings.TrimSpace(rawText)
	if trimmed == "" {
		return result
	}

	// Try as a JSON array of candles.
	if candles := tryParseCandleArray(trimmed); len(candles) > 0 {
		result.Candles = candles
		return result
	}

	// Try as a JSON object with wrapper keys.
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &obj); err == nil {
		// Direct "candles" key.
		if inner, ok := obj["candles"]; ok {
			if candles := tryParseCandleArray(string(inner)); len(candles) > 0 {
				result.Candles = candles
				return result
			}
		}
		// "data" key — could be {"candles": [...]} or a direct array.
		if inner, ok := obj["data"]; ok {
			if candles := tryParseCandleArray(string(inner)); len(candles) > 0 {
				result.Candles = candles
				return result
			}
			// "data" wrapping another object with "candles".
			var innerObj map[string]json.RawMessage
			if err := json.Unmarshal(inner, &innerObj); err == nil {
				if deep, ok := innerObj["candles"]; ok {
					if candles := tryParseCandleArray(string(deep)); len(candles) > 0 {
						result.Candles = candles
						return result
					}
				}
			}
		}
	}

	return result
}

// tryParseCandleArray parses a JSON array where each element is either a
// Kite Connect candle array [date, open, high, low, close, volume] or a
// candle object {"date": ..., "open": ...}. Returns nil on any failure.
func tryParseCandleArray(rawJSON string) []Candle {
	var elements []json.RawMessage
	if err := json.Unmarshal([]byte(rawJSON), &elements); err != nil {
		return nil
	}
	if len(elements) == 0 {
		return nil
	}

	candles := make([]Candle, 0, len(elements))
	for _, elem := range elements {
		c, ok := parseCandleElement(elem)
		if !ok {
			return nil
		}
		candles = append(candles, c)
	}
	return candles
}

// parseCandleElement parses a single candle from a JSON array or object.
// Kite Connect format: ["2026-07-01T09:15:00+0530", 100.5, 102.0, 99.8, 101.2, 50000]
func parseCandleElement(raw json.RawMessage) (Candle, bool) {
	// Try as candle array [date, o, h, l, c, v].
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err == nil && len(arr) >= 5 {
		var c Candle
		if s, err := unquote(arr[0]); err == nil {
			c.Date = s
		}
		c.Open = jsonFloat(arr[1])
		c.High = jsonFloat(arr[2])
		c.Low = jsonFloat(arr[3])
		c.Close = jsonFloat(arr[4])
		if len(arr) >= 6 {
			c.Volume = jsonFloat(arr[5])
		}
		return c, true
	}

	// Try as candle object.
	var c Candle
	if err := json.Unmarshal(raw, &c); err == nil && c.Date != "" {
		return c, true
	}

	return Candle{}, false
}

// unquote extracts a string from a JSON raw message.
func unquote(raw json.RawMessage) (string, error) {
	var s string
	err := json.Unmarshal(raw, &s)
	return s, err
}

// jsonFloat extracts a float64 from a JSON raw message, tolerating
// both numbers and numeric strings.
func jsonFloat(raw json.RawMessage) float64 {
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return f
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		var f2 float64
		if err := json.Unmarshal([]byte(s), &f2); err == nil {
			return f2
		}
	}
	return 0
}
