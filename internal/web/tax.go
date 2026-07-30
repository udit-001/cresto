package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"cresto/internal/ais"
	"cresto/internal/itr"
	"cresto/internal/kiteconsole"
	"cresto/internal/store"
	"cresto/internal/tax"
)

// taxView is the view model for the /tax page. Empty state (no AIS imported)
// shows an import wizard; populated state shows parsed AIS data + TDS
// reconciliation against Cresto payslips.
type taxView struct {
	HasAIS      bool
	HasKite     bool
	HasProfile  bool
	FY          string
	FYStartYear int
	ImportedAt  string

	Salaries        []ais.SalaryEntry
	SavingsInterest []ais.InterestEntry
	FDInterest      []ais.InterestEntry
	Dividends       []ais.DividendEntry
	TDSRecon        []TDSRecon
	Securities      []ais.SecuritySale
	AdvanceTax      []ais.AdvanceTaxEntry
	CGTrades        []store.CapitalGainsTrade

	TotalSalary     float64
	TotalSavings    float64
	TotalFD         float64
	TotalInterest   float64
	TotalDividends  float64
	TotalAISTDS     float64
	TotalAdvanceTax float64
	TotalSTCG       float64
	TotalLTCG       float64
	CGBuy           float64
	CGSell          float64

	TaxBreakdown tax.Breakdown
	RefundDue    float64
	HasRefund    bool

	// Identity (from TaxpayerProfile — surfaced on /tax, edited in Settings)
	PAN            string
	DeclarantName  string
	DOBYear        string // YYYY only, for display next to AIS upload

	// Refund destination (primary bank account)
	PrimaryBankName  string
	PrimaryBankMasked string // "••••" + last 4 digits
	HasPrimaryBank   bool

	// Export readiness flags
	HasVerificationDetails bool // DeclarantName + VerificationPlace both set
}

// TDSRecon is one row in the TDS reconciliation table: AIS TDS per deductor
// compared against Cresto's payslip-derived TDS for the same employer.
type TDSRecon struct {
	Deductor    string
	Section     string
	AISIncome   float64
	AISTDS      float64
	CrestoTDS   float64
	HasPayslips bool
	Status      string
	GapAmount   float64
}

// handleTax renders the /tax page. Empty state (no AIS imported) shows an
// import wizard with AIS download instructions; populated state shows
// parsed AIS data and TDS reconciliation.
func (s *Server) handleTax(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pending, _ := s.store.ListPendingReview(ctx)

	v := taxView{
		HasProfile: s.hasTaxProfile(ctx),
	}

	// Load identity + readiness flags from taxpayer profile.
	if prof, err := s.store.GetTaxpayerProfile(ctx); err == nil {
		v.PAN = prof.PAN
		v.DeclarantName = prof.DeclarantName
		if len(prof.DOB) == 8 {
			v.DOBYear = prof.DOB[4:]
		}
		v.HasVerificationDetails = prof.DeclarantName != "" && prof.VerificationPlace != ""
	}

	// Load primary bank for refund destination display.
	if accounts, err := s.store.ListBankAccounts(ctx); err == nil {
		for i := range accounts {
			if accounts[i].IsPrimary {
				v.HasPrimaryBank = true
				v.PrimaryBankName = accounts[i].BankName
				acct := accounts[i].AccountNumber
				if len(acct) >= 4 {
					v.PrimaryBankMasked = "\u2022\u2022\u2022\u2022" + acct[len(acct)-4:]
				} else {
					v.PrimaryBankMasked = acct
				}
				break
			}
		}
	}

	imports, _ := s.store.ListAISImports(ctx)
	if len(imports) > 0 {
		im := imports[0]
		v.HasAIS = true
		v.FYStartYear = im.FYStartYear
		v.FY = fmt.Sprintf("%d-%d", im.FYStartYear, (im.FYStartYear+1)%100)
		v.ImportedAt = im.ImportedAt

		raw, err := os.ReadFile(im.RawJSONPath)
		if err == nil {
			parsed, err := ais.Parse(raw)
			if err == nil {
				s.populateTaxView(ctx, &v, &parsed, im.FYStartYear)
			}
		}

		if has, _ := s.store.HasCapitalGains(ctx, im.FYStartYear); has {
			v.HasKite = true
			trades, _ := s.store.ListCapitalGainsTrades(ctx, im.FYStartYear)
			v.CGTrades = trades
			for _, tr := range trades {
				if strings.Contains(tr.Section, "Short Term") {
					v.TotalSTCG += tr.TaxableProfit
				} else if strings.Contains(tr.Section, "Long Term") {
					v.TotalLTCG += tr.TaxableProfit
				}
				v.CGBuy += tr.BuyValue
				v.CGSell += tr.SellValue
			}
		}

		v.TaxBreakdown = tax.Compute(tax.Input{
			GrossSalary:     v.TotalSalary,
			SavingsInterest: v.TotalSavings,
			FDInterest:      v.TotalFD,
			Dividends:       v.TotalDividends,
			STCG:            v.TotalSTCG,
			LTCG:            v.TotalLTCG,
		})
		v.RefundDue = (v.TotalAISTDS + v.TotalAdvanceTax) - v.TaxBreakdown.TotalTaxLiability
		v.HasRefund = v.RefundDue > 0
	}

	s.render(w, "tax", struct {
		pageData
		taxView
	}{
		pageData: pageData{Title: "Tax", PendingCount: len(pending), Active: "tax"},
		taxView:  v,
	})
}

