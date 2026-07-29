package web

import (
	"math"
	"testing"

	"cresto/internal/groww"
	"cresto/internal/kite"
)

func TestMapGrowwHolding(t *testing.T) {
	// Groww doesn't return LTP directly — it's derived from CurrentValue /
	// Quantity. Here CurrentValue=15000, Quantity=10 → LTP=1500.
	// PnL% is computed from avgPrice=1000 and derived LTP=1500 → 50%.
	h := groww.Holding{
		TradingSymbol: "RELIANCE",
		Title:         "Reliance Industries Ltd",
		Quantity:      10,
		AvgPrice:      1000,
		CurrentValue:  15000,
		PnL:           5000,
		PnLPercent:    50, // broker-provided — we recompute to stay consistent
		ISIN:          "INE002A01018",
	}

	got := mapGrowwHolding(h)

	if got.Symbol != "RELIANCE" {
		t.Errorf("Symbol = %q, want RELIANCE", got.Symbol)
	}
	if got.Broker != "groww" {
		t.Errorf("Broker = %q, want groww", got.Broker)
	}
	if got.Title != "Reliance Industries Ltd" {
		t.Errorf("Title = %q", got.Title)
	}
	if got.ISIN != "INE002A01018" {
		t.Errorf("ISIN = %q", got.ISIN)
	}
	if got.Quantity != 10 {
		t.Errorf("Quantity = %v, want 10", got.Quantity)
	}
	if got.AvgPrice != 1000 {
		t.Errorf("AvgPrice = %v, want 1000", got.AvgPrice)
	}
	if got.LTP != 1500 {
		t.Errorf("LTP = %v, want 1500 (derived from CurrentValue/Quantity)", got.LTP)
	}
	if got.PnL != 5000 {
		t.Errorf("PnL = %v, want 5000 (broker-provided)", got.PnL)
	}
	if got.PnLPercent != 50 {
		t.Errorf("PnLPercent = %v, want 50 (computed from avg+LTP)", got.PnLPercent)
	}
}

func TestMapGrowwHolding_DerivesLTPFromCurrentValue(t *testing.T) {
	// Verify the core normalization: Groww has no LastPrice field, so LTP
	// must come from CurrentValue / Quantity. Use awkward numbers to catch
	// rounding or truncation bugs.
	h := groww.Holding{
		TradingSymbol: "INFY",
		Quantity:      7,
		AvgPrice:      1400,
		CurrentValue:  10255.30,
		PnL:           455.30,
	}

	got := mapGrowwHolding(h)

	// Use runtime float64 division (not constant arithmetic) to match the
	// function's computation. Constant expressions get extra precision.
	cv, qty := 10255.30, 7.0
	wantLTP := cv / qty
	if math.Abs(got.LTP-wantLTP) > 1e-9 {
		t.Errorf("LTP = %v, want %v (CurrentValue/Quantity)", got.LTP, wantLTP)
	}
}

func TestMapKiteHolding(t *testing.T) {
	// Kite returns LastPrice directly — no derivation needed.
	// PnL% is computed from AveragePrice=200 and LastPrice=250 → 25%.
	h := kite.Holding{
		TradingSymbol: "TCS",
		Quantity:      20,
		AveragePrice:  200,
		LastPrice:     250,
		PnL:           1000,
		DayChangePct:  1.5, // dropped — portfolio normalizes to total P&L %
		ISIN:          "INE467B01029",
	}

	got := mapKiteHolding(h)

	if got.Symbol != "TCS" {
		t.Errorf("Symbol = %q, want TCS", got.Symbol)
	}
	if got.Broker != "kite" {
		t.Errorf("Broker = %q, want kite", got.Broker)
	}
	if got.Title != "" {
		t.Errorf("Title = %q, want empty (Kite has no title)", got.Title)
	}
	if got.ISIN != "INE467B01029" {
		t.Errorf("ISIN = %q", got.ISIN)
	}
	if got.Quantity != 20 {
		t.Errorf("Quantity = %v, want 20", got.Quantity)
	}
	if got.AvgPrice != 200 {
		t.Errorf("AvgPrice = %v, want 200", got.AvgPrice)
	}
	if got.LTP != 250 {
		t.Errorf("LTP = %v, want 250 (Kite's LastPrice directly)", got.LTP)
	}
	if got.PnL != 1000 {
		t.Errorf("PnL = %v, want 1000 (broker-provided)", got.PnL)
	}
	if got.PnLPercent != 25 {
		t.Errorf("PnLPercent = %v, want 25 (computed from avg+LTP)", got.PnLPercent)
	}
}

