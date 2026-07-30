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

	TotalSalary    float64
	TotalSavings   float64
	TotalFD        float64
	TotalInterest  float64
	TotalDividends float64
	TotalAISTDS    float64
	TotalSTCG      float64
	TotalLTCG      float64
	CGBuy          float64
	CGSell         float64

	TaxBreakdown tax.Breakdown
	RefundDue    float64
	HasRefund    bool
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
		v.RefundDue = v.TotalAISTDS - v.TaxBreakdown.TotalTaxLiability
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
// to disk, and stores the import record. Redirects to /tax on success.
func (s *Server) handleTaxAISUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	prof, err := s.store.GetTaxpayerProfile(ctx)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.renderError(w, http.StatusBadRequest,
				"Set up your Tax Profile first (Settings → Tax Profile) — Cresto needs your PAN and date of birth to decrypt the AIS file.")
			return
		}
		s.renderError(w, http.StatusInternalServerError, "Could not load tax profile: "+err.Error())
		return
	}

	if err := r.ParseMultipartForm(10 << 20); err != nil {
		s.renderError(w, http.StatusBadRequest, "Could not read upload: "+err.Error())
		return
	}

	file, _, err := r.FormFile("ais")
	if err != nil {
		s.renderError(w, http.StatusBadRequest, "No AIS file provided.")
		return
	}
	defer file.Close()

	raw, err := io.ReadAll(file)
	if err != nil {
		s.renderError(w, http.StatusBadRequest, "Could not read uploaded file: "+err.Error())
		return
	}

	decrypted, err := ais.Decrypt(string(raw), prof.PAN, prof.DOB)
	if err != nil {
		s.renderError(w, http.StatusBadRequest,
			"Could not decrypt AIS file: "+err.Error()+". Check that your PAN and DOB in Settings match your income tax portal account.")
		return
	}

	parsed, err := ais.Parse(decrypted)
	if err != nil {
		s.renderError(w, http.StatusBadRequest, "Could not parse AIS JSON: "+err.Error())
		return
	}

	fyStart := ais.FYStartYear(parsed.FY)
	if fyStart == 0 {
		s.renderError(w, http.StatusBadRequest, "AIS JSON does not contain a financial year in its header.")
		return
	}

	aisDir := filepath.Join(s.cfg.DataDir, "ais")
	if err := os.MkdirAll(aisDir, 0o700); err != nil {
		s.renderError(w, http.StatusInternalServerError, "Could not create AIS storage directory: "+err.Error())
		return
	}

	savePath := filepath.Join(aisDir, fmt.Sprintf("fy%d.json", fyStart))
	if err := os.WriteFile(savePath, decrypted, 0o600); err != nil {
		s.renderError(w, http.StatusInternalServerError, "Could not save AIS file: "+err.Error())
		return
	}

	if err := s.store.SaveAISImport(ctx, fyStart, savePath); err != nil {
		s.renderError(w, http.StatusInternalServerError, "Could not record AIS import: "+err.Error())
		return
	}

	http.Redirect(w, r, "/tax?toast=AIS imported successfully&variant=success", http.StatusSeeOther)
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
		s.renderError(w, http.StatusBadRequest, "Import your AIS first — Cresto needs the FY from AIS before importing Kite Console data.")
		return
	}
	fyStartYear := imports[0].FYStartYear

	if err := r.ParseMultipartForm(10 << 20); err != nil {
		s.renderError(w, http.StatusBadRequest, "Could not read upload: "+err.Error())
		return
	}

	file, _, err := r.FormFile("kite")
	if err != nil {
		s.renderError(w, http.StatusBadRequest, "No Kite Console file provided.")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		s.renderError(w, http.StatusBadRequest, "Could not read uploaded file: "+err.Error())
		return
	}

	summary, err := kiteconsole.Parse(data)
	if err != nil {
		s.renderError(w, http.StatusBadRequest, "Could not parse Kite Console XLSX: "+err.Error())
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
		s.renderError(w, http.StatusInternalServerError, "Could not save trades: "+err.Error())
		return
	}

	http.Redirect(w, r, "/tax?toast=Kite Console imported&variant=success", http.StatusSeeOther)
}
