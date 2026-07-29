package web

import (
	"reflect"
	"testing"

	"cresto/internal/store"
)

func TestFinancialYear_AprilStartsNewFY(t *testing.T) {
	tests := []struct {
		name         string
		month, year  int
		wantStart    int
		wantLabel    string
	}{
		{"Jan is in previous FY", 1, 2026, 2025, "FY 2025-26"},
		{"Feb is in previous FY", 2, 2026, 2025, "FY 2025-26"},
		{"Mar is in previous FY (last month)", 3, 2026, 2025, "FY 2025-26"},
		{"Apr starts new FY", 4, 2026, 2026, "FY 2026-27"},
		{"May in current FY", 5, 2026, 2026, "FY 2026-27"},
		{"Jun in current FY", 6, 2026, 2026, "FY 2026-27"},
		{"Jul in current FY", 7, 2026, 2026, "FY 2026-27"},
		{"Dec in current FY (last month)", 12, 2026, 2026, "FY 2026-27"},
		{"Jan next year is in previous FY", 1, 2027, 2026, "FY 2026-27"},
		{"Mar next year closes the FY", 3, 2027, 2026, "FY 2026-27"},
		{"Apr next year opens new FY", 4, 2027, 2027, "FY 2027-28"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStart, gotLabel := FinancialYear(tt.month, tt.year)
			if gotStart != tt.wantStart {
				t.Errorf("FinancialYear(%d, %d) startYear = %d, want %d", tt.month, tt.year, gotStart, tt.wantStart)
			}
			if gotLabel != tt.wantLabel {
				t.Errorf("FinancialYear(%d, %d) label = %q, want %q", tt.month, tt.year, gotLabel, tt.wantLabel)
			}
		})
	}
}

func TestFinancialYearLabelFormat(t *testing.T) {
	// Label is always "FY <start>-<start+1 mod 100>", with the second year
	// zero-padded to two digits when the rollover crosses a century. Most
	// realistic inputs just roll the last two digits; this test pins the
	// format string so we don't drift to "FY 26-27" or "FY 2026-2027".
	_, label := FinancialYear(7, 2026)
	if label != "FY 2026-27" {
		t.Errorf("label = %q, want exactly %q", label, "FY 2026-27")
	}
}

func TestBuildSparklinePoints(t *testing.T) {
	// All cases use a 6-month window. The timeline comes from
	// GetComponentTimeline: ordered oldest → newest, with (year, month)
	// pairs that may have gaps (skipped months) or be shorter than N.
	tests := []struct {
		name    string
		timeline []store.ComponentPoint
		months   int
		want     []float64
	}{
		{
			name:     "empty timeline returns empty slice",
			timeline: nil,
			months:   6,
			want:     []float64{},
		},
		{
			name: "full window passes through unchanged",
			timeline: []store.ComponentPoint{
				{PayPeriodYear: 2026, PayPeriodMonth: 2, Amount: 100},
				{PayPeriodYear: 2026, PayPeriodMonth: 3, Amount: 110},
				{PayPeriodYear: 2026, PayPeriodMonth: 4, Amount: 105},
				{PayPeriodYear: 2026, PayPeriodMonth: 5, Amount: 115},
				{PayPeriodYear: 2026, PayPeriodMonth: 6, Amount: 120},
				{PayPeriodYear: 2026, PayPeriodMonth: 7, Amount: 130},
			},
			months: 6,
			want:   []float64{100, 110, 105, 115, 120, 130},
		},
		{
			name: "short timeline is NOT left-padded (component is newer than window)",
			// A 3-month-old component shows three real points; the leading
			// months of the window simply don't exist for it, and zero-filling
			// them would lie about the component's age. The template renders
			// whatever points it gets.
			timeline: []store.ComponentPoint{
				{PayPeriodYear: 2026, PayPeriodMonth: 5, Amount: 115},
				{PayPeriodYear: 2026, PayPeriodMonth: 6, Amount: 120},
				{PayPeriodYear: 2026, PayPeriodMonth: 7, Amount: 130},
			},
			months: 6,
			want:   []float64{115, 120, 130},
		},
		{
			name: "timeline longer than window is truncated to last N",
			timeline: []store.ComponentPoint{
				{PayPeriodYear: 2025, PayPeriodMonth: 12, Amount: 90},
				{PayPeriodYear: 2026, PayPeriodMonth: 1, Amount: 95},
				{PayPeriodYear: 2026, PayPeriodMonth: 2, Amount: 100},
				{PayPeriodYear: 2026, PayPeriodMonth: 3, Amount: 110},
				{PayPeriodYear: 2026, PayPeriodMonth: 4, Amount: 105},
				{PayPeriodYear: 2026, PayPeriodMonth: 5, Amount: 115},
				{PayPeriodYear: 2026, PayPeriodMonth: 6, Amount: 120},
				{PayPeriodYear: 2026, PayPeriodMonth: 7, Amount: 130},
			},
			months: 6,
			// 8 points → last 6 = indices 2..7 → amounts 100,110,105,115,120,130.
			want: []float64{100, 110, 105, 115, 120, 130},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildSparklinePoints(tt.timeline, tt.months)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("BuildSparklinePoints got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsAnomalous(t *testing.T) {
	tests := []struct {
		name   string
		deltas []float64
		want   bool
	}{
		{"empty is not anomalous", nil, false},
		{"single point is not anomalous (no mean to compare against)", []float64{400}, false},
		{"two points: latest exactly 2x mean abs delta — boundary, not anomalous", []float64{100, 200}, false},
		// mean abs = (100+200)/2 = 150; 200 < 2*150 = 300 → not anomalous.
		{"latest > 2x mean abs delta is anomalous", []float64{100, 110, 105, 400}, true},
		// mean abs = (100+110+105+400)/4 = 178.75; 400 > 357.5 → anomalous.
		{"steady deltas are not anomalous", []float64{100, 110, 105, 120}, false},
		// mean abs = (100+110+105+120)/4 = 108.75; 120 < 217.5 → not anomalous.
		{"large negative delta can be anomalous too", []float64{-10, -12, -9, -100}, true},
		// mean abs = (10+12+9+100)/4 = 32.75; 100 > 65.5 → anomalous.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsAnomalous(tt.deltas); got != tt.want {
				t.Errorf("IsAnomalous(%v) = %v, want %v", tt.deltas, got, tt.want)
			}
		})
	}
}

