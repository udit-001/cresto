package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"cresto/internal/greythr"
	"cresto/internal/store"
)

// greythrPageData is the template payload for the /greythr page.
type greythrPageData struct {
	pageData
	Connected  bool
	Expired    bool
	Host       string
	ProfileID  int
	Email      string
	Months     []greythr.PayslipMonth
	FetchError string
	Prefill    greythrPrefill
}

type greythrPrefill struct {
	Host       string
	AccessToken string
	ProfileID  string
}

func (s *Server) handleGreytHRPage(w http.ResponseWriter, r *http.Request) {
	data := greythrPageData{
		pageData: pageData{Title: "greytHR", Active: "greythr"},
	}

	if !s.greythr.Connected() {
		s.render(w, "greythr", data)
		return
	}

	sess, err := s.greythr.LoadSession()
	if err != nil {
		data.FetchError = "Session error: " + err.Error()
		s.render(w, "greythr", data)
		return
	}

	data.Host = sess.Host
	data.ProfileID = sess.ProfileID
	data.Email = sess.Email

	months, err := s.greythr.ListPayslipMonths(r.Context())
	if err != nil {
		if errors.Is(err, greythr.ErrNotConnected) {
			data.Expired = true
		} else {
			data.FetchError = err.Error()
			data.Connected = true
		}
	} else {
		data.Connected = true
		data.Months = months.Months
	}

	s.render(w, "greythr", data)
}

func (s *Server) handleGreytHRConnectForm(w http.ResponseWriter, r *http.Request) {
	host := r.URL.Query().Get("host")
	accessToken := r.URL.Query().Get("access_token")
	profileIDStr := r.URL.Query().Get("profile_id")

	// If all three params are present (from extension redirect), save
	// directly — the user already clicked "Connect" in the extension popup.
	if host != "" && accessToken != "" && profileIDStr != "" {
		profileID, err := strconv.Atoi(profileIDStr)
		if err != nil || profileID <= 0 {
			s.renderError(w, http.StatusBadRequest, "Profile ID must be a positive number.")
			return
		}
		if err := s.greythr.SaveSession(host, accessToken, profileID); err != nil {
			s.renderError(w, http.StatusInternalServerError, "Could not save greytHR session: "+err.Error())
			return
		}
		http.Redirect(w, r, "/greythr?toast=Connected&variant=success", http.StatusSeeOther)
		return
	}

	// Partial or no params — show the form with whatever prefill values we have.
	data := greythrPageData{
		pageData: pageData{Title: "greytHR", Active: "greythr"},
		Prefill: greythrPrefill{
			Host:        host,
			AccessToken: accessToken,
			ProfileID:   profileIDStr,
		},
	}
	s.render(w, "greythr", data)
}

func (s *Server) handleGreytHRConnect(w http.ResponseWriter, r *http.Request) {
	host := strings.TrimSpace(r.FormValue("host"))
	accessToken := strings.TrimSpace(r.FormValue("access_token"))
	profileIDStr := strings.TrimSpace(r.FormValue("profile_id"))

	if host == "" || accessToken == "" || profileIDStr == "" {
		s.renderError(w, http.StatusBadRequest, "Host, access token, and profile ID are all required.")
		return
	}

	profileID, err := strconv.Atoi(profileIDStr)
	if err != nil || profileID <= 0 {
		s.renderError(w, http.StatusBadRequest, "Profile ID must be a positive number.")
		return
	}

	if err := s.greythr.SaveSession(host, accessToken, profileID); err != nil {
		s.renderError(w, http.StatusInternalServerError, "Could not save greytHR session: "+err.Error())
		return
	}

	http.Redirect(w, r, "/greythr?toast=Connected&variant=success", http.StatusSeeOther)
}

func (s *Server) handleGreytHRDisconnect(w http.ResponseWriter, r *http.Request) {
	if err := s.greythr.Disconnect(); err != nil {
		s.renderError(w, http.StatusInternalServerError, "Could not disconnect: "+err.Error())
		return
	}
	http.Redirect(w, r, "/greythr?toast=Disconnected&variant=info", http.StatusSeeOther)
}