func TestPnLPercent_NegativeReturn(t *testing.T) {
	// Stock bought at 200, now worth 150 → -25%.
	got := pnlPercent(200, 150)
	if got != -25 {
		t.Errorf("pnlPercent(200, 150) = %v, want -25", got)
	}
}

func TestPnLPercent_ZeroAvgPrice(t *testing.T) {
	// No cost basis recorded (gifted stock, data glitch) — avoid div by zero.
	got := pnlPercent(0, 150)
	if got != 0 {
		t.Errorf("pnlPercent(0, 150) = %v, want 0 (guard against div by zero)", got)
	}
}

func TestPnLPercent_BothZero(t *testing.T) {
	got := pnlPercent(0, 0)
	if got != 0 {
		t.Errorf("pnlPercent(0, 0) = %v, want 0", got)
	}
}

func TestPnLPercent_BreakEven(t *testing.T) {
	got := pnlPercent(500, 500)
	if got != 0 {
		t.Errorf("pnlPercent(500, 500) = %v, want 0", got)
	}
}

func TestAggregatePortfolio_MixedBrokers(t *testing.T) {
	holdings := []portfolioHolding{
		{Symbol: "RELIANCE", Broker: "groww", Quantity: 10, AvgPrice: 1000, LTP: 1500, PnL: 5000},
		{Symbol: "TCS", Broker: "kite", Quantity: 20, AvgPrice: 200, LTP: 250, PnL: 1000},
	}

	totals := aggregatePortfolio(holdings)

	// TotalValue = (1500×10) + (250×20) = 15000 + 5000 = 20000
	if totals.TotalValue != 20000 {
		t.Errorf("TotalValue = %v, want 20000", totals.TotalValue)
	}
	// TotalInvested = (1000×10) + (200×20) = 10000 + 4000 = 14000
	if totals.TotalInvested != 14000 {
		t.Errorf("TotalInvested = %v, want 14000", totals.TotalInvested)
	}
	// TotalPnL = 5000 + 1000 = 6000
	if totals.TotalPnL != 6000 {
		t.Errorf("TotalPnL = %v, want 6000", totals.TotalPnL)
	}
}

func TestAggregatePortfolio_Empty(t *testing.T) {
	totals := aggregatePortfolio(nil)
	if totals != (portfolioTotals{}) {
		t.Errorf("aggregatePortfolio(nil) = %+v, want zero values", totals)
	}
}

func TestAggregatePortfolio_SingleHolding(t *testing.T) {
	holdings := []portfolioHolding{
		{Symbol: "INFY", Broker: "kite", Quantity: 5, AvgPrice: 1400, LTP: 1600, PnL: 1000},
	}
	totals := aggregatePortfolio(holdings)

	if totals.TotalValue != 8000 {
		t.Errorf("TotalValue = %v, want 8000", totals.TotalValue)
	}
	if totals.TotalInvested != 7000 {
		t.Errorf("TotalInvested = %v, want 7000", totals.TotalInvested)
	}
	if totals.TotalPnL != 1000 {
		t.Errorf("TotalPnL = %v, want 1000", totals.TotalPnL)
	}
}

func TestAggregatePortfolio_NegativePnL(t *testing.T) {
	holdings := []portfolioHolding{
		{Symbol: "LOSS", Broker: "groww", Quantity: 10, AvgPrice: 500, LTP: 300, PnL: -2000},
	}
	totals := aggregatePortfolio(holdings)

	if totals.TotalValue != 3000 {
		t.Errorf("TotalValue = %v, want 3000", totals.TotalValue)
	}
	if totals.TotalInvested != 5000 {
		t.Errorf("TotalInvested = %v, want 5000", totals.TotalInvested)
	}
	if totals.TotalPnL != -2000 {
		t.Errorf("TotalPnL = %v, want -2000", totals.TotalPnL)
	}
}

