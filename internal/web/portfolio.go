package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"cresto/internal/groww"
	"cresto/internal/kite"
)

// brokerStatus is the connection state of a single broker, surfaced in the
// portfolio page's broker status strip. It carries only state — no data.
// The connect/disconnect links are constant paths the template hardcodes.
type brokerStatus struct {
	Name      string // display name: "Groww" or "Kite"
	Connected bool   // has a live session/token right now
	Expired   bool   // token lapsed (Groww-only; Kite has no expired state)
}

// portfolioView is the shared state for the /portfolio page. Computed once
// by preparePortfolioView and serialized as either HTML (wrapped in pageData)
// or JSON (for the refresh button's AJAX update).
//
// Extends across tickets: PF-60 added the broker status strip + page shell;
// PF-61 adds Holdings + Totals; PF-62 adds MFHoldings + Trades.
type portfolioView struct {
	Groww        brokerStatus
	Kite         brokerStatus
	AnyConnected bool   // true if at least one broker is connected (template convenience)
	Holdings     []portfolioHolding
	Totals       portfolioTotals
	MFHoldings   []kite.MFHolding // Kite-only; nil when Kite disconnected or no MF
	Trades       []kite.Trade     // Kite-only; nil when Kite disconnected or no trades
	GrowwError   string           // user-facing error if Groww fetch failed
	KiteError    string           // user-facing error if Kite fetch failed
	FetchedAt    string           // formatted timestamp of the latest successful fetch
}

// preparePortfolioView reads each broker's connection state and, for connected
// brokers, fetches holdings and maps them to the unified portfolioHolding type.
// Per-broker error handling: if one broker's fetch fails, the other's data
// still renders. If a broker's session expires mid-fetch, its status updates
// to expired/disconnected and its rows disappear.
func (s *Server) preparePortfolioView(ctx context.Context) portfolioView {
	v := portfolioView{
		Groww: brokerStatus{
			Name:      "Groww",
			Connected: s.groww.Connected(),
			Expired:   s.groww.HasExpiredToken(),
		},
		Kite: brokerStatus{
			Name:      "Kite",
			Connected: s.kite.Connected(),
		},
	}

	var latestFetch time.Time

	// Fetch Groww holdings if connected.
	if v.Groww.Connected {
		result, err := s.groww.Holdings(ctx)
		if err != nil {
			if errors.Is(err, groww.ErrNotConnected) {
				v.Groww.Connected = false
				v.Groww.Expired = true
			} else {
				v.GrowwError = userFacingBrokerError("Groww", err)
			}
		} else {
			for _, h := range result.Holdings {
				v.Holdings = append(v.Holdings, mapGrowwHolding(h))
			}
			if result.FetchedAt.After(latestFetch) {
				latestFetch = result.FetchedAt
			}
		}
	}

	// Fetch Kite holdings, MF, and trades if connected. Each fetch is
	// independent — MF/trades failures hide their own section without
	// blocking the others (fixes the prepareKiteView cascade bug).
	if v.Kite.Connected {
		result, err := s.kite.Holdings(ctx)
		if err != nil {
			if errors.Is(err, kite.ErrNotConnected) {
				v.Kite.Connected = false
			} else {
				v.KiteError = userFacingBrokerError("Kite", err)
			}
		} else {
			for _, h := range result.Holdings {
				v.Holdings = append(v.Holdings, mapKiteHolding(h))
			}
			if result.FetchedAt.After(latestFetch) {
				latestFetch = result.FetchedAt
			}
		}

		// MF holdings — non-fatal. On error the section is hidden (nil).
		// ErrNotConnected means the session lapsed mid-fetch; mark
		// disconnected so the status strip updates.
		if v.Kite.Connected {
			if mfResult, err := s.kite.MFHoldings(ctx); err == nil {
				v.MFHoldings = mfResult.Holdings
				if mfResult.FetchedAt.After(latestFetch) {
					latestFetch = mfResult.FetchedAt
				}
			} else if errors.Is(err, kite.ErrNotConnected) {
				v.Kite.Connected = false
			}
		}

		// Trades — non-fatal. Same handling as MF.
		if v.Kite.Connected {
			if trResult, err := s.kite.Trades(ctx); err == nil {
				v.Trades = trResult.Trades
				if trResult.FetchedAt.After(latestFetch) {
					latestFetch = trResult.FetchedAt
				}
			} else if errors.Is(err, kite.ErrNotConnected) {
				v.Kite.Connected = false
			}
		}
	}

	v.AnyConnected = v.Groww.Connected || v.Kite.Connected
	v.Totals = aggregatePortfolio(v.Holdings)
	if !latestFetch.IsZero() {
		v.FetchedAt = latestFetch.Format("3:04 PM, Jan 2")
	}
	return v
}

// handlePortfolio renders the consolidated portfolio page.
func (s *Server) handlePortfolio(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pending, _ := s.store.ListPendingReview(ctx)
	v := s.preparePortfolioView(ctx)

	s.render(w, "portfolio", struct {
		pageData
		portfolioView
	}{
		pageData:      pageData{Title: "Portfolio", PendingCount: len(pending), ActiveBatchID: s.activeBatchID(ctx), Active: "portfolio"},
		portfolioView: v,
	})
}

