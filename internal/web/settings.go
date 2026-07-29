package web

import (
	"net/http"

	"cresto/internal/config"
)

// settingsView is the view model for the /settings page. Groups all external
// service connections: brokers (Groww + Kite) and the LLM Studio endpoint.
type settingsView struct {
	Groww     brokerStatus
	Kite      brokerStatus
	GreytHR   greythrStatus
	LLMURL    string
	LLMModel  string
	LLMAPIKey string
	LLMHealth string
}

type greythrStatus struct {
	Connected bool
	Host      string
}

// handleSettings renders the settings page with broker connection management
// and the LLM Studio status + configuration form.
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
		GreytHR: greythrStatus{
			Connected: s.greythr.Connected(),
		},
		LLMURL:    s.cfg.LMStudioBaseURL,
		LLMModel:  s.cfg.ModelName,
		LLMAPIKey: s.cfg.LMStudioAPIKey,
		LLMHealth: s.llmClient.Health(),
	}
	if sess, err := s.greythr.LoadSession(); err == nil {
		v.GreytHR.Host = sess.Host
	}

	s.render(w, "settings", struct {
		pageData
		settingsView
	}{
		pageData:     pageData{Title: "Settings", PendingCount: len(pending), ActiveBatchID: s.activeBatchID(ctx), Active: "settings"},
		settingsView: v,
	})
}

// handleSettingsLLM saves the LLM Studio configuration from the settings form.
// Persists to the TOML config file and hot-reloads the running LLM client
// with the new endpoint, model, and API key — no server restart needed.
func (s *Server) handleSettingsLLM(w http.ResponseWriter, r *http.Request) {
	baseURL := r.FormValue("base_url")
	model := r.FormValue("model")
	apiKey := r.FormValue("api_key")

	if baseURL == "" {
		baseURL = "http://localhost:1234/v1"
	}
	if model == "" {
		model = "mistralai/ministral-3-3b"
	}

	// Update the in-memory config.
	s.cfg.LMStudioBaseURL = baseURL
	s.cfg.ModelName = model
	s.cfg.LMStudioAPIKey = apiKey

	// Persist to TOML so it survives restarts.
	if err := config.Save(&s.cfg); err != nil {
		s.renderError(w, http.StatusInternalServerError, "Could not save config: "+err.Error())
		return
	}

	// Hot-reload the running client.
	s.llmClient.UpdateConfig(baseURL, model, apiKey)

	http.Redirect(w, r, "/settings?toast=LLM settings saved&variant=success", http.StatusSeeOther)
}
