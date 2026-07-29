// Package web hosts the HTTP server for payslip upload and review.
// portfolio_mapper.go converts broker-specific holding types into a unified
// portfolioHolding view model, plus aggregation logic for the totals row.
// Pure functions — no network, no database. Tested at the function boundary
// like MapExtraction in mapper.go.
package web

import (
	"cresto/internal/groww"
	"cresto/internal/kite"
)

// Broker name constants used as the portfolioHolding.Broker source tag.
const (
	brokerGroww = "groww"
	brokerKite  = "kite"
)

// portfolioHolding is the unified view model for a stock holding, regardless
// of which broker it came from. Both Groww and Kite holdings map onto this
// type so the portfolio page can render one flat table. Transient — fetched
// live, never persisted.
type portfolioHolding struct {
	Symbol     string  // trading symbol (e.g. RELIANCE, INFY)
	Broker     string  // source broker: "groww" or "kite"
	Title      string  // company name (Groww-only; empty for Kite)
	ISIN       string  // international securities identifier
	Quantity   float64 // number of shares held
	AvgPrice   float64 // average buy price per share
	LTP        float64 // last traded price (Groww derives it, Kite returns it)
	PnL        float64 // absolute profit/loss (broker-provided)
	PnLPercent float64 // total return %, computed universally from avg + LTP
}

// portfolioTotals holds the aggregated values for the unified total row.
// Computed by aggregatePortfolio across all connected brokers' holdings.
type portfolioTotals struct {
	TotalValue     float64 // sum of LTP × quantity (current market value)
	TotalInvested  float64 // sum of avgPrice × quantity (cost basis)
	TotalPnL       float64 // sum of PnL (absolute profit/loss)
}

// mapGrowwHolding converts a Groww holding into the unified portfolioHolding.
// Groww doesn't return LTP directly — it's derived from CurrentValue/Quantity
// via the Holding.CurrentPrice() method. PnL% is computed universally from
// avgPrice and the derived LTP rather than using Groww's own PnLPercent, so
// the formula matches Kite's and stays consistent across brokers.
func mapGrowwHolding(h groww.Holding) portfolioHolding {
	ltp := h.CurrentPrice()
	return portfolioHolding{
		Symbol:     h.TradingSymbol,
		Broker:     brokerGroww,
		Title:      h.Title,
		ISIN:       h.ISIN,
		Quantity:   h.Quantity,
		AvgPrice:   h.AvgPrice,
		LTP:        ltp,
		PnL:        h.PnL,
		PnLPercent: pnlPercent(h.AvgPrice, ltp),
	}
}

// mapKiteHolding converts a Kite holding into the unified portfolioHolding.
// Kite returns LastPrice directly (unlike Groww), so LTP is used as-is. PnL%
// is computed universally from AveragePrice and LastPrice. DayChangePct is
// dropped — the portfolio table normalizes to total P&L %, not intraday change.
func mapKiteHolding(h kite.Holding) portfolioHolding {
	return portfolioHolding{
		Symbol:     h.TradingSymbol,
		Broker:     brokerKite,
		ISIN:       h.ISIN,
		Quantity:   h.Quantity,
		AvgPrice:   h.AveragePrice,
		LTP:        h.LastPrice,
		PnL:        h.PnL,
		PnLPercent: pnlPercent(h.AveragePrice, h.LastPrice),
	}
}

// pnlPercent computes total return percentage from average buy price and
// current price. Returns 0 when avgPrice is zero to avoid division by zero
// (e.g. a gifted stock with no recorded cost basis, or a data glitch).
func pnlPercent(avgPrice, currentPrice float64) float64 {
	if avgPrice == 0 {
		return 0
	}
	return (currentPrice - avgPrice) / avgPrice * 100
}

// aggregatePortfolio sums total value, invested amount, and P&L across all
// holdings. Used for the unified total row at the bottom of the portfolio
// table — a single total across all connected brokers, not per-broker.
func aggregatePortfolio(holdings []portfolioHolding) portfolioTotals {
	var t portfolioTotals
	for _, h := range holdings {
		t.TotalValue += h.LTP * h.Quantity
		t.TotalInvested += h.AvgPrice * h.Quantity
		t.TotalPnL += h.PnL
	}
	return t
}

// PnLPercent computes the overall portfolio return percentage from the
// aggregated totals. Returns 0 when there's no cost basis to avoid division
// by zero. Used by the total row in the portfolio table.
func (t portfolioTotals) PnLPercent() float64 {
	if t.TotalInvested == 0 {
		return 0
	}
	return t.TotalPnL / t.TotalInvested * 100
}