// handleTaxAISUpload accepts an AIS JSON file upload, decrypts it using the
// stored taxpayer profile (PAN + DOB), parses it, saves the decrypted JSON
// to disk, and stores the import record. Returns JSON.
func (s *Server) handleTaxAISUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	prof, err := s.store.GetTaxpayerProfile(ctx)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			jsonError(w, http.StatusBadRequest, "Set up your Tax Profile first — Cresto needs your PAN and date of birth to decrypt the AIS file. <a href=\"/settings?tab=tax\" class=\"underline\">Go to Settings →</a>")
			return
		}
		jsonError(w, http.StatusInternalServerError, "Could not load tax profile: "+err.Error())
		return
	}

	if err := r.ParseMultipartForm(10 << 20); err != nil {
		jsonError(w, http.StatusBadRequest, "Could not read upload: "+err.Error())
		return
	}

	file, _, err := r.FormFile("ais")
	if err != nil {
		jsonError(w, http.StatusBadRequest, "No AIS file provided.")
		return
	}
	defer file.Close()

	raw, err := io.ReadAll(file)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "Could not read uploaded file: "+err.Error())
		return
	}

	decrypted, err := ais.Decrypt(string(raw), prof.PAN, prof.DOB)
	if err != nil {
		msg := "Could not decrypt AIS file. "
		if strings.Contains(err.Error(), "too short") || strings.Contains(err.Error(), "IV hex") || strings.Contains(err.Error(), "base64") {
			msg += "This doesn't look like an encrypted AIS JSON. Make sure you downloaded \"AIS (JSON)\", not the PDF."
		} else {
			msg += "Your PAN or date of birth doesn't match. <a href=\"/settings?tab=tax\" class=\"underline\">Edit tax profile →</a>"
		}
		jsonError(w, http.StatusBadRequest, msg)
		return
	}

	parsed, err := ais.Parse(decrypted)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "Could not parse AIS JSON: "+err.Error())
		return
	}

	fyStart := ais.FYStartYear(parsed.FY)
	if fyStart == 0 {
		jsonError(w, http.StatusBadRequest, "AIS JSON does not contain a financial year in its header.")
		return
	}

	aisDir := filepath.Join(s.cfg.DataDir, "ais")
	if err := os.MkdirAll(aisDir, 0o700); err != nil {
		jsonError(w, http.StatusInternalServerError, "Could not create AIS storage directory: "+err.Error())
		return
	}

	savePath := filepath.Join(aisDir, fmt.Sprintf("fy%d.json", fyStart))
	if err := os.WriteFile(savePath, decrypted, 0o600); err != nil {
		jsonError(w, http.StatusInternalServerError, "Could not save AIS file: "+err.Error())
		return
	}

	if err := s.store.SaveAISImport(ctx, fyStart, savePath); err != nil {
		jsonError(w, http.StatusInternalServerError, "Could not record AIS import: "+err.Error())
		return
	}

	jsonOK(w, "AIS imported successfully")
}