func TestMapGrowwHolding_ZeroQuantity(t *testing.T) {
	// Zero quantity: Groww's CurrentPrice() returns 0 (guarded), so LTP=0
	// and PnL% computes from avgPrice=1000 and 0 → -100%.
	h := groww.Holding{
		TradingSymbol: "SOLDOUT",
		Quantity:      0,
		AvgPrice:      1000,
		CurrentValue:  0,
		PnL:           0,
	}

	got := mapGrowwHolding(h)

	if got.LTP != 0 {
		t.Errorf("LTP = %v, want 0 (zero quantity → no derivable price)", got.LTP)
	}
	if got.PnLPercent != -100 {
		t.Errorf("PnLPercent = %v, want -100 (avg=1000, ltp=0)", got.PnLPercent)
	}
}

func TestMapKiteHolding_ZeroAvgPrice(t *testing.T) {
	// Zero avg price: pnlPercent guards against div by zero, returns 0.
	h := kite.Holding{
		TradingSymbol: "GIFT",
		Quantity:      5,
		AveragePrice:  0,
		LastPrice:     200,
		PnL:           1000,
	}

	got := mapKiteHolding(h)

	if got.PnLPercent != 0 {
		t.Errorf("PnLPercent = %v, want 0 (zero avg price guarded)", got.PnLPercent)
	}
	if got.LTP != 200 {
		t.Errorf("LTP = %v, want 200", got.LTP)
	}
}

func TestMapGrowwHolding_PnLPercentConsistentAcrossBrokers(t *testing.T) {
	// The core normalization guarantee: two holdings with the same avg price
	// and current price should produce the same PnL%, regardless of broker.
	// Groww derives LTP from CurrentValue/Quantity; Kite returns LastPrice.
	// If we used broker-provided PnL%, Groww's might differ from Kite's.
	growwH := groww.Holding{
		TradingSymbol: "SAME",
		Quantity:      10,
		AvgPrice:      100,
		CurrentValue:  1200, // → LTP = 120
		PnLPercent:    19.5, // broker-provided, intentionally different
	}
	kiteH := kite.Holding{
		TradingSymbol: "SAME",
		Quantity:      10,
		AveragePrice:  100,
		LastPrice:     120,
		DayChangePct:  0.8, // different metric entirely
	}

	gw := mapGrowwHolding(growwH)
	kw := mapKiteHolding(kiteH)

	if gw.PnLPercent != kw.PnLPercent {
		t.Errorf("PnL%% differs across brokers: groww=%v, kite=%v — should be identical for same avg+LTP",
			gw.PnLPercent, kw.PnLPercent)
	}
	want := 20.0
	if gw.PnLPercent != want {
		t.Errorf("PnLPercent = %v, want %v", gw.PnLPercent, want)
	}
}

func TestAggregatePortfolio_FloatPrecision(t *testing.T) {
	// Holdings with fractional prices shouldn't produce wildly imprecise
	// totals. Use a tolerance check to catch accumulation errors.
	holdings := []portfolioHolding{
		{Symbol: "A", Broker: "groww", Quantity: 3, AvgPrice: 333.33, LTP: 444.44, PnL: 333.33},
		{Symbol: "B", Broker: "kite", Quantity: 7, AvgPrice: 111.11, LTP: 222.22, PnL: 777.77},
	}
	totals := aggregatePortfolio(holdings)

	wantValue := 444.44*3 + 222.22*7
	wantInvested := 333.33*3 + 111.11*7
	wantPnL := 333.33 + 777.77

	if math.Abs(totals.TotalValue-wantValue) > 1e-6 {
		t.Errorf("TotalValue = %v, want ~%v", totals.TotalValue, wantValue)
	}
	if math.Abs(totals.TotalInvested-wantInvested) > 1e-6 {
		t.Errorf("TotalInvested = %v, want ~%v", totals.TotalInvested, wantInvested)
	}
	if math.Abs(totals.TotalPnL-wantPnL) > 1e-6 {
		t.Errorf("TotalPnL = %v, want ~%v", totals.TotalPnL, wantPnL)
	}
}