func (s *Server) handleGreytHRFetch(w http.ResponseWriter, r *http.Request) {
	if !s.greythr.Connected() {
		http.Redirect(w, r, "/greythr?toast=Not connected&variant=warning", http.StatusSeeOther)
		return
	}

	months, err := s.greythr.ListPayslipMonths(r.Context())
	if err != nil {
		if errors.Is(err, greythr.ErrNotConnected) {
			http.Redirect(w, r, "/greythr?toast=Session expired, please reconnect&variant=warning", http.StatusSeeOther)
			return
		}
		s.renderError(w, http.StatusInternalServerError, "Could not list payslip months: "+err.Error())
		return
	}

	// Filter out months that already have a payslip in the DB.
	var toFetch []greythr.PayslipMonth
	for _, m := range months.Months {
		if !m.Released {
			continue
		}
		pm, py := greythr.ParseFromDate(m.FromDate)
		if pm == 0 || py == 0 {
			continue
		}
		existing, err := s.store.ListPayslips(r.Context(), store.Filter{
			MonthFrom: pm, MonthTo: pm, YearFrom: py, YearTo: py, Limit: 1,
		})
		if err != nil {
			log.Printf("greythr fetch: dedup check for %s: %v", m.Month, err)
			continue
		}
		if len(existing) > 0 {
			continue
		}
		toFetch = append(toFetch, m)
	}

	if len(toFetch) == 0 {
		http.Redirect(w, r, "/greythr?toast=All payslips already fetched&variant=info", http.StatusSeeOther)
		return
	}

	batchID := newBatchID()
	if err := s.store.CreateBatch(r.Context(), batchID, len(toFetch)); err != nil {
		s.renderError(w, http.StatusInternalServerError, "Could not create batch: "+err.Error())
		return
	}

	go s.processGreytHRFetch(batchID, toFetch)

	http.Redirect(w, r, "/upload/batch/"+batchID, http.StatusSeeOther)
}

// processGreytHRFetch runs in a background goroutine. For each month it:
// 1. Fetches the payslip JSON data from greytHR's /published endpoint
// 2. Maps the JSON to a store.Payslip (no LLM extraction needed)
// 3. Downloads the PDF for archival
// 4. Saves the payslip as pending_review
//
// This is the JSON-first path — the LLM vision pipeline is skipped entirely
// because greytHR's API returns structured data. The PDF is only for the
// review UI's inline viewer.
func (s *Server) processGreytHRFetch(batchID string, months []greythr.PayslipMonth) {
	ctx := context.Background()

	canonicals, err := s.store.ListCanonicals(ctx)
	if err != nil {
		log.Printf("greythr batch %s: load canonicals: %v", batchID, err)
		return
	}

	sess, err := s.greythr.LoadSession()
	if err != nil {
		log.Printf("greythr batch %s: load session: %v", batchID, err)
		return
	}

	// Fetch employee info once (designation + employee number) for all payslips.
	// Best-effort: if it fails we continue with empty values.
	info, empErr := s.greythr.FetchEmployeeInfo(ctx)
	if empErr != nil {
		log.Printf("greythr batch %s: fetch employee info (continuing without): %v", batchID, empErr)
	}

	// YTD cache: FY year → summary. Lazy-fetched per FY (one API call covers
	// all months in the financial year).
	ytdCache := make(map[int]*greythr.YTDSummary)

	for _, m := range months {
		_ = s.store.UpdateBatchProgress(ctx, batchID, m.Month, "fetching")

		payMonth, payYear := greythr.ParseFromDate(m.FromDate)
		fyYear := greythr.FYYearFor(payMonth, payYear)
		if _, ok := ytdCache[fyYear]; !ok {
			ytd, err := s.greythr.FetchYTDSummary(ctx, fyYear)
			if err != nil {
				log.Printf("greythr batch %s: fetch YTD for FY %d (continuing without): %v", batchID, fyYear, err)
			} else {
				ytdCache[fyYear] = ytd
			}
		}

		p, err := s.fetchAndMapOne(ctx, m, sess.Host, info, ytdCache, canonicals)
		if err != nil {
			log.Printf("greythr batch %s: %s: %v", batchID, m.Month, err)
			failed := store.Payslip{
				EmployerName: m.Month,
				Status:       store.StatusFailed,
				BatchID:      batchID,
				ErrorMessage: err.Error(),
			}
			if saveErr := s.store.SavePayslip(ctx, &failed); saveErr != nil {
				log.Printf("greythr batch %s: save failed %s: %v", batchID, m.Month, saveErr)
			}
			_ = s.store.IncrementBatchFailed(ctx, batchID)
			continue
		}

		p.BatchID = batchID
		p.Status = store.StatusPendingReview
		if len(p.Components) == 0 {
			log.Printf("greythr batch %s: %s: no components (empty payslip), skipping", batchID, m.Month)
			_ = s.store.IncrementBatchProcessed(ctx, batchID)
			continue
		}
		if err := s.store.SavePayslip(ctx, &p); err != nil {
			log.Printf("greythr batch %s: save %s: %v", batchID, m.Month, err)
			_ = s.store.IncrementBatchFailed(ctx, batchID)
			continue
		}
		_ = s.store.IncrementBatchProcessed(ctx, batchID)
	}
	_ = s.store.UpdateBatchProgress(ctx, batchID, "", "")
}

