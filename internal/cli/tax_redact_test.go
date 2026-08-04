package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"cresto/internal/ais"
	"cresto/internal/store"
)

func TestRedactSalaryEntryDropsPII(t *testing.T) {
	t.Parallel()
	s := ais.SalaryEntry{
		Employer:    "Acme Corp Pvt Ltd",
		TAN:         "MUMA12345B",
		GrossSalary: 200000,
		TDS:         20000,
	}
	out := redactSalaryEntry(s, "")
	b, _ := json.Marshal(out)
	str := string(b)

	dropped := []string{"Acme Corp", "MUMA12345B", "TAN"}
	for _, pii := range dropped {
		if strings.Contains(str, pii) {
			t.Errorf("PII %q leaked into redacted salary JSON: %s", pii, str)
		}
	}
}

func TestRedactSalaryEntryKeepsFinancials(t *testing.T) {
	t.Parallel()
	s := ais.SalaryEntry{
		Employer:    "Acme Corp",
		GrossSalary: 150000,
		TDS:         15000,
	}
	out := redactSalaryEntry(s, "")
	if out.GrossSalary != 150000 {
		t.Errorf("GrossSalary = %v, want 150000", out.GrossSalary)
	}
	if out.TDS != 15000 {
		t.Errorf("TDS = %v, want 15000", out.TDS)
	}
	if out.Employer != employerHash("Acme Corp") {
		t.Errorf("Employer = %q, want %q", out.Employer, employerHash("Acme Corp"))
	}
}

func TestRedactSalaryEntryUsesCanonicalName(t *testing.T) {
	t.Parallel()
	s := ais.SalaryEntry{
		Employer:    "ACME CORPORATION PRIVATE LIMITED",
		GrossSalary: 200000,
	}
	out := redactSalaryEntry(s, "Acme Corp")
	if out.Employer != employerHash("Acme Corp") {
		t.Errorf("Employer = %q, want hash of canonical %q", out.Employer, employerHash("Acme Corp"))
	}
	if out.Employer == employerHash(s.Employer) {
		t.Errorf("should not hash the AIS name when a canonical name is provided")
	}
}

func TestRedactInterestEntryKeepsBankName(t *testing.T) {
	t.Parallel()
	e := ais.InterestEntry{Bank: "HDFC BANK LTD", Amount: 4500}
	out := redactInterestEntry(e)
	if out.Bank != "HDFC BANK LTD" {
		t.Errorf("Bank = %q, want kept as-is", out.Bank)
	}
	if out.Amount != 4500 {
		t.Errorf("Amount = %v, want 4500", out.Amount)
	}
}

func TestRedactDividendEntryKeepsCompanyName(t *testing.T) {
	t.Parallel()
	d := ais.DividendEntry{Company: "Castrol India Ltd", Amount: 5000, TDS: 500}
	out := redactDividendEntry(d)
	if out.Company != "Castrol India Ltd" {
		t.Errorf("Company = %q, want kept as-is", out.Company)
	}
	if out.Amount != 5000 || out.TDS != 500 {
		t.Errorf("Amount/TDS = %v/%v, want 5000/500", out.Amount, out.TDS)
	}
}

func TestRedactAdvanceTaxDropsReceiptIDs(t *testing.T) {
	t.Parallel()
	a := ais.AdvanceTaxEntry{
		FY:        "2025-26",
		MajorHead: "Income Tax",
		MinorHead: "Advance Tax",
		Tax:       40000,
		Surcharge: 0,
		Cess:      1600,
		Total:     41600,
		BSRCode:   "0510123",
		Date:      "2025-09-15",
		Challan:   "CHALLAN123",
		CIN:       "CIN456",
	}
	out := redactAdvanceTaxEntry(a)
	b, _ := json.Marshal(out)
	str := string(b)

	dropped := []string{"BSRCode", "0510123", "CHALLAN123", "CIN456", "Income Tax"}
	for _, pii := range dropped {
		if strings.Contains(str, pii) {
			t.Errorf("PII/busywork %q leaked into redacted advance tax JSON: %s", pii, str)
		}
	}
	if out.Total != 41600 {
		t.Errorf("Total = %v, want 41600", out.Total)
	}
	if out.MinorHead != "Advance Tax" {
		t.Errorf("MinorHead = %q, want 'Advance Tax'", out.MinorHead)
	}
	if out.Date != "2025-09-15" {
		t.Errorf("Date = %q, want '2025-09-15'", out.Date)
	}
}

func TestRedactCapitalGainsTradeDropsPII(t *testing.T) {
	t.Parallel()
	tr := store.CapitalGainsTrade{
		ID:            99,
		FYStartYear:   2025,
		Section:       "Short Term",
		Symbol:        "RELIANCE",
		ISIN:          "INE002A01018",
		EntryDate:     "2025-04-01",
		ExitDate:      "2025-06-15",
		Quantity:      10,
		BuyValue:      25000,
		SellValue:     28000,
		Profit:        3000,
		TaxableProfit: 3000,
		STT:           28,
	}
	out := redactCapitalGainsTrade(tr)
	b, _ := json.Marshal(out)
	str := string(b)

	dropped := []string{"INE002A01018", "ISIN"}
	for _, pii := range dropped {
		if strings.Contains(str, pii) {
			t.Errorf("PII %q leaked into redacted CG JSON: %s", pii, str)
		}
	}
	if out.Symbol != "RELIANCE" {
		t.Errorf("Symbol = %q, want RELIANCE", out.Symbol)
	}
	if out.TaxableProfit != 3000 {
		t.Errorf("TaxableProfit = %v, want 3000", out.TaxableProfit)
	}
}

func TestRedactDeductorSection192(t *testing.T) {
	t.Parallel()
	employer := "Acme Corp Pvt Ltd"
	got := redactDeductor("192", employer, "")
	if got != employerHash(employer) {
		t.Errorf("Section 192 deductor = %q, want %q (hashed)", got, employerHash(employer))
	}
}

func TestRedactDeductorNon192(t *testing.T) {
	t.Parallel()
	bank := "HDFC BANK LTD"
	got := redactDeductor("194A", bank, "")
	if got != bank {
		t.Errorf("Non-192 deductor = %q, want %q (kept)", got, bank)
	}
}

func TestRedactDeductorUsesCanonicalName(t *testing.T) {
	t.Parallel()
	aisName := "ACME CORPORATION PRIVATE LIMITED"
	canonical := "Acme Corp"
	got := redactDeductor("192", aisName, canonical)
	if got != employerHash(canonical) {
		t.Errorf("Section 192 with canonical = %q, want %q", got, employerHash(canonical))
	}
}

func TestEmployerHashCorrelatesAcrossData(t *testing.T) {
	t.Parallel()
	employer := "Acme Corp Pvt Ltd"
	// With canonical resolution: both produce the payslip name's hash
	salary := redactSalaryEntry(ais.SalaryEntry{Employer: "ACME CORP PRIVATE LTD"}, employer)
	tds := redactDeductor("192", "ACME CORP PRIVATE LTD", employer)

	if salary.Employer != tds {
		t.Errorf("hash mismatch: salary=%q, tds=%q — same employer must produce same hash",
			salary.Employer, tds)
	}
}

func TestRedactCapitalGainsTradesEmpty(t *testing.T) {
	t.Parallel()
	out := redactCapitalGainsTrades(nil)
	if len(out) != 0 {
		t.Fatalf("expected empty slice, got %d", len(out))
	}
}
