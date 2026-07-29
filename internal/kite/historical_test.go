package kite

import (
	"testing"
)

func TestParseHistoricalData_Empty(t *testing.T) {
	result := parseHistoricalData("")
	if len(result.Candles) != 0 {
		t.Errorf("empty input should yield 0 candles")
	}
}

func TestParseHistoricalData_NonJSONFallback(t *testing.T) {
	raw := "No historical data available"
	result := parseHistoricalData(raw)
	if len(result.Candles) != 0 {
		t.Errorf("non-JSON should yield 0 candles")
	}
	if result.RawText != raw {
		t.Errorf("RawText should be preserved")
	}
}

func TestParseHistoricalData_CandleArrays(t *testing.T) {
	// Kite Connect API format: array of [timestamp, open, high, low, close, volume]
	raw := `[
		["2026-07-01T09:15:00+0530", 100.5, 102.0, 99.8, 101.2, 50000],
		["2026-07-02T09:15:00+0530", 101.2, 103.5, 100.9, 103.0, 62000]
	]`
	result := parseHistoricalData(raw)
	if len(result.Candles) != 2 {
		t.Fatalf("got %d candles, want 2", len(result.Candles))
	}
	c := result.Candles[0]
	if c.Date != "2026-07-01T09:15:00+0530" {
		t.Errorf("date = %q, want 2026-07-01T09:15:00+0530", c.Date)
	}
	if c.Open != 100.5 {
		t.Errorf("open = %v, want 100.5", c.Open)
	}
	if c.High != 102.0 {
		t.Errorf("high = %v, want 102.0", c.High)
	}
	if c.Low != 99.8 {
		t.Errorf("low = %v, want 99.8", c.Low)
	}
	if c.Close != 101.2 {
		t.Errorf("close = %v, want 101.2", c.Close)
	}
	if c.Volume != 50000 {
		t.Errorf("volume = %v, want 50000", c.Volume)
	}
}

func TestParseHistoricalData_CandleObjects(t *testing.T) {
	// Array of candle objects (alternative format).
	raw := `[{"date":"2026-07-01","open":100.5,"high":102.0,"low":99.8,"close":101.2,"volume":50000}]`
	result := parseHistoricalData(raw)
	if len(result.Candles) != 1 {
		t.Fatalf("got %d candles, want 1", len(result.Candles))
	}
	if result.Candles[0].Close != 101.2 {
		t.Errorf("close = %v, want 101.2", result.Candles[0].Close)
	}
}

func TestParseHistoricalData_WrappedCandles(t *testing.T) {
	// Wrapped in {"candles": [...]} or {"data": {"candles": [...]}}
	raw := `{"candles":[
		["2026-07-01", 100.0, 101.0, 99.0, 100.5, 30000]
	]}`
	result := parseHistoricalData(raw)
	if len(result.Candles) != 1 {
		t.Fatalf("got %d candles, want 1", len(result.Candles))
	}
	if result.Candles[0].Close != 100.5 {
		t.Errorf("close = %v, want 100.5", result.Candles[0].Close)
	}
}

func TestParseHistoricalData_WrappedDataCandles(t *testing.T) {
	raw := `{"data":{"candles":[
		["2026-07-01", 100.0, 101.0, 99.0, 100.5, 30000]
	]}}`
	result := parseHistoricalData(raw)
	if len(result.Candles) != 1 {
		t.Fatalf("got %d candles, want 1", len(result.Candles))
	}
}