func TestBuildYTDCumulative(t *testing.T) {
	// Build three cumulative series (net, tax, PF) across an FY timeline.
	// Uses the existing bucketDeductions logic for tax/PF bucketing so the
	// numbers match the dashboard's monthly breakdown.
	canons := map[int64]string{1: "basic", 14: "epf", 15: "tds", 16: "professional_tax"}
	// FY 2025-26: April 2025, May 2025, Jan 2026 (3 distinct months).
	// Two employers to make sure components from any payslip feed in.
	timeline := []store.Payslip{
		{
			PayPeriodMonth: 4, PayPeriodYear: 2025, NetPay: 40000,
			Components: []store.Component{
				{CanonicalID: 1, Amount: 50000, Category: store.CategoryEarning},
				{CanonicalID: 14, Amount: 2000, Category: store.CategoryDeduction}, // PF
				{CanonicalID: 15, Amount: 5000, Category: store.CategoryDeduction}, // TDS
				{CanonicalID: 16, Amount: 200, Category: store.CategoryDeduction},   // prof tax → tax
			},
		},
		{
			PayPeriodMonth: 5, PayPeriodYear: 2025, NetPay: 45000,
			Components: []store.Component{
				{CanonicalID: 1, Amount: 55000, Category: store.CategoryEarning},
				{CanonicalID: 14, Amount: 2000, Category: store.CategoryDeduction},
				{CanonicalID: 15, Amount: 6000, Category: store.CategoryDeduction},
				{CanonicalID: 16, Amount: 200, Category: store.CategoryDeduction},
			},
		},
		{
			PayPeriodMonth: 1, PayPeriodYear: 2026, NetPay: 42000,
			Components: []store.Component{
				{CanonicalID: 1, Amount: 52000, Category: store.CategoryEarning},
				{CanonicalID: 14, Amount: 2000, Category: store.CategoryDeduction},
				{CanonicalID: 15, Amount: 4000, Category: store.CategoryDeduction},
				{CanonicalID: 16, Amount: 200, Category: store.CategoryDeduction},
			},
		},
	}

	got := BuildYTDCumulative(timeline, canons)

	// Labels: one per month, in chronological order. April, May (2025),
	// January (2026). The label format must include the month short-name so
	// the chart x-axis is readable.
	wantLabels := []string{"Apr 2025", "May 2025", "Jan 2026"}
	if !reflect.DeepEqual(got.Labels, wantLabels) {
		t.Errorf("Labels = %v, want %v", got.Labels, wantLabels)
	}

	// Cumulative net: 40000 → 85000 → 127000.
	wantNet := []float64{40000, 85000, 127000}
	if !reflect.DeepEqual(got.NetSeries, wantNet) {
		t.Errorf("NetSeries = %v, want %v", got.NetSeries, wantNet)
	}

	// Cumulative tax: TDS + prof tax per month = 5200, 6200, 4200.
	// Cumulative: 5200 → 11400 → 15600.
	wantTax := []float64{5200, 11400, 15600}
	if !reflect.DeepEqual(got.TaxSeries, wantTax) {
		t.Errorf("TaxSeries = %v, want %v", got.TaxSeries, wantTax)
	}

	// Cumulative PF: 2000 per month → 2000, 4000, 6000.
	wantPF := []float64{2000, 4000, 6000}
	if !reflect.DeepEqual(got.PFSeries, wantPF) {
		t.Errorf("PFSeries = %v, want %v", got.PFSeries, wantPF)
	}
}