// populateTaxView fills the view model with parsed AIS data and computes
// TDS reconciliation against Cresto's payslip-derived TDS.
func (s *Server) populateTaxView(ctx context.Context, v *taxView, parsed *ais.ParsedAIS, fyStartYear int) {
	v.Salaries = parsed.Salaries
	v.SavingsInterest = parsed.SavingsInterest
	v.FDInterest = parsed.FDInterest
	v.Dividends = parsed.Dividends
	v.Securities = parsed.Securities
	v.AdvanceTax = parsed.AdvanceTax

	for _, sal := range parsed.Salaries {
		v.TotalSalary += sal.GrossSalary
	}
	for _, si := range parsed.SavingsInterest {
		v.TotalSavings += si.Amount
	}
	for _, fd := range parsed.FDInterest {
		v.TotalFD += fd.Amount
	}
	v.TotalInterest = v.TotalSavings + v.TotalFD
	for _, d := range parsed.Dividends {
		v.TotalDividends += d.Amount
	}
	for _, at := range parsed.AdvanceTax {
		v.TotalAdvanceTax += at.Total
	}

	employerTDS, _ := s.store.GetFYEmployerTDS(ctx, fyStartYear)
	tdsByEmployer := make(map[string]store.EmployerTDS)
	for _, e := range employerTDS {
		tdsByEmployer[e.EmployerName] = e
	}

	for _, tds := range parsed.TDS {
		v.TotalAISTDS += tds.TDS
		recon := TDSRecon{
			Deductor:  tds.Deductor,
			Section:   tds.Section,
			AISIncome: tds.Income,
			AISTDS:    tds.TDS,
		}

		if tds.Section == "192" {
			if emp, ok := matchEmployerTDS(tds.Deductor, tdsByEmployer); ok {
				recon.CrestoTDS = emp.TDS
				recon.HasPayslips = true
				recon.GapAmount = tds.TDS - emp.TDS
				if abs(recon.GapAmount) < 1 {
					recon.Status = "match"
				} else {
					recon.Status = "gap"
				}
			} else {
				recon.Status = "no_payslips"
			}
		} else {
			if tds.TDS == 0 {
				recon.Status = "match"
			} else {
				recon.Status = "no_payslips"
			}
		}

		v.TDSRecon = append(v.TDSRecon, recon)
	}
}

func (s *Server) hasTaxProfile(ctx context.Context) bool {
	_, err := s.store.GetTaxpayerProfile(ctx)
	return err == nil
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"error":%q}`, msg)
}

func jsonOK(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true,"message":%q}`, msg)
}

func matchEmployerTDS(aisName string, employers map[string]store.EmployerTDS) (store.EmployerTDS, bool) {
	aisUpper := strings.ToUpper(strings.TrimSpace(aisName))
	for name, emp := range employers {
		empUpper := strings.ToUpper(strings.TrimSpace(name))
		if aisUpper == empUpper {
			return emp, true
		}
		if strings.Contains(aisUpper, empUpper) || strings.Contains(empUpper, aisUpper) {
			return emp, true
		}
	}
	return store.EmployerTDS{}, false
}

// handleTaxKiteUpload accepts a Kite Console Tax P&L XLSX file, parses it,
// stores the trades, and redirects to /tax. Requires an AIS import to exist
// first (to determine the FY).
func (s *Server) handleTaxKiteUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	imports, _ := s.store.ListAISImports(ctx)
	if len(imports) == 0 {
		jsonError(w, http.StatusBadRequest, "Import your AIS first — Cresto needs the FY from AIS before importing Kite Console data.")
		return
	}
	fyStartYear := imports[0].FYStartYear

	if err := r.ParseMultipartForm(10 << 20); err != nil {
		jsonError(w, http.StatusBadRequest, "Could not read upload: "+err.Error())
		return
	}

	file, _, err := r.FormFile("kite")
	if err != nil {
		jsonError(w, http.StatusBadRequest, "No Kite Console file provided.")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "Could not read uploaded file: "+err.Error())
		return
	}

	summary, err := kiteconsole.Parse(data)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "Could not parse Kite Console XLSX: "+err.Error())
		return
	}

	trades := make([]store.CapitalGainsTrade, 0, len(summary.Trades))
	for _, t := range summary.Trades {
		trades = append(trades, store.CapitalGainsTrade{
			Section:       t.Section,
			Symbol:        t.Symbol,
			ISIN:          t.ISIN,
			EntryDate:     t.EntryDate,
			ExitDate:      t.ExitDate,
			Quantity:      t.Quantity,
			BuyValue:      t.BuyValue,
			SellValue:     t.SellValue,
			Profit:        t.Profit,
			TaxableProfit: t.TaxableProfit,
			FMV:           t.FMV,
			STT:           t.STT,
		})
	}

	if err := s.store.SaveCapitalGainsTrades(ctx, fyStartYear, trades); err != nil {
		jsonError(w, http.StatusInternalServerError, "Could not save trades: "+err.Error())
		return
	}

	jsonOK(w, "Kite Console imported")
}

