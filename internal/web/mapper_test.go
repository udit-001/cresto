package web

import (
	"testing"
	"time"

	"cresto/internal/llm"
	"cresto/internal/store"
)

// fakeCanonicals builds a canonical slice matching the seed set, with stable IDs.
// Used to test MapExtraction without a real database.
func fakeCanonicals() []store.Canonical {
	names := []struct {
		name string
		cat  store.Category
	}{
		{"basic", store.CategoryEarning},
		{"hra", store.CategoryEarning},
		{"da", store.CategoryEarning},
		{"special_allowance", store.CategoryEarning},
		{"bonus", store.CategoryEarning},
		{"arrears", store.CategoryEarning},
		{"term_insurance_earning", store.CategoryEarning},
		{"medical_insurance_earning", store.CategoryEarning},
		{"other_earnings", store.CategoryEarning},
		{"epf", store.CategoryDeduction},
		{"professional_tax", store.CategoryDeduction},
		{"tds", store.CategoryDeduction},
		{"lop", store.CategoryDeduction},
		{"term_insurance_deduction", store.CategoryDeduction},
		{"medical_insurance_deduction", store.CategoryDeduction},
		{"other_deductions", store.CategoryDeduction},
	}
	out := make([]store.Canonical, len(names))
	for i, n := range names {
		out[i] = store.Canonical{ID: int64(i + 1), Name: n.name, Category: n.cat}
	}
	return out
}

func mustMap(t *testing.T, ext *llm.Extraction) store.Payslip {
	t.Helper()
	p, err := MapExtraction(ext, nil, fakeCanonicals())
	if err != nil {
		t.Fatalf("MapExtraction: %v", err)
	}
	return p
}

func TestMapLabelToCanonical_Exact(t *testing.T) {
	cases := map[string]string{
		"Basic":            "basic",
		"Basic Pay":        "basic",
		"House Rent Allowance": "hra",
		"HRA":              "hra",
		"Dearness Allowance": "da",
		"Special Allowance": "special_allowance",
		"Performance Bonus": "bonus",
		"Provident Fund":   "epf",
		"EPF":              "epf",
		"Income Tax":       "tds",
		"TDS":              "tds",
		"Professional Tax": "professional_tax",
		"Loss of Pay":      "lop",
	}
	for label, want := range cases {
		if got := mapLabelToCanonical(label); got != want {
			t.Errorf("mapLabelToCanonical(%q) = %q, want %q", label, got, want)
		}
	}
}

func TestMapLabelToCanonical_NoMatch(t *testing.T) {
	if got := mapLabelToCanonical("Stock Options"); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
	if got := mapLabelToCanonical(""); got != "" {
		t.Errorf("expected empty for blank, got %q", got)
	}
}

func TestMapLabelToCanonical_LongerKeywordWins(t *testing.T) {
	// "House Rent Allowance" contains both "house rent allowance" (long) and
	// "rent allowance" (shorter). Both map to "hra" anyway, but this confirms
	// the longer keyword doesn't get shadowed by a shorter one from another canon.
	if got := mapLabelToCanonical("House Rent Allowance"); got != "hra" {
		t.Errorf("got %q, want hra", got)
	}
}

func TestMapLabelToCanonical_CaseInsensitive(t *testing.T) {
	if got := mapLabelToCanonical("BASIC PAY"); got != "basic" {
		t.Errorf("got %q, want basic", got)
	}
	if got := mapLabelToCanonical("Basic pay"); got != "basic" {
		t.Errorf("got %q, want basic", got)
	}
}

func TestMapLabelToCanonical_CollapsesWhitespace(t *testing.T) {
	if got := mapLabelToCanonical("House  Rent   Allowance"); got != "hra" {
		t.Errorf("got %q, want hra", got)
	}
}

func TestParsePayPeriod_FullMonthName(t *testing.T) {
	m, y, err := parsePayPeriod("July 2026")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if m != 7 || y != 2026 {
		t.Errorf("got (%d, %d), want (7, 2026)", m, y)
	}
}

func TestParsePayPeriod_Abbrev(t *testing.T) {
	m, y, err := parsePayPeriod("Jul 2026")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if m != 7 || y != 2026 {
		t.Errorf("got (%d, %d), want (7, 2026)", m, y)
	}
}

