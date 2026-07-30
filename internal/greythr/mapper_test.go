package greythr

import (
	"testing"

	"cresto/internal/store"
)

// testCanonicals builds a canonical slice with stable IDs for mapper tests.
func testCanonicals() []store.Canonical {
	names := []struct {
		name string
		cat  store.Category
	}{
		{"basic", store.CategoryEarning},
		{"hra", store.CategoryEarning},
		{"special_allowance", store.CategoryEarning},
		{"medical", store.CategoryEarning},
		{"other_earnings", store.CategoryEarning},
		{"epf", store.CategoryDeduction},
		{"tds", store.CategoryDeduction},
		{"other_deductions", store.CategoryDeduction},
	}
	out := make([]store.Canonical, len(names))
	for i, n := range names {
		out[i] = store.Canonical{ID: int64(i + 1), Name: n.name, Category: n.cat}
	}
	return out
}

func canonNameByID(cans []store.Canonical) map[int64]string {
	m := make(map[int64]string, len(cans))
	for _, c := range cans {
		m[c.ID] = c.Name
	}
	return m
}

func mkItem(name, parent, desc string, value float64, show bool) PayslipItem {
	return PayslipItem{
		Item:  PayslipItemDef{Name: name, Parent: parent, Description: desc, Show: show},
		Value: value,
	}
}

func TestMapToPayslip_FullPayslip(t *testing.T) {
	data := &PayslipData{Content: []PayslipItem{
		mkItem("BASIC", "INCOME", "Basic", 50000, true),
		mkItem("HRA", "INCOME", "House Rent Allowance", 20000, true),
		mkItem("SPECIAL_ALLOWANCE", "INCOME", "Special Allowance", 10000, true),
		mkItem("PF", "DEDUCT", "Provident Fund", -1800, true),
		mkItem("INCOME_TAX", "DEDUCT", "Income Tax", -5000, true),
		// Aggregate rows have empty parents so they set totals but don't
		// become components.
		mkItem("INCOME", "", "Earnings", 80000, true),
		mkItem("DEDUCT", "", "Deductions", -6800, true),
		mkItem("TOT_COST", "", "Net Pay", 73200, true),
		mkItem("EFFWORKDAYS", "", "Worked Days", 22, true),
		mkItem("DAYSINMONTH", "", "Days", 30, true),
	}}
	month := PayslipMonth{FromDate: "2026-06-01", Month: "Jun 2026"}
	cans := testCanonicals()

	p, err := MapToPayslip(data, month, "gyansys.greythr.com", cans, nil)
	if err != nil {
		t.Fatalf("MapToPayslip: %v", err)
	}

	if p.EmployerName != "Gyansys" {
		t.Errorf("EmployerName = %q, want %q", p.EmployerName, "Gyansys")
	}
	if p.PayPeriodMonth != 6 || p.PayPeriodYear != 2026 {
		t.Errorf("period = %d/%d, want 6/2026", p.PayPeriodMonth, p.PayPeriodYear)
	}
	if p.GrossSalary != 80000 {
		t.Errorf("GrossSalary = %v, want 80000", p.GrossSalary)
	}
	if p.TotalDeductions != 6800 {
		t.Errorf("TotalDeductions = %v, want 6800", p.TotalDeductions)
	}
	if p.NetPay != 73200 {
		t.Errorf("NetPay = %v, want 73200", p.NetPay)
	}
	if p.PayDays != 22 || p.TotalDays != 30 {
		t.Errorf("days = %d/%d, want 22/30", p.PayDays, p.TotalDays)
	}
	if p.Status != store.StatusPendingReview {
		t.Errorf("Status = %q, want %q", p.Status, store.StatusPendingReview)
	}

	// Employee metadata is stamped by the caller, not the mapper.
	if p.EmployeeID != "" || p.Designation != "" {
		t.Errorf("EmployeeID=%q Designation=%q, want both empty (caller's job)", p.EmployeeID, p.Designation)
	}

	wantComps := map[string]float64{
		"basic": 50000, "hra": 20000, "special_allowance": 10000,
		"epf": -1800, "tds": -5000,
	}
	names := canonNameByID(cans)
	if len(p.Components) != len(wantComps) {
		t.Fatalf("components = %d, want %d", len(p.Components), len(wantComps))
	}
	for _, c := range p.Components {
		nm := names[c.CanonicalID]
		want, ok := wantComps[nm]
		if !ok {
			t.Errorf("unexpected component %q", nm)
			continue
		}
		if c.Amount != want {
			t.Errorf("component %q amount = %v, want %v", nm, c.Amount, want)
		}
	}
}

