// Package cli — tax_redact.go: PII redaction layer for tax data.
//
// This is the single source of truth for what tax data is safe to expose
// through the CLI to agents. Every command that outputs tax data crosses
// this seam; no command touches store.* or ais.* fields directly for output.
//
// PII policy:
//   - PAN, DOB, DeclarantName, VerificationPlace → DROPPED (identity + keys)
//   - IFSC, AccountNumber        → DROPPED (banking PII)
//   - RawJSONPath                → DROPPED (filesystem path)
//   - AIS Section 192 deductor   → hashed (employer — same entity as payslips)
//   - AIS non-192 deductor       → KEPT (banks, companies — public institutions)
//   - TAN                        → DROPPED (reverse-lookupable to employer)
//   - BSRCode, Challan, CIN      → DROPPED (filing receipt IDs)
//   - ISIN                       → DROPPED (symbol already identifies security)
//   - bank_name, company_name    → KEPT (public institutions, analytical clarity)
//   - Symbol, SecurityName       → KEPT (public securities)
//   - all financials, dates, FY  → KEPT (the analytical payload)
package cli

import (
	"cresto/internal/ais"
	"cresto/internal/store"
	"cresto/internal/tax"
)

// --- tax summary (cresto tax) ---

type taxSummaryJSON struct {
	FY             string `json:"fy"`
	FYStartYear    int    `json:"fy_start_year"`
	AISImported    bool   `json:"ais_imported"`
	CGImported     bool   `json:"cg_imported"`
	ProfileSet     bool   `json:"profile_set"`
	PrimaryBankSet bool   `json:"primary_bank_set"`
	Form16OnFile   bool   `json:"form16_on_file"`
	ExportReady    bool   `json:"export_ready"`

	TotalSalary     float64 `json:"total_salary"`
	TotalSavings    float64 `json:"total_savings_interest"`
	TotalFD         float64 `json:"total_fd_interest"`
	TotalDividends  float64 `json:"total_dividends"`
	TotalAISTDS     float64 `json:"total_ais_tds"`
	TotalAdvanceTax float64 `json:"total_advance_tax"`
	TotalSTCG       float64 `json:"total_stcg"`
	TotalLTCG       float64 `json:"total_ltcg"`

	Breakdown          tax.Breakdown       `json:"breakdown"`
	RefundDue          float64             `json:"refund_due"`
	HasRefund          bool                `json:"has_refund"`
	ExcludedAdvanceTax []advanceTaxEntryJSON `json:"excluded_advance_tax,omitempty"`
}

// --- tax income (cresto tax income) ---

type taxIncomeJSON struct {
	Salaries        []salaryEntryJSON     `json:"salaries,omitempty"`
	SavingsInterest []interestEntryJSON   `json:"savings_interest,omitempty"`
	FDInterest      []interestEntryJSON   `json:"fd_interest,omitempty"`
	Dividends       []dividendEntryJSON   `json:"dividends,omitempty"`
	Securities      []securitySaleJSON    `json:"securities,omitempty"`
	AdvanceTax      []advanceTaxEntryJSON `json:"advance_tax,omitempty"`
}

type salaryEntryJSON struct {
	Employer    string  `json:"employer"` // hashed — same key as payslips
	GrossSalary float64 `json:"gross_salary"`
	TDS         float64 `json:"tds"`
}

type interestEntryJSON struct {
	Bank   string  `json:"bank"` // kept — public institution
	Amount float64 `json:"amount"`
}

type dividendEntryJSON struct {
	Company string  `json:"company"` // kept — public institution
	Amount  float64 `json:"amount"`
	TDS     float64 `json:"tds"`
}

type securitySaleJSON struct {
	SecurityName       string  `json:"security_name"` // kept — public security
	SalesConsideration float64 `json:"sales_consideration"`
	CostOfAcquisition  float64 `json:"cost_of_acquisition"`
	Type               string  `json:"type"`
}

type advanceTaxEntryJSON struct {
	FY        string  `json:"fy"`
	MinorHead string  `json:"minor_head"` // "Advance Tax" / "Self Assessment Tax"
	Total     float64 `json:"total"`
	Date      string  `json:"date"`
}

// --- capital gains (cresto tax capital-gains) ---