// fetchAndMapOne fetches JSON data + PDF for a single month and returns a
// store.Payslip ready for SavePayslip. The PDF is saved via pdfstore for
// archival; the payslip data comes from the JSON endpoint (no LLM needed).
// Employee metadata (info) is stamped onto the payslip after mapping.
func (s *Server) fetchAndMapOne(ctx context.Context, m greythr.PayslipMonth, host string, info greythr.EmployeeInfo, ytdCache map[int]*greythr.YTDSummary, canonicals []store.Canonical) (store.Payslip, error) {
	data, err := s.greythr.FetchPayslipData(ctx, m.ID)
	if err != nil {
		return store.Payslip{}, fmt.Errorf("fetch data: %w", err)
	}

	payMonth, payYear := greythr.ParseFromDate(m.FromDate)
	fyYear := greythr.FYYearFor(payMonth, payYear)
	var ytd map[string]float64
	if summary := ytdCache[fyYear]; summary != nil {
		ytd = summary.YTDForMonth(payMonth)
	}

	p, err := greythr.MapToPayslip(data, m, host, canonicals, ytd)
	if err != nil {
		return store.Payslip{}, fmt.Errorf("map: %w", err)
	}
	p.Designation = info.Designation
	p.EmployeeID = info.EmployeeNo

	// Download PDF for archival (best-effort — don't fail if it errors).
	pdfBody, filename, err := s.greythr.DownloadPayslipPDF(ctx, m.ID)
	if err != nil {
		log.Printf("greythr: download PDF for %s (continuing without): %v", m.Month, err)
		return p, nil
	}
	defer pdfBody.Close()

	relPath, err := s.pdfs.Save(filename, pdfBody)
	if err != nil {
		log.Printf("greythr: save PDF for %s (continuing without): %v", m.Month, err)
		return p, nil
	}
	p.RawPDFPath = relPath
	return p, nil
}

// --- JSON API for AJAX ---

// greythrMonthsJSON is the JSON shape for the months list API.
type greythrMonthsJSON struct {
	Connected bool                    `json:"connected"`
	Error     string                  `json:"error,omitempty"`
	Months    []greythr.PayslipMonth  `json:"months,omitempty"`
}

func (s *Server) handleGreytHRMonthsAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if !s.greythr.Connected() {
		json.NewEncoder(w).Encode(greythrMonthsJSON{Connected: false})
		return
	}

	months, err := s.greythr.ListPayslipMonths(r.Context())
	if err != nil {
		json.NewEncoder(w).Encode(greythrMonthsJSON{Connected: false, Error: err.Error()})
		return
	}

	json.NewEncoder(w).Encode(greythrMonthsJSON{Connected: true, Months: months.Months})
}

// handleGreytHRExtension serves the Mozilla-signed XPI for one-click install
// in Firefox. The XPI is pre-signed via AMO (web-ext sign --channel=unlisted)
// and embedded in the binary. Firefox triggers the install prompt when served
// with Content-Type: application/x-xpinstall.
func (s *Server) handleGreytHRExtension(w http.ResponseWriter, r *http.Request) {
	data, err := contentFS.ReadFile("extension/cresto-greythr-connector.xpi")
	if err != nil {
		http.Error(w, "signed extension not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-xpinstall")
	w.Header().Set("Content-Disposition", `attachment; filename="cresto-greythr-connector.xpi"`)
	w.Write(data)
}

// handleExtensionZip serves the extension as a ZIP for Chrome users. They
// download, extract, and load it unpacked via chrome://extensions.
func (s *Server) handleExtensionZip(w http.ResponseWriter, r *http.Request) {
	data, err := contentFS.ReadFile("extension/cresto-connector-chrome.zip")
	if err != nil {
		http.Error(w, "extension zip not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="cresto-connector-chrome.zip"`)
	w.Write(data)
}