func TestBuildYTDCumulative_Empty(t *testing.T) {
	got := BuildYTDCumulative(nil, nil)
	if len(got.Labels) != 0 || len(got.NetSeries) != 0 {
		t.Errorf("empty input should yield empty series, got %+v", got)
	}
}

func TestBuildYTDCumulative_SinglePayslip(t *testing.T) {
	// One payslip: cumulative = monthly values, no accumulation across months.
	canons := map[int64]string{1: "basic"}
	timeline := []store.Payslip{
		{
			PayPeriodMonth: 4, PayPeriodYear: 2025, NetPay: 40000,
			Components: []store.Component{
				{CanonicalID: 1, Amount: 50000, Category: store.CategoryEarning},
			},
		},
	}
	got := BuildYTDCumulative(timeline, canons)
	if len(got.NetSeries) != 1 || got.NetSeries[0] != 40000 {
		t.Errorf("single payslip net series = %v, want [40000]", got.NetSeries)
	}
}

func TestBuildAnnualSummary(t *testing.T) {
	// Two canonicals (basic + tds), three FY payslips, two prev-FY payslips.
	// The builder should produce earnings-first, then deductions, sorted by
	// FY total descending within each category.
	canonBasic := store.Canonical{ID: 1, Name: "basic", Category: store.CategoryEarning}
	canonTDS := store.Canonical{ID: 15, Name: "tds", Category: store.CategoryDeduction}
	canonHRA := store.Canonical{ID: 2, Name: "hra", Category: store.CategoryEarning}
	canonicals := []store.Canonical{canonBasic, canonHRA, canonTDS}

	// FY 2025-26 payslips (April 2025 through March 2026). Three months of
	// data: April, May 2025, January 2026.
	fyPayslips := []store.Payslip{
		{
			PayPeriodMonth: 4, PayPeriodYear: 2025,
			Components: []store.Component{
				{CanonicalID: 1, Amount: 50000, Category: store.CategoryEarning},
				{CanonicalID: 2, Amount: 5000, Category: store.CategoryEarning},
				{CanonicalID: 15, Amount: 3000, Category: store.CategoryDeduction},
			},
		},
		{
			PayPeriodMonth: 5, PayPeriodYear: 2025,
			Components: []store.Component{
				{CanonicalID: 1, Amount: 50000, Category: store.CategoryEarning},
				{CanonicalID: 2, Amount: 5000, Category: store.CategoryEarning},
				{CanonicalID: 15, Amount: 3000, Category: store.CategoryDeduction},
			},
		},
		{
			PayPeriodMonth: 1, PayPeriodYear: 2026,
			Components: []store.Component{
				{CanonicalID: 1, Amount: 52000, Category: store.CategoryEarning},
				{CanonicalID: 15, Amount: 4000, Category: store.CategoryDeduction},
				// HRA absent this month — gap in the sparkline.
			},
		},
	}

	// Prev FY (2024-25) payslips: April 2024, May 2024.
	prevFYPayslips := []store.Payslip{
		{
			PayPeriodMonth: 4, PayPeriodYear: 2024,
			Components: []store.Component{
				{CanonicalID: 1, Amount: 48000, Category: store.CategoryEarning},
				{CanonicalID: 15, Amount: 2000, Category: store.CategoryDeduction},
			},
		},
		{
			PayPeriodMonth: 5, PayPeriodYear: 2024,
			Components: []store.Component{
				{CanonicalID: 1, Amount: 48000, Category: store.CategoryEarning},
				{CanonicalID: 15, Amount: 2000, Category: store.CategoryDeduction},
			},
		},
	}

	rows := BuildAnnualSummary(fyPayslips, prevFYPayslips, canonicals).Rows

	// Expect three rows: Basic (earning, total 152000) → HRA (earning, total 10000)
	// → TDS (deduction, total 10000). Earnings sorted desc; deductions after.
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	if rows[0].CanonicalID != 1 || rows[0].Name != "Basic" {
		t.Errorf("row 0: got %+v, want Basic first (highest earning total)", rows[0])
	}
	if rows[1].CanonicalID != 2 || rows[1].Name != "HRA" {
		t.Errorf("row 1: got %+v, want HRA second (lower earning total)", rows[1])
	}
	if rows[2].CanonicalID != 15 || rows[2].Name != "TDS" {
		t.Errorf("row 2: got %+v, want TDS third (deduction)", rows[2])
	}

	// Basic FY total: 50000+50000+52000 = 152000.
	if rows[0].FYTotal != 152000 {
		t.Errorf("Basic FY total = %v, want 152000", rows[0].FYTotal)
	}
	// Basic vs prev FY: prev total = 48000+48000 = 96000; delta = +56000.
	if rows[0].PrevFYTotal != 96000 {
		t.Errorf("Basic prev FY total = %v, want 96000", rows[0].PrevFYTotal)
	}
	if rows[0].Delta != 56000 {
		t.Errorf("Basic delta = %v, want 56000", rows[0].Delta)
	}
	if !rows[0].HasPrevFY {
		t.Errorf("Basic HasPrevFY should be true")
	}

	// HRA present in current FY but absent in prev → delta = FY total, HasPrevFY false.
	if rows[1].HasPrevFY {
		t.Errorf("HRA HasPrevFY should be false (not in prev FY data)")
	}
	if rows[1].PrevFYTotal != 0 {
		t.Errorf("HRA PrevFYTotal = %v, want 0", rows[1].PrevFYTotal)
	}
	if rows[1].Delta != 10000 {
		t.Errorf("HRA delta = %v, want 10000 (=FY total since prev is absent)", rows[1].Delta)
	}

	// Sparkline is 12 points (April = index 0, March = index 11).
	// Basic: Apr=50000, May=50000, Jan=52000, all others 0.
	wantSparkBasic := []float64{50000, 50000, 0, 0, 0, 0, 0, 0, 0, 52000, 0, 0}
	if !reflect.DeepEqual(rows[0].Sparkline, wantSparkBasic) {
		t.Errorf("Basic sparkline = %v, want %v", rows[0].Sparkline, wantSparkBasic)
	}
}

