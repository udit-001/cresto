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
	GrowwError   string // user-facing error if Groww fetch failed
	KiteError    string // user-facing error if Kite fetch failed
	FetchedAt    string // formatted timestamp of the latest successful fetch
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

	// Fetch Kite holdings if connected.
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
	FetchedAt      string                 `json:"fetched_at"`
	GrowwError     string                 `json:"groww_error,omitempty"`
	KiteError      string                 `json:"kite_error,omitempty"`
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
		FetchedAt:      v.FetchedAt,
		GrowwError:     v.GrowwError,
		KiteError:      v.KiteError,
	})
}
