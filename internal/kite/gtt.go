package kite

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// GTT represents a single Good Till Triggered order from the Kite MCP.
// Field names follow Kite Connect API conventions. Single-leg GTTs have
// trigger_price/quantity at the top level; two-leg (OCO) GTTs store the
// legs in the Orders slice.
type GTT struct {
	TriggerID       int        `json:"id"`
	TradingSymbol   string     `json:"tradingsymbol"`
	Exchange        string     `json:"exchange"`
	Type            string     `json:"type"`
	Status          string     `json:"status"`
	TransactionType string     `json:"transaction_type"`
	Product         string     `json:"product"`
	Quantity        float64    `json:"quantity"`
	TriggerPrice    float64    `json:"trigger_price"`
	Price           float64    `json:"price"`
	LastPrice       float64    `json:"last_price"`
	CreatedAt       string     `json:"created_at"`
	ExpiresAt       string     `json:"expires_at"`
	Orders          []GTTOrder `json:"orders"`
}

// GTTOrder represents one leg of a two-leg (OCO) GTT order.
type GTTOrder struct {
	TransactionType string  `json:"transaction_type"`
	Quantity        float64 `json:"quantity"`
	Price           float64 `json:"price"`
	TriggerPrice    float64 `json:"trigger_price"`
	Product         string  `json:"product"`
	OrderType       string  `json:"order_type"`
}

// GTTResult holds parsed GTT orders plus the raw MCP text.
type GTTResult struct {
	GTTs      []GTT
	RawText   string
	FetchedAt time.Time
}

// ListGTTs fetches active GTT orders via Kite MCP's get_gtts tool.
func (c *Client) ListGTTs(ctx context.Context) (*GTTResult, error) {
	raw, err := c.callRaw(ctx, "get_gtts", nil)
	if err != nil {
		return nil, err
	}
	return parseGTTs(raw), nil
}

// CancelGTT deletes a GTT order by its trigger ID via Kite MCP's
// delete_gtt_order tool. Returns the raw response text.
func (c *Client) CancelGTT(ctx context.Context, triggerID int) (string, error) {
	args := map[string]interface{}{
		"trigger_id": triggerID,
	}
	raw, err := c.callRaw(ctx, "delete_gtt_order", args)
	if err != nil {
		return "", err
	}
	return raw, nil
}

// parseGTTs extracts structured GTT orders from the MCP tool's text
// response. Handles JSON arrays and wrapped objects ({"data": [...]})
// and falls back to raw text on parse failure.
func parseGTTs(rawText string) *GTTResult {
	result := &GTTResult{
		RawText:   rawText,
		FetchedAt: time.Now(),
	}

	trimmed := strings.TrimSpace(rawText)
	if trimmed == "" {
		return result
	}

	// Try as a JSON array.
	var gtts []GTT
	if err := json.Unmarshal([]byte(trimmed), &gtts); err == nil && len(gtts) > 0 {
		result.GTTs = gtts
		return result
	}

	// Try as a JSON object with common wrapper keys.
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &obj); err == nil {
		for _, key := range []string{"gtts", "data", "result"} {
			if inner, ok := obj[key]; ok {
				if err := json.Unmarshal(inner, &gtts); err == nil && len(gtts) > 0 {
					result.GTTs = gtts
					return result
				}
			}
		}
	}

	return result
}

// GetLTP fetches the latest traded price for an instrument via Kite MCP's
// get_ltp tool. Used by the GTT placement flow to auto-fill the required
// last_price parameter.
func (c *Client) GetLTP(ctx context.Context, exchange, tradingsymbol string) (float64, error) {
	instrument := exchange + ":" + tradingsymbol
	args := map[string]interface{}{
		"instruments": []string{instrument},
	}
	raw, err := c.callRaw(ctx, "get_ltp", args)
	if err != nil {
		return 0, err
	}
	price, ok := parseLTP(raw, instrument)
	if !ok {
		return 0, fmt.Errorf("could not parse LTP from response: %s", truncateRaw(raw, 100))
	}
	return price, nil
}

// PlaceGTTParams holds the parameters for placing a GTT order.
// For single-leg orders, set TriggerValue, Quantity, and LimitPrice.
// For two-leg (OCO) orders, set the Lower/Upper fields.
type PlaceGTTParams struct {
	TradingSymbol   string
	Exchange        string
	LastPrice       float64
	TransactionType string
	TriggerType    string // "single" or "two-leg"
	Product         string

	// Single-leg:
	TriggerValue float64
	Quantity     float64
	LimitPrice   float64

	// Two-leg (OCO):
	LowerTriggerValue float64
	LowerQuantity     float64
	LowerLimitPrice   float64
	UpperTriggerValue float64
	UpperQuantity     float64
	UpperLimitPrice   float64
}