func TestBuildAnnualSummary_NoPrevFYData(t *testing.T) {
	// No prev-FY data at all: delta column should report HasPrevFY=false so
	// the template renders "—" rather than a misleading "0" delta.
	canonBasic := store.Canonical{ID: 1, Name: "basic", Category: store.CategoryEarning}
	canonicals := []store.Canonical{canonBasic}
	fyPayslips := []store.Payslip{
		{
			PayPeriodMonth: 4, PayPeriodYear: 2025,
			Components: []store.Component{
				{CanonicalID: 1, Amount: 50000, Category: store.CategoryEarning},
			},
		},
	}
	rows := BuildAnnualSummary(fyPayslips, nil, canonicals).Rows
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].HasPrevFY {
		t.Errorf("HasPrevFY should be false when prev FY has no data")
	}
}

func TestBuildAnnualSummary_ComponentAbsentInCurrentFY(t *testing.T) {
	// A canonical that's in the vocabulary but unused in this FY's payslips
	// must NOT appear in the summary — only used canonicals show up.
	canonUnused := store.Canonical{ID: 99, Name: "lwf", Category: store.CategoryDeduction}
	canonUsed := store.Canonical{ID: 1, Name: "basic", Category: store.CategoryEarning}
	canonicals := []store.Canonical{canonUsed, canonUnused}
	fyPayslips := []store.Payslip{
		{
			PayPeriodMonth: 4, PayPeriodYear: 2025,
			Components: []store.Component{
				{CanonicalID: 1, Amount: 50000, Category: store.CategoryEarning},
			},
		},
	}
	rows := BuildAnnualSummary(fyPayslips, nil, canonicals).Rows
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 (unused canonicals excluded)", len(rows))
	}
	if rows[0].CanonicalID != 1 {
		t.Errorf("row 0 canonical = %d, want 1 (basic, the only used)", rows[0].CanonicalID)
	}
}