func TestParsePayPeriod_Numeric(t *testing.T) {
	cases := map[string][2]int{
		"07/2026": {7, 2026},
		"7/2026":  {7, 2026},
		"2026-07": {7, 2026},
		"2026-7":  {7, 2026},
	}
	for s, want := range cases {
		m, y, err := parsePayPeriod(s)
		if err != nil {
			t.Errorf("%q: err %v", s, err)
			continue
		}
		if m != want[0] || y != want[1] {
			t.Errorf("%q: got (%d, %d), want (%d, %d)", s, m, y, want[0], want[1])
		}
	}
}

func TestParsePayPeriod_WithNoise(t *testing.T) {
	// Free-form strings from LLMs often have extra context.
	m, y, err := parsePayPeriod("Pay Period: July 2026")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if m != 7 || y != 2026 {
		t.Errorf("got (%d, %d), want (7, 2026)", m, y)
	}
}

func TestParsePayPeriod_Empty(t *testing.T) {
	if _, _, err := parsePayPeriod(""); err == nil {
		t.Error("expected error for empty")
	}
}

func TestParsePayPeriod_NoYear(t *testing.T) {
	if _, _, err := parsePayPeriod("July"); err == nil {
		t.Error("expected error: no year")
	}
}

func TestParsePayPeriod_NoMonth(t *testing.T) {
	// Year present but no recognizable month.
	m, _, err := parsePayPeriod("Year 2026 Q3")
	if err == nil {
		t.Error("expected error: no month")
	}
	if m != 0 {
		t.Errorf("month should be 0 on error, got %d", m)
	}
}

func TestMapExtraction_Full(t *testing.T) {
	ext := &llm.Extraction{
		Company:   "Google",
		PayPeriod: "July 2026",
		Earnings: []llm.Component{
			{Label: "Basic Pay", Amount: 8000, YTD: 56000},
			{Label: "House Rent Allowance", Amount: 4000, YTD: 28000},
			{Label: "Stock Vesting", Amount: 1000, YTD: 6000}, // unknown → other_earnings
		},
		Deductions: []llm.Component{
			{Label: "Tax", Amount: 800, YTD: 5600}, // matches "tax" inside "income tax"? no — but "tds" keywords don't match "tax"
			{Label: "Provident Fund", Amount: 1200, YTD: 8400},
		},
		Totals: llm.Totals{
			Earnings:   13000,
			Deductions: 2000,
			NetPay:     11000,
		},
	}

	p := mustMap(t, ext)
	if p.EmployerName != "Google" {
		t.Errorf("employer = %q", p.EmployerName)
	}
	if p.PayPeriodMonth != 7 || p.PayPeriodYear != 2026 {
		t.Errorf("period = (%d, %d)", p.PayPeriodMonth, p.PayPeriodYear)
	}
	if p.GrossSalary != 13000 || p.TotalDeductions != 2000 || p.NetPay != 11000 {
		t.Errorf("totals = %+v", p)
	}
	if p.Status != store.StatusPendingReview {
		t.Errorf("status = %q, want pending_review", p.Status)
	}
	if len(p.Components) != 5 {
		t.Fatalf("got %d components, want 5", len(p.Components))
	}

	// Component 0: Basic Pay → basic canonical.
	c0 := p.Components[0]
	if c0.RawLabel != "Basic Pay" || c0.Category != store.CategoryEarning {
		t.Errorf("c0 = %+v", c0)
	}
	basicCanon := findCanonByName(t, "basic")
	if c0.CanonicalID != basicCanon.ID {
		t.Errorf("c0 canonical = %d, want %d (basic)", c0.CanonicalID, basicCanon.ID)
	}
	if c0.Amount != 8000 || c0.YTDAmt != 56000 {
		t.Errorf("c0 amounts = (%v, %v)", c0.Amount, c0.YTDAmt)
	}

	// Stock Vesting falls back to other_earnings.
	otherEarning := findCanonByName(t, "other_earnings")
	foundFallback := false
	for _, c := range p.Components {
		if c.CanonicalID == otherEarning.ID && c.RawLabel == "Stock Vesting" {
			foundFallback = true
		}
	}
	if !foundFallback {
		t.Error("Stock Vesting did not fall back to other_earnings")
	}

	// Provident Fund → epf.
	epfCanon := findCanonByName(t, "epf")
	foundEPF := false
	for _, c := range p.Components {
		if c.CanonicalID == epfCanon.ID {
			foundEPF = true
			if c.Category != store.CategoryDeduction {
				t.Errorf("epf category = %q", c.Category)
			}
		}
	}
	if !foundEPF {
		t.Error("Provident Fund did not map to epf")
	}

	// "Tax" alone doesn't match "income tax" or "tds" keywords — should fall back
	// to other_deductions (NOT tds). This documents the current behavior.
	otherDeduction := findCanonByName(t, "other_deductions")
	foundTaxFallback := false
	for _, c := range p.Components {
		if c.RawLabel == "Tax" {
			if c.CanonicalID == otherDeduction.ID {
				foundTaxFallback = true
			} else {
				t.Errorf("Tax mapped to %d, expected other_deductions (%d)", c.CanonicalID, otherDeduction.ID)
			}
		}
	}
	if !foundTaxFallback {
		t.Error("Tax did not fall back to other_deductions as expected")
	}
}