func TestMapToPayslip_KeywordAndFallbackResolution(t *testing.T) {
	data := &PayslipData{Content: []PayslipItem{
		// Unknown name + no keyword match → other_earnings bucket.
		mkItem("WEIRD_EARNING", "INCOME", "Mystery Allowance", 3000, true),
		// Unknown name + no keyword match → other_deductions bucket.
		mkItem("WEIRD_DEDUCTION", "DEDUCT", "Mystery Deduction", -1000, true),
		// Unknown name but description matches the "medical" keyword → medical.
		mkItem("CUSTOM_MED", "INCOME", "Medical Reimbursement", 1250, true),
	}}
	cans := testCanonicals()

	p, err := MapToPayslip(data, PayslipMonth{FromDate: "2026-01-15"}, "acme.greythr.com", cans, nil)
	if err != nil {
		t.Fatalf("MapToPayslip: %v", err)
	}

	names := canonNameByID(cans)
	got := map[string]float64{}
	for _, c := range p.Components {
		got[names[c.CanonicalID]] = c.Amount
	}
	if got["other_earnings"] != 3000 {
		t.Errorf("other_earnings = %v, want 3000", got["other_earnings"])
	}
	if got["other_deductions"] != -1000 {
		t.Errorf("other_deductions = %v, want -1000", got["other_deductions"])
	}
	if got["medical"] != 1250 {
		t.Errorf("medical = %v, want 1250 (keyword fallback)", got["medical"])
	}
}

func TestMapToPayslip_SkipsZeroAndHidden(t *testing.T) {
	data := &PayslipData{Content: []PayslipItem{
		mkItem("BASIC", "INCOME", "Basic", 0, true),        // zero value → skipped
		mkItem("HRA", "INCOME", "House Rent", 5000, false), // hidden → skipped
		mkItem("SPECIAL_ALLOWANCE", "INCOME", "Special", 10000, true),
	}}
	cans := testCanonicals()

	p, err := MapToPayslip(data, PayslipMonth{FromDate: "2026-03-01"}, "x.greythr.com", cans, nil)
	if err != nil {
		t.Fatalf("MapToPayslip: %v", err)
	}
	if len(p.Components) != 1 {
		t.Fatalf("components = %d, want 1 (only special_allowance)", len(p.Components))
	}
	names := canonNameByID(cans)
	if names[p.Components[0].CanonicalID] != "special_allowance" {
		t.Errorf("component = %q, want special_allowance", names[p.Components[0].CanonicalID])
	}
}

func TestMapToPayslip_MissingFallbackCanonicalErrors(t *testing.T) {
	// No other_earnings bucket present → the fallback slug can't resolve.
	cans := []store.Canonical{
		{ID: 1, Name: "basic", Category: store.CategoryEarning},
	}
	data := &PayslipData{Content: []PayslipItem{
		mkItem("WEIRD_EARNING", "INCOME", "Mystery", 3000, true),
	}}
	_, err := MapToPayslip(data, PayslipMonth{FromDate: "2026-01-01"}, "x.greythr.com", cans, nil)
	if err == nil {
		t.Fatal("want error for missing fallback canonical, got nil")
	}
}

func TestMapToPayslip_SetsYTD(t *testing.T) {
	data := &PayslipData{Content: []PayslipItem{
		mkItem("BASIC", "INCOME", "Basic", 112500, true),
		mkItem("PF", "DEDUCT", "PF", -13500, true),
	}}
	ytd := map[string]float64{
		"BASIC": 337500,
		"PF":    -40500,
	}
	cans := testCanonicals()

	p, err := MapToPayslip(data, PayslipMonth{FromDate: "2026-06-01"}, "x.greythr.com", cans, ytd)
	if err != nil {
		t.Fatalf("MapToPayslip: %v", err)
	}

	names := canonNameByID(cans)
	for _, c := range p.Components {
		switch names[c.CanonicalID] {
		case "basic":
			if c.YTDAmt != 337500 {
				t.Errorf("basic YTD = %v, want 337500", c.YTDAmt)
			}
		case "epf":
			if c.YTDAmt != -40500 {
				t.Errorf("epf YTD = %v, want -40500", c.YTDAmt)
			}
		}
	}
}

func TestFYYearFor(t *testing.T) {
	cases := []struct {
		month, year, want int
	}{
		{4, 2026, 2026},  // April → same year
		{6, 2026, 2026},  // June → same year
		{12, 2026, 2026}, // Dec → same year
		{1, 2027, 2026},  // Jan → prior year
		{3, 2027, 2026},  // March → prior year
	}
	for _, tc := range cases {
		got := FYYearFor(tc.month, tc.year)
		if got != tc.want {
			t.Errorf("FYYearFor(%d, %d) = %d, want %d", tc.month, tc.year, got, tc.want)
		}
	}
}
