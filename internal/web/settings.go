package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"cresto/internal/config"
	"cresto/internal/store"
)

// dobToISO converts the stored DDMMYYYY format (used for AIS decryption) to
// YYYY-MM-DD for rendering in an <input type="date">. Returns "" if the input
// isn't the expected 8 digits.
func dobToISO(dob string) string {
	if len(dob) != 8 {
		return ""
	}
	return dob[4:8] + "-" + dob[2:4] + "-" + dob[0:2]
}

// dobFromISO converts the YYYY-MM-DD value submitted by <input type="date">
// to DDMMYYYY for storage (AIS decryption key derivation).
func dobFromISO(iso string) string {
	if len(iso) != 10 || iso[4] != '-' || iso[7] != '-' {
		return strings.ReplaceAll(iso, "-", "")
	}
	return iso[8:10] + iso[5:7] + iso[0:4]
}

// settingsView is the view model for the /settings page. Groups all external
// service connections: brokers (Groww + Kite), greytHR, LLM Studio, and the
// taxpayer profile (PAN/DOB/verification) + bank accounts.
type settingsView struct {
	Groww     brokerStatus
	Kite      brokerStatus
	GreytHR   greythrStatus
	LLMURL    string
	LLMModel  string
	LLMAPIKey string
	LLMHealth string

	// Tax profile — singleton identity for AIS decryption + ITR-2 export.
	HasProfile    bool
	PAN            string
	DOB            string
	DeclarantName  string
	VerifyPlace    string

	// Bank accounts — one-to-many, one primary.
	BankAccounts []store.BankAccount

	// ActiveTab controls which tab is selected on initial render (server-side).
	ActiveTab string
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
			Expired:   s.kite.HasExpiredSession(),
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

	// Tax profile (may not exist on fresh installs).
	if prof, err := s.store.GetTaxpayerProfile(ctx); err == nil {
		v.HasProfile = true
		v.PAN = prof.PAN
		v.DOB = dobToISO(prof.DOB)
		v.DeclarantName = prof.DeclarantName
		v.VerifyPlace = prof.VerificationPlace
	}

	// Bank accounts.
	v.BankAccounts, _ = s.store.ListBankAccounts(ctx)

	// Active tab — server-side default from ?tab= query param.
	tab := r.URL.Query().Get("tab")
	if tab != "connections" && tab != "tax" && tab != "engine" {
		tab = "connections"
	}
	v.ActiveTab = tab

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

// handleSettingsTaxProfile saves the taxpayer profile (PAN, DOB, declarant
// name, verification place) from the settings form. Upserts the singleton row.
func (s *Server) handleSettingsTaxProfile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	prof := store.TaxpayerProfile{
		PAN:               strings.ToUpper(strings.TrimSpace(r.FormValue("pan"))),
		DOB:               dobFromISO(strings.TrimSpace(r.FormValue("dob"))),
		DeclarantName:     strings.TrimSpace(r.FormValue("declarant_name")),
		VerificationPlace: strings.TrimSpace(r.FormValue("verification_place")),
	}
	if err := s.store.SaveTaxpayerProfile(ctx, prof); err != nil {
		s.renderError(w, http.StatusInternalServerError, "Could not save tax profile: "+err.Error())
		return
	}
	http.Redirect(w, r, "/settings?toast=Tax profile saved&variant=success", http.StatusSeeOther)
}

// handleSettingsBankAccountAdd inserts a new bank account from the add form.
func (s *Server) handleSettingsBankAccountAdd(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	acc := store.BankAccount{
		IFSC:          strings.ToUpper(strings.TrimSpace(r.FormValue("ifsc"))),
		AccountNumber: strings.TrimSpace(r.FormValue("account_number")),
		AccountType:   r.FormValue("account_type"),
		BankName:      strings.TrimSpace(r.FormValue("bank_name")),
		IsPrimary:     r.FormValue("is_primary") == "on",
	}
	if acc.AccountType != "savings" && acc.AccountType != "current" {
		acc.AccountType = "savings"
	}
	if acc.IFSC == "" || acc.AccountNumber == "" {
		s.renderError(w, http.StatusBadRequest, "IFSC and account number are required.")
		return
	}
	if _, err := s.store.SaveBankAccount(ctx, acc); err != nil {
		s.renderError(w, http.StatusInternalServerError, "Could not add bank account: "+err.Error())
		return
	}
	http.Redirect(w, r, "/settings?toast=Bank account added&variant=success", http.StatusSeeOther)
}

// handleSettingsBankAccountDelete removes a bank account by ID.
func (s *Server) handleSettingsBankAccountDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		s.renderError(w, http.StatusBadRequest, "Invalid account ID.")
		return
	}
	if err := s.store.DeleteBankAccount(ctx, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.renderError(w, http.StatusNotFound, "Bank account not found.")
			return
		}
		s.renderError(w, http.StatusInternalServerError, "Could not delete bank account: "+err.Error())
		return
	}
	http.Redirect(w, r, "/settings?toast=Bank account removed&variant=success", http.StatusSeeOther)
}

// handleSettingsBankAccountPrimary sets the given account as primary.
func (s *Server) handleSettingsBankAccountPrimary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		s.renderError(w, http.StatusBadRequest, "Invalid account ID.")
		return
	}
	if err := s.store.SetPrimaryBankAccount(ctx, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.renderError(w, http.StatusNotFound, "Bank account not found.")
			return
		}
		s.renderError(w, http.StatusInternalServerError, "Could not set primary account: "+err.Error())
		return
	}
	http.Redirect(w, r, "/settings?toast=Primary account updated&variant=success", http.StatusSeeOther)
}