func TestMapExtraction_MissingCanonical(t *testing.T) {
	// If the DB is missing a required canonical (e.g. other_earnings not seeded),
	// MapExtraction returns an error rather than silently using ID 0.
	ext := &llm.Extraction{
		Company:   "X",
		PayPeriod: "2026-07",
		Earnings:  []llm.Component{{Label: "Unknown Thing", Amount: 100}},
	}
	// Empty canonicals slice — even the fallback is missing.
	if _, err := MapExtraction(ext, nil, nil); err == nil {
		t.Error("expected error when canonicals missing")
	}
}

func TestMapExtraction_Empty(t *testing.T) {
	ext := &llm.Extraction{Company: "Empty Co"}
	p := mustMap(t, ext)
	if p.EmployerName != "Empty Co" {
		t.Errorf("employer = %q", p.EmployerName)
	}
	if len(p.Components) != 0 {
		t.Errorf("got %d components, want 0", len(p.Components))
	}
	if p.PayPeriodMonth != 0 || p.PayPeriodYear != 0 {
		t.Errorf("period should be (0,0) for missing pay_period")
	}
}

func TestMapExtraction_PreservesYTD(t *testing.T) {
	ext := &llm.Extraction{
		Company:   "Co",
		PayPeriod: "Jan 2026",
		Earnings: []llm.Component{
			{Label: "Basic", Amount: 5000, YTD: 5000},
			{Label: "Basic", Amount: 5000, YTD: 10000}, // duplicate raw labels are fine
		},
	}
	p := mustMap(t, ext)
	if len(p.Components) != 2 {
		t.Fatalf("got %d components, want 2", len(p.Components))
	}
	if p.Components[0].YTDAmt != 5000 || p.Components[1].YTDAmt != 10000 {
		t.Errorf("YTD values: %v, %v", p.Components[0].YTDAmt, p.Components[1].YTDAmt)
	}
}

func findCanonByName(t *testing.T, name string) store.Canonical {
	t.Helper()
	for _, c := range fakeCanonicals() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("canonical %q not in fakeCanonicals", name)
	return store.Canonical{}
}

// silence unused import if time gets removed in future edits.
var _ = time.January

func TestMapExtraction_WithClassification_Arrears(t *testing.T) {
	// The classifier routes "Basic Arrears" and "HRA Arrears" to their parent
	// canonicals (basic, hra) instead of the generic "arrears" bucket — the
	// core fix for the longest-keyword-wins bug.
	ext := &llm.Extraction{
		Company:   "Co",
		PayPeriod: "Jul 2026",
		Earnings: []llm.Component{
			{Label: "Basic", Amount: 81000},
			{Label: "Basic Arrears", Amount: 5400},
			{Label: "HRA", Amount: 40500},
			{Label: "HRA Arrears", Amount: 2700},
		},
	}
	class := &llm.Classification{
		Earnings: []string{"basic", "basic", "hra", "hra"},
	}
	canons := fakeCanonicals()
	p, err := MapExtraction(ext, class, canons)
	if err != nil {
		t.Fatalf("MapExtraction: %v", err)
	}
	basic := findCanonByName(t, "basic")
	arrears := findCanonByName(t, "arrears")

	basicCount, arrearsCount := 0, 0
	for _, c := range p.Components {
		if c.CanonicalID == basic.ID {
			basicCount++
		}
		if c.CanonicalID == arrears.ID {
			arrearsCount++
		}
	}
	if basicCount != 2 {
		t.Errorf("expected 2 basic components (Basic + Basic Arrears), got %d", basicCount)
	}
	if arrearsCount != 0 {
		t.Errorf("expected 0 arrears components, got %d — classifier should route Basic Arrears to basic", arrearsCount)
	}
	if len(p.Components) != 4 {
		t.Errorf("expected 4 components, got %d", len(p.Components))
	}
}

