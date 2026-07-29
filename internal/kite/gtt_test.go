package kite

import (
	"testing"
)

func TestParseGTTs_Empty(t *testing.T) {
	result := parseGTTs("")
	if len(result.GTTs) != 0 {
		t.Errorf("empty input should yield 0 GTTs")
	}
}

func TestParseGTTs_NonJSONFallback(t *testing.T) {
	raw := "No GTT orders found"
	result := parseGTTs(raw)
	if len(result.GTTs) != 0 {
		t.Errorf("non-JSON should yield 0 GTTs")
	}
	if result.RawText != raw {
		t.Errorf("RawText should be preserved")
	}
}

func TestParseGTTs_Array(t *testing.T) {
	raw := `[
		{"id":123456,"type":"single","status":"active","tradingsymbol":"RELIANCE","exchange":"NSE","product":"CNC","transaction_type":"SELL","quantity":10,"trigger_price":3200,"price":0,"last_price":3100,"created_at":"2024-01-01 10:00:00","expires_at":"2025-01-01 10:00:00"}
	]`
	result := parseGTTs(raw)
	if len(result.GTTs) != 1 {
		t.Fatalf("got %d GTTs, want 1", len(result.GTTs))
	}
	g := result.GTTs[0]
	if g.TriggerID != 123456 {
		t.Errorf("trigger_id = %d, want 123456", g.TriggerID)
	}
	if g.TradingSymbol != "RELIANCE" {
		t.Errorf("tradingsymbol = %q, want RELIANCE", g.TradingSymbol)
	}
	if g.Type != "single" {
		t.Errorf("type = %q, want single", g.Type)
	}
	if g.Status != "active" {
		t.Errorf("status = %q, want active", g.Status)
	}
	if g.Quantity != 10 {
		t.Errorf("quantity = %v, want 10", g.Quantity)
	}
	if g.TriggerPrice != 3200 {
		t.Errorf("trigger_price = %v, want 3200", g.TriggerPrice)
	}
	if g.TransactionType != "SELL" {
		t.Errorf("transaction_type = %q, want SELL", g.TransactionType)
	}
}

func TestParseGTTs_TwoLegOCO(t *testing.T) {
	raw := `[
		{
			"id":123457,
			"type":"two-leg",
			"status":"active",
			"tradingsymbol":"TCS",
			"exchange":"NSE",
			"last_price":3500,
			"created_at":"2024-01-01 10:00:00",
			"orders":[
				{"transaction_type":"SELL","quantity":10,"price":2800,"trigger_price":2800,"product":"CNC","order_type":"LIMIT"},
				{"transaction_type":"SELL","quantity":10,"price":3200,"trigger_price":3200,"product":"CNC","order_type":"LIMIT"}
			]
		}
	]`
	result := parseGTTs(raw)
	if len(result.GTTs) != 1 {
		t.Fatalf("got %d GTTs, want 1", len(result.GTTs))
	}
	g := result.GTTs[0]
	if g.Type != "two-leg" {
		t.Errorf("type = %q, want two-leg", g.Type)
	}
	if len(g.Orders) != 2 {
		t.Fatalf("got %d orders, want 2", len(g.Orders))
	}
	if g.Orders[0].TriggerPrice != 2800 {
		t.Errorf("lower trigger = %v, want 2800", g.Orders[0].TriggerPrice)
	}
	if g.Orders[1].TriggerPrice != 3200 {
		t.Errorf("upper trigger = %v, want 3200", g.Orders[1].TriggerPrice)
	}
}

func TestParseGTTs_WrappedObject(t *testing.T) {
	raw := `{"data":[
		{"id":100,"type":"single","status":"active","tradingsymbol":"INFY","exchange":"NSE","quantity":5,"trigger_price":1500,"created_at":"2024-01-01 10:00:00"}
	]}`
	result := parseGTTs(raw)
	if len(result.GTTs) != 1 {
		t.Fatalf("got %d GTTs, want 1", len(result.GTTs))
	}
	if result.GTTs[0].TradingSymbol != "INFY" {
		t.Errorf("tradingsymbol = %q, want INFY", result.GTTs[0].TradingSymbol)
	}
}

func TestParseLTP_KeyedByInstrument(t *testing.T) {
	raw := `{"NSE:RELIANCE": {"last_price": 1275.5, "last_trade_time": "2024-01-01T10:00:00+0530"}}`
	price, ok := parseLTP(raw, "NSE:RELIANCE")
	if !ok {
		t.Fatalf("parseLTP returned ok=false, want true")
	}
	if price != 1275.5 {
		t.Errorf("price = %v, want 1275.5", price)
	}
}

func TestParseLTP_DirectObject(t *testing.T) {
	raw := `{"last_price": 3200.0}`
	price, ok := parseLTP(raw, "NSE:RELIANCE")
	if !ok {
		t.Fatalf("parseLTP returned ok=false, want true")
	}
	if price != 3200.0 {
		t.Errorf("price = %v, want 3200.0", price)
	}
}

func TestParseLTP_Array(t *testing.T) {
	raw := `[{"last_price": 999.75}]`
	price, ok := parseLTP(raw, "NSE:RELIANCE")
	if !ok {
		t.Fatalf("parseLTP returned ok=false, want true")
	}
	if price != 999.75 {
		t.Errorf("price = %v, want 999.75", price)
	}
}

func TestParseLTP_Empty(t *testing.T) {
	_, ok := parseLTP("", "NSE:RELIANCE")
	if ok {
		t.Errorf("parseLTP should return ok=false for empty input")
	}
}

func TestParseLTP_NonJSON(t *testing.T) {
	_, ok := parseLTP("Price not available", "NSE:RELIANCE")
	if ok {
		t.Errorf("parseLTP should return ok=false for non-JSON input")
	}
}

func TestParsePlaceGTTResponse_Direct(t *testing.T) {
	raw := `{"trigger_id": 123456, "status": "active"}`
	id, ok := parsePlaceGTTResponse(raw)
	if !ok {
		t.Fatalf("parsePlaceGTTResponse returned ok=false, want true")
	}
	if id != 123456 {
		t.Errorf("trigger_id = %d, want 123456", id)
	}
}

func TestParsePlaceGTTResponse_Wrapped(t *testing.T) {
	raw := `{"data": {"trigger_id": 789012, "status": "active"}}`
	id, ok := parsePlaceGTTResponse(raw)
	if !ok {
		t.Fatalf("parsePlaceGTTResponse returned ok=false, want true")
	}
	if id != 789012 {
		t.Errorf("trigger_id = %d, want 789012", id)
	}
}

func TestParsePlaceGTTResponse_NoID(t *testing.T) {
	raw := `{"status": "active"}`
	_, ok := parsePlaceGTTResponse(raw)
	if ok {
		t.Errorf("parsePlaceGTTResponse should return ok=false when no trigger_id")
	}
}