type capitalGainsJSON struct {
	Section       string  `json:"section"` // "Short Term" / "Long Term"
	Symbol        string  `json:"symbol"`  // kept — public security
	EntryDate     string  `json:"entry_date"`
	ExitDate      string  `json:"exit_date"`
	Quantity      float64 `json:"quantity"`
	BuyValue      float64 `json:"buy_value"`
	SellValue     float64 `json:"sell_value"`
	Profit        float64 `json:"profit"`
	TaxableProfit float64 `json:"taxable_profit"`
	FMV           float64 `json:"fmv"`
	STT           float64 `json:"stt"`
}

// --- TDS reconciliation (cresto tax tds) ---

type tdsReconJSON struct {
	Deductor    string  `json:"deductor"`     // hashed for Section 192, kept otherwise
	Section     string  `json:"section"`      // "192", "194A", etc.
	AISIncome   float64 `json:"ais_income"`
	AISTDS      float64 `json:"ais_tds"`
	CrestoTDS   float64 `json:"cresto_tds"`
	HasPayslips bool    `json:"has_payslips"`
	Status      string  `json:"status"` // "match", "gap", "no_payslips"
	GapAmount   float64 `json:"gap_amount"`
}

// --- redaction functions ---

// redactSalaryEntry converts an AIS salary entry to agent-safe JSON.
// canonicalEmployer is the payslip employer name if the fuzzy matcher
// resolved one (so the hash matches 'cresto payslips'); empty falls back
// to the AIS name.
func redactSalaryEntry(s ais.SalaryEntry, canonicalEmployer string) salaryEntryJSON {
	name := canonicalEmployer
	if name == "" {
		name = s.Employer
	}
	return salaryEntryJSON{
		Employer:    employerHash(name),
		GrossSalary: s.GrossSalary,
		TDS:         s.TDS,
	}
}

func redactInterestEntry(e ais.InterestEntry) interestEntryJSON {
	return interestEntryJSON{
		Bank:   e.Bank,
		Amount: e.Amount,
	}
}

func redactDividendEntry(d ais.DividendEntry) dividendEntryJSON {
	return dividendEntryJSON{
		Company: d.Company,
		Amount:  d.Amount,
		TDS:     d.TDS,
	}
}

func redactSecuritySale(s ais.SecuritySale) securitySaleJSON {
	return securitySaleJSON{
		SecurityName:       s.SecurityName,
		SalesConsideration: s.SalesConsideration,
		CostOfAcquisition:  s.CostOfAcquisition,
		Type:               s.Type,
	}
}

func redactAdvanceTaxEntry(a ais.AdvanceTaxEntry) advanceTaxEntryJSON {
	return advanceTaxEntryJSON{
		FY:        a.FY,
		MinorHead: a.MinorHead,
		Total:     a.Total,
		Date:      a.Date,
	}
}

func redactCapitalGainsTrade(t store.CapitalGainsTrade) capitalGainsJSON {
	return capitalGainsJSON{
		Section:       t.Section,
		Symbol:        t.Symbol,
		EntryDate:     t.EntryDate,
		ExitDate:      t.ExitDate,
		Quantity:      t.Quantity,
		BuyValue:      t.BuyValue,
		SellValue:     t.SellValue,
		Profit:        t.Profit,
		TaxableProfit: t.TaxableProfit,
		FMV:           t.FMV,
		STT:           t.STT,
	}
}

func redactCapitalGainsTrades(trades []store.CapitalGainsTrade) []capitalGainsJSON {
	out := make([]capitalGainsJSON, 0, len(trades))
	for _, t := range trades {
		out = append(out, redactCapitalGainsTrade(t))
	}
	return out
}

// redactDeductor hashes Section 192 deductors (employers — same hash as
// payslips so AIS, TDS, and payslip data correlate) and keeps non-192
// deductors (banks, dividend companies — public institutions).
// canonicalEmployer is the payslip employer name if the fuzzy matcher
// resolved one; empty falls back to the AIS deductor name.
func redactDeductor(section, deductor, canonicalEmployer string) string {
	if section == "192" {
		name := canonicalEmployer
		if name == "" {
			name = deductor
		}
		return employerHash(name)
	}
	return deductor
}