// handleTaxExport generates an ITR-2 JSON for the current FY and returns it
// as a downloadable file. Requires AIS import + taxpayer profile.
func (s *Server) handleTaxExport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	imports, _ := s.store.ListAISImports(ctx)
	if len(imports) == 0 {
		s.renderError(w, http.StatusBadRequest, "Import your AIS first before exporting.")
		return
	}
	im := imports[0]

	prof, err := s.store.GetTaxpayerProfile(ctx)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.renderError(w, http.StatusBadRequest, "Set up your Tax Profile first (PAN, DOB, verification details).")
			return
		}
		s.renderError(w, http.StatusInternalServerError, "Could not load tax profile: "+err.Error())
		return
	}

	raw, err := os.ReadFile(im.RawJSONPath)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Could not read AIS data: "+err.Error())
		return
	}
	parsed, err := ais.Parse(raw)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Could not parse AIS data: "+err.Error())
		return
	}

	cgTrades, _ := s.store.ListCapitalGainsTrades(ctx, im.FYStartYear)
	bankAccounts, _ := s.store.ListBankAccounts(ctx)

	var totalSTCG, totalLTCG float64
	for _, tr := range cgTrades {
		if strings.Contains(tr.Section, "Short Term") {
			totalSTCG += tr.TaxableProfit
		} else if strings.Contains(tr.Section, "Long Term") {
			totalLTCG += tr.TaxableProfit
		}
	}

	var totalSalary, totalSavings, totalFD, totalDiv float64
	for _, sal := range parsed.Salaries {
		totalSalary += sal.GrossSalary
	}
	for _, si := range parsed.SavingsInterest {
		totalSavings += si.Amount
	}
	for _, fd := range parsed.FDInterest {
		totalFD += fd.Amount
	}
	for _, d := range parsed.Dividends {
		totalDiv += d.Amount
	}

	breakdown := tax.Compute(tax.Input{
		GrossSalary:     totalSalary,
		SavingsInterest: totalSavings,
		FDInterest:      totalFD,
		Dividends:       totalDiv,
		STCG:            totalSTCG,
		LTCG:            totalLTCG,
	})

	data, err := itr.Generate(itr.Input{
		Profile:      prof,
		BankAccounts: bankAccounts,
		AIS:          parsed,
		CGTrades:     cgTrades,
		TaxBreakdown: breakdown,
		FYStartYear:  im.FYStartYear,
	})
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Could not generate ITR-2 JSON: "+err.Error())
		return
	}

	filename := fmt.Sprintf("ITR-2_%s_AY2026-27.json", prof.PAN)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Write(data)
}

// handleTaxClearAIS removes the AIS import for the current FY, returning the
// page to the empty-state import wizard. Also deletes any Kite Console CG
// trades since they're meaningless without the AIS context.
func (s *Server) handleTaxClearAIS(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	imports, _ := s.store.ListAISImports(ctx)
	if len(imports) == 0 {
		http.Redirect(w, r, "/tax", http.StatusSeeOther)
		return
	}
	im := imports[0]
	if err := s.store.DeleteCapitalGainsTrades(ctx, im.FYStartYear); err != nil {
		s.renderError(w, http.StatusInternalServerError, "Could not clear capital gains: "+err.Error())
		return
	}
	if err := s.store.DeleteAISImport(ctx, im.FYStartYear); err != nil {
		s.renderError(w, http.StatusInternalServerError, "Could not clear AIS import: "+err.Error())
		return
	}
	os.Remove(im.RawJSONPath)
	http.Redirect(w, r, "/tax?toast=AIS+data+cleared&variant=info", http.StatusSeeOther)
}

// handleTaxClearKite removes all capital gains trades for the current FY.
func (s *Server) handleTaxClearKite(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	imports, _ := s.store.ListAISImports(ctx)
	if len(imports) == 0 {
		http.Redirect(w, r, "/tax", http.StatusSeeOther)
		return
	}
	if err := s.store.DeleteCapitalGainsTrades(ctx, imports[0].FYStartYear); err != nil {
		s.renderError(w, http.StatusInternalServerError, "Could not clear capital gains: "+err.Error())
		return
	}
	http.Redirect(w, r, "/tax?toast=Capital+gains+cleared&variant=info", http.StatusSeeOther)
}
