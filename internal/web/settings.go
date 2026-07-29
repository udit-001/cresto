package web

import (
	"net/http"
)

// settingsView is the view model for the /settings page. Groups all external
// service connections: brokers (Groww + Kite) and the LLM Studio endpoint.
type settingsView struct {
	Groww      brokerStatus
	Kite       brokerStatus
	LLMURL     string
	LLMModel   string
	LLMHealth  string // raw state from llmClient.Health(): loaded, server_down, etc.
}

// handleSettings renders the settings page with broker connection management
// (moved from /portfolio) and the LLM Studio status display.
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pending, _ := s.store.ListPendingReview(ctx)
	v := settingsView{
		Groww: brokerStatus{
			Name:      "Groww",
			Connected: s.groww.Connected(),
			Expired:   s.groww.HasExpiredToken(),
		},
		Kite: brokerStatus{
			Name:      "Kite",
			Connected: s.kite.Connected(),
		},
		LLMURL:    s.cfg.LMStudioBaseURL,
		LLMModel:  s.cfg.ModelName,
		LLMHealth: s.llmClient.Health(),
	}

	s.render(w, "settings", struct {
		pageData
		settingsView
	}{
		pageData:     pageData{Title: "Settings", PendingCount: len(pending), ActiveBatchID: s.activeBatchID(ctx), Active: "settings"},
		settingsView: v,
	})
}