func TestBuildSlopegraph(t *testing.T) {
	canons := map[int64]string{1: "basic", 2: "hra", 15: "tds", 99: "bonus"}

	// Latest payslip: basic=60000, hra=5000, tds=5000, bonus=10000 (new).
	// Prev FY payslip: basic=50000, hra=5000, tds=6000 (bonus absent → "new").
	latest := store.Payslip{
		Components: []store.Component{
			{CanonicalID: 1, Amount: 60000, Category: store.CategoryEarning},
			{CanonicalID: 2, Amount: 5000, Category: store.CategoryEarning},
			{CanonicalID: 15, Amount: 5000, Category: store.CategoryDeduction},
			{CanonicalID: 99, Amount: 10000, Category: store.CategoryEarning},
		},
	}
	prevFY := store.Payslip{
		Components: []store.Component{
			{CanonicalID: 1, Amount: 50000, Category: store.CategoryEarning},
			{CanonicalID: 2, Amount: 5000, Category: store.CategoryEarning},
			{CanonicalID: 15, Amount: 6000, Category: store.CategoryDeduction},
		},
	}

	rows := BuildSlopegraph(latest, prevFY, canons)

	// 4 rows: basic (delta +10000), bonus (delta +10000, "new"), tds (delta -1000),
	// hra (delta 0). Sorted by abs delta descending; ties keep insertion order.
	if len(rows) != 4 {
		t.Fatalf("got %d rows, want 4", len(rows))
	}
	// Basic: delta = +10000 (largest or tied for largest).
	if rows[0].CanonicalID != 1 {
		t.Errorf("row 0: got canonical %d, want 1 (basic, largest delta)", rows[0].CanonicalID)
	}
	if rows[0].Delta != 10000 {
		t.Errorf("basic delta = %v, want 10000", rows[0].Delta)
	}
	if rows[0].Label != "" {
		t.Errorf("basic label = %q, want empty (present in both years)", rows[0].Label)
	}
	// Bonus: new in latest, prev amount = 0, label "new".
	var bonusRow *slopegraphRow
	for i := range rows {
		if rows[i].CanonicalID == 99 {
			bonusRow = &rows[i]
			break
		}
	}
	if bonusRow == nil {
		t.Fatalf("bonus row missing")
	}
	if bonusRow.PrevFYAmount != 0 {
		t.Errorf("bonus prev amount = %v, want 0 (not in prev FY)", bonusRow.PrevFYAmount)
	}
	if bonusRow.LatestAmount != 10000 {
		t.Errorf("bonus latest amount = %v, want 10000", bonusRow.LatestAmount)
	}
	if bonusRow.Delta != 10000 {
		t.Errorf("bonus delta = %v, want 10000", bonusRow.Delta)
	}
	if bonusRow.Label != "new" {
		t.Errorf("bonus label = %q, want 'new'", bonusRow.Label)
	}
	// HRA: zero delta, should be sorted last (or near last).
	lastRow := rows[len(rows)-1]
	if lastRow.CanonicalID != 2 || lastRow.Delta != 0 {
		t.Errorf("last row: got canonical %d delta %v, want hra (2) with delta 0", lastRow.CanonicalID, lastRow.Delta)
	}
}

func TestBuildSlopegraph_GoneComponent(t *testing.T) {
	canons := map[int64]string{1: "basic", 50: "old_allowance"}
	// Latest: basic only. Prev FY: basic + old_allowance (absent in latest → "gone").
	latest := store.Payslip{
		Components: []store.Component{
			{CanonicalID: 1, Amount: 50000, Category: store.CategoryEarning},
		},
	}
	prevFY := store.Payslip{
		Components: []store.Component{
			{CanonicalID: 1, Amount: 50000, Category: store.CategoryEarning},
			{CanonicalID: 50, Amount: 3000, Category: store.CategoryEarning},
		},
	}
	rows := BuildSlopegraph(latest, prevFY, canons)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	// old_allowance: gone in latest, latest amount = 0, label "gone".
	var goneRow *slopegraphRow
	for i := range rows {
		if rows[i].CanonicalID == 50 {
			goneRow = &rows[i]
			break
		}
	}
	if goneRow == nil {
		t.Fatalf("gone component row missing")
	}
	if goneRow.LatestAmount != 0 {
		t.Errorf("gone component latest amount = %v, want 0", goneRow.LatestAmount)
	}
	if goneRow.PrevFYAmount != 3000 {
		t.Errorf("gone component prev amount = %v, want 3000", goneRow.PrevFYAmount)
	}
	if goneRow.Delta != -3000 {
		t.Errorf("gone component delta = %v, want -3000", goneRow.Delta)
	}
	if goneRow.Label != "gone" {
		t.Errorf("gone component label = %q, want 'gone'", goneRow.Label)
	}
}

func TestBuildSlopegraph_Empty(t *testing.T) {
	rows := BuildSlopegraph(store.Payslip{}, store.Payslip{}, nil)
	if len(rows) != 0 {
		t.Errorf("empty inputs: got %d rows, want 0", len(rows))
	}
}