func TestMapExtraction_WithClassification_TermInsurance(t *testing.T) {
	// Term Insurance Premium (earning) and Term Insurance Premium Deduction
	// (deduction) map to their respective term_insurance canonicals instead of
	// the generic other_* buckets.
	ext := &llm.Extraction{
		Company:   "Co",
		PayPeriod: "Jul 2026",
		Earnings: []llm.Component{
			{Label: "Basic", Amount: 81000},
			{Label: "Term Insurance Premium", Amount: 199},
		},
		Deductions: []llm.Component{
			{Label: "Term Insurance Premium Deduction", Amount: 199},
		},
	}
	class := &llm.Classification{
		Earnings:   []string{"basic", "term_insurance_earning"},
		Deductions: []string{"term_insurance_deduction"},
	}
	canons := fakeCanonicals()
	p, err := MapExtraction(ext, class, canons)
	if err != nil {
		t.Fatalf("MapExtraction: %v", err)
	}
	tiEarning := findCanonByName(t, "term_insurance_earning")
	tiDeduction := findCanonByName(t, "term_insurance_deduction")
	otherEarn := findCanonByName(t, "other_earnings")
	otherDed := findCanonByName(t, "other_deductions")

	for _, c := range p.Components {
		if c.RawLabel == "Term Insurance Premium" && c.CanonicalID != tiEarning.ID {
			t.Errorf("Term Insurance earning mapped to %d, want %d (term_insurance_earning)", c.CanonicalID, tiEarning.ID)
		}
		if c.RawLabel == "Term Insurance Premium Deduction" && c.CanonicalID != tiDeduction.ID {
			t.Errorf("Term Insurance deduction mapped to %d, want %d (term_insurance_deduction)", c.CanonicalID, tiDeduction.ID)
		}
		if c.CanonicalID == otherEarn.ID && c.RawLabel == "Term Insurance Premium" {
			t.Error("Term Insurance earning fell back to other_earnings — classifier slug not resolved")
		}
		if c.CanonicalID == otherDed.ID && c.RawLabel == "Term Insurance Premium Deduction" {
			t.Error("Term Insurance deduction fell back to other_deductions — classifier slug not resolved")
		}
	}
}

func TestMapExtraction_ClassificationLengthMismatch_FallsBack(t *testing.T) {
	// If the classifier returns wrong-length arrays, MapExtraction falls back
	// to keyword matching for every component rather than crashing.
	ext := &llm.Extraction{
		Company:   "Co",
		PayPeriod: "Jul 2026",
		Earnings: []llm.Component{
			{Label: "Basic", Amount: 5000},
			{Label: "HRA", Amount: 2000},
		},
	}
	class := &llm.Classification{
		Earnings: []string{"basic"}, // wrong length — 1 vs 2
	}
	p, err := MapExtraction(ext, class, fakeCanonicals())
	if err != nil {
		t.Fatalf("MapExtraction: %v", err)
	}
	basic := findCanonByName(t, "basic")
	hra := findCanonByName(t, "hra")
	if p.Components[0].CanonicalID != basic.ID {
		t.Errorf("Basic should still map via keyword fallback, got canonical %d", p.Components[0].CanonicalID)
	}
	if p.Components[1].CanonicalID != hra.ID {
		t.Errorf("HRA should map via keyword fallback, got canonical %d", p.Components[1].CanonicalID)
	}
}

func TestMapExtraction_ClassificationHallucinatedSlug_FallsBack(t *testing.T) {
	// If the LLM hallucinates a slug not in the canonical list, resolveSlug
	// falls back to other_* rather than failing the extraction.
	ext := &llm.Extraction{
		Company:   "Co",
		PayPeriod: "Jul 2026",
		Earnings: []llm.Component{
			{Label: "Mystery Component", Amount: 100},
		},
	}
	class := &llm.Classification{
		Earnings: []string{"nonexistent_slug"},
	}
	p, err := MapExtraction(ext, class, fakeCanonicals())
	if err != nil {
		t.Fatalf("MapExtraction: %v", err)
	}
	otherEarn := findCanonByName(t, "other_earnings")
	if p.Components[0].CanonicalID != otherEarn.ID {
		t.Errorf("hallucinated slug should fall back to other_earnings, got canonical %d", p.Components[0].CanonicalID)
	}
}
