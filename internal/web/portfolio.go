package web

import (
	"context"
	"net/http"
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
// or JSON (for the refresh button's AJAX update in PF-61).
//
// Extends across tickets: PF-60 adds the broker status strip + page shell;
// PF-61 adds Holdings + Totals; PF-62 adds MFHoldings + Trades.
type portfolioView struct {
	Groww        brokerStatus
	Kite         brokerStatus
	AnyConnected bool // true if at least one broker is connected (template convenience)
}

// preparePortfolioView reads each broker's connection state without making
// any network calls. Holdings/MF/trades fetches arrive in PF-61/PF-62 — this
// ticket only needs to know which brokers are live so the status strip and
// empty state render correctly.
func (s *Server) preparePortfolioView(_ context.Context) portfolioView {
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
	v.AnyConnected = v.Groww.Connected || v.Kite.Connected
	return v
}

// handlePortfolio renders the consolidated portfolio page. Shows a broker
// status strip with connect/disconnect actions and — when at least one broker
// is connected — the holdings table (added in PF-61). When no brokers are
// connected, shows a full-page empty state with both connect buttons.
func (s *Server) handlePortfolio(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pending, _ := s.store.ListPendingReview(ctx)
	v := s.preparePortfolioView(ctx)

	s.render(w, "portfolio", struct {
		pageData
		portfolioView
	}{
		pageData:       pageData{Title: "Portfolio", PendingCount: len(pending), ActiveBatchID: s.activeBatchID(ctx), Active: "portfolio"},
		portfolioView:  v,
	})
}