// PlaceGTT places a GTT order via Kite MCP's place_gtt_order tool.
// Returns a structured result with the trigger ID (if parseable).
func (c *Client) PlaceGTT(ctx context.Context, params PlaceGTTParams) (*PlaceGTTResult, error) {
	args := map[string]interface{}{
		"exchange":         params.Exchange,
		"tradingsymbol":    params.TradingSymbol,
		"last_price":       params.LastPrice,
		"transaction_type": params.TransactionType,
		"trigger_type":     params.TriggerType,
		"product":          params.Product,
	}

	if params.TriggerType == "single" {
		args["trigger_value"] = params.TriggerValue
		args["quantity"] = params.Quantity
		args["limit_price"] = params.LimitPrice
	} else {
		args["lower_trigger_value"] = params.LowerTriggerValue
		args["lower_quantity"] = params.LowerQuantity
		args["lower_limit_price"] = params.LowerLimitPrice
		args["upper_trigger_value"] = params.UpperTriggerValue
		args["upper_quantity"] = params.UpperQuantity
		args["upper_limit_price"] = params.UpperLimitPrice
	}

	raw, err := c.callRaw(ctx, "place_gtt_order", args)
	if err != nil {
		return nil, err
	}
	result := &PlaceGTTResult{RawText: raw}
	if id, ok := parsePlaceGTTResponse(raw); ok {
		result.TriggerID = id
	}
	return result, nil
}

// PlaceGTTResult holds the parsed trigger ID plus raw MCP text.
type PlaceGTTResult struct {
	TriggerID int
	RawText   string
}

// parseLTP extracts the last_price from a get_ltp MCP response. Handles
// three common shapes:
//   - Object keyed by instrument: {"NSE:RELIANCE": {"last_price": 1275}}
//   - Direct object: {"last_price": 1275}
//   - Array: [{"last_price": 1275}]
func parseLTP(rawText string, instrument string) (float64, bool) {
	trimmed := strings.TrimSpace(rawText)
	if trimmed == "" {
		return 0, false
	}

	// Try as object keyed by instrument.
	var objMap map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &objMap); err == nil {
		// Direct last_price field.
		if raw, ok := objMap["last_price"]; ok {
			var price float64
			if err := json.Unmarshal(raw, &price); err == nil && price > 0 {
				return price, true
			}
		}
		// Keyed by instrument name.
		if raw, ok := objMap[instrument]; ok {
			return extractLastPrice(raw)
		}
		// Try case-insensitive instrument match.
		for k, v := range objMap {
			if strings.EqualFold(k, instrument) {
				return extractLastPrice(v)
			}
		}
		// Try wrapped: {"data": {"last_price": ...}} or {"result": ...}
		for _, key := range []string{"data", "result"} {
			if inner, ok := objMap[key]; ok {
				if price, found := extractLastPrice(inner); found {
					return price, true
				}
			}
		}
	}

	// Try as JSON array.
	var arr []json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &arr); err == nil && len(arr) > 0 {
		return extractLastPrice(arr[0])
	}

	return 0, false
}

// extractLastPrice pulls last_price from a JSON object.
func extractLastPrice(raw json.RawMessage) (float64, bool) {
	var obj struct {
		LastPrice float64 `json:"last_price"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && obj.LastPrice > 0 {
		return obj.LastPrice, true
	}
	return 0, false
}

// truncateRaw truncates a string to n characters, appending "..." if cut.
func truncateRaw(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// parsePlaceGTTResponse extracts the trigger_id from a place_gtt_order
// MCP response. Handles direct objects and wrapped objects.
func parsePlaceGTTResponse(rawText string) (int, bool) {
	trimmed := strings.TrimSpace(rawText)
	if trimmed == "" {
		return 0, false
	}

	var obj struct {
		TriggerID int `json:"trigger_id"`
	}
	if err := json.Unmarshal([]byte(trimmed), &obj); err == nil && obj.TriggerID > 0 {
		return obj.TriggerID, true
	}

	var wrapped map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &wrapped); err == nil {
		for _, key := range []string{"data", "result"} {
			if inner, ok := wrapped[key]; ok {
				if err := json.Unmarshal(inner, &obj); err == nil && obj.TriggerID > 0 {
					return obj.TriggerID, true
				}
			}
		}
	}

	return 0, false
}