// --- AJAX API ---

// portfolioHoldingJSON is the JSON-serializable form of portfolioHolding for
// the refresh endpoint. Field names use snake_case for JS convention.
type portfolioHoldingJSON struct {
	Symbol     string  `json:"symbol"`
	Broker     string  `json:"broker"`
	Title      string  `json:"title"`
	Quantity   float64 `json:"quantity"`
	AvgPrice   float64 `json:"avg_price"`
	LTP        float64 `json:"ltp"`
	PnL        float64 `json:"pnl"`
	PnLPercent float64 `json:"pnl_percent"`
}

type portfolioHoldingsResponse struct {
	GrowwConnected bool                   `json:"groww_connected"`
	KiteConnected  bool                   `json:"kite_connected"`
	GrowwExpired   bool                   `json:"groww_expired"`
	Holdings       []portfolioHoldingJSON `json:"holdings"`
	TotalValue     float64                `json:"total_value"`
	TotalInvested  float64                `json:"total_invested"`
	TotalPnL       float64                `json:"total_pnl"`
	TotalPnLPct    float64                `json:"total_pnl_pct"`
	MFHoldings     []portfolioMFJSON      `json:"mf_holdings"`
	Trades         []portfolioTradeJSON   `json:"trades"`
	FetchedAt      string                 `json:"fetched_at"`
	GrowwError     string                 `json:"groww_error,omitempty"`
	KiteError      string                 `json:"kite_error,omitempty"`
}

type portfolioMFJSON struct {
	SchemeName string  `json:"scheme_name"`
	Folio      string  `json:"folio"`
	Quantity   float64 `json:"quantity"`
	AvgPrice   float64 `json:"avg_price"`
	LastPrice  float64 `json:"last_price"`
	PnL        float64 `json:"pnl"`
}

type portfolioTradeJSON struct {
	Symbol          string  `json:"symbol"`
	TransactionType string  `json:"transaction_type"`
	Quantity        float64 `json:"quantity"`
	Price           float64 `json:"price"`
	TradeValue      float64 `json:"trade_value"`
	FillTimestamp   string  `json:"fill_timestamp"`
}

// handlePortfolioHoldingsAPI returns unified holdings as JSON for the Refresh
// button's AJAX update. Fetches from all connected brokers, maps to the
// unified type, and returns the combined holdings + totals. Per-broker errors
// are included so the client can show alerts without losing the other
// broker's data.
func (s *Server) handlePortfolioHoldingsAPI(w http.ResponseWriter, r *http.Request) {
	v := s.preparePortfolioView(r.Context())

	holdings := make([]portfolioHoldingJSON, 0, len(v.Holdings))
	for _, h := range v.Holdings {
		holdings = append(holdings, portfolioHoldingJSON{
			Symbol:     h.Symbol,
			Broker:     h.Broker,
			Title:      h.Title,
			Quantity:   h.Quantity,
			AvgPrice:   h.AvgPrice,
			LTP:        h.LTP,
			PnL:        h.PnL,
			PnLPercent: h.PnLPercent,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(portfolioHoldingsResponse{
		GrowwConnected: v.Groww.Connected,
		KiteConnected:  v.Kite.Connected,
		GrowwExpired:   v.Groww.Expired,
		Holdings:       holdings,
		TotalValue:     v.Totals.TotalValue,
		TotalInvested:  v.Totals.TotalInvested,
		TotalPnL:       v.Totals.TotalPnL,
		TotalPnLPct:    v.Totals.PnLPercent(),
		MFHoldings:     mapMFHoldings(v.MFHoldings),
		Trades:         mapTrades(v.Trades),
		FetchedAt:      v.FetchedAt,
		GrowwError:     v.GrowwError,
		KiteError:      v.KiteError,
	})
}

// mapMFHoldings converts Kite MF holdings to the JSON-serializable form.
// MF holdings are Kite-only — no cross-broker mapping needed.
func mapMFHoldings(mf []kite.MFHolding) []portfolioMFJSON {
	if len(mf) == 0 {
		return nil
	}
	out := make([]portfolioMFJSON, len(mf))
	for i, m := range mf {
		out[i] = portfolioMFJSON{
			SchemeName: m.SchemeName,
			Folio:      m.Folio,
			Quantity:   m.Quantity,
			AvgPrice:   m.AveragePrice,
			LastPrice:  m.LastPrice,
			PnL:        m.PnL,
		}
	}
	return out
}

// mapTrades converts Kite trades to the JSON-serializable form.
func mapTrades(trades []kite.Trade) []portfolioTradeJSON {
	if len(trades) == 0 {
		return nil
	}
	out := make([]portfolioTradeJSON, len(trades))
	for i, t := range trades {
		out[i] = portfolioTradeJSON{
			Symbol:          t.TradingSymbol,
			TransactionType: t.TransactionType,
			Quantity:        t.Quantity,
			Price:           t.Price,
			TradeValue:      t.TradeValue,
			FillTimestamp:   t.FillTimestamp,
		}
	}
	return out
}
