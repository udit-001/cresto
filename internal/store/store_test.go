package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// newTestStore returns an open Store backed by a temp file and a cleanup function.
// Each test gets a fresh DB so migrations/seeding run every time.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func mustCreateCanonical(t *testing.T, st *Store, name string, cat Category) Canonical {
	t.Helper()
	c, err := st.CreateCanonical(context.Background(), name, cat)
	if err != nil {
		t.Fatalf("CreateCanonical(%s): %v", name, err)
	}
	return c
}

// samplePayslip returns a valid Payslip with two components. The canonical IDs
// must exist before SavePayslip; helper looks them up by name.
func samplePayslip(t *testing.T, st *Store, month, year int, employer string) Payslip {
	t.Helper()
	basic, err := st.FindCanonicalByName(context.Background(), "basic")
	if err != nil {
		t.Fatalf("find basic: %v", err)
	}
	tds, err := st.FindCanonicalByName(context.Background(), "tds")
	if err != nil {
		t.Fatalf("find tds: %v", err)
	}
	return Payslip{
		EmployerName:    employer,
		PayPeriodMonth:  month,
		PayPeriodYear:   year,
		EmployeeID:      "EMP42",
		Designation:     "Engineer",
		PayDays:         30,
		TotalDays:       30,
		GrossSalary:     50000,
		TotalDeductions: 5000,
		NetPay:          45000,
		RawPDFPath:      "/tmp/payslip-jul.pdf",
		Components: []Component{
			{CanonicalID: basic.ID, RawLabel: "Basic", Amount: 40000, YTDAmt: 280000, Category: CategoryEarning},
			{CanonicalID: tds.ID, RawLabel: "TDS", Amount: 5000, YTDAmt: 35000, Category: CategoryDeduction},
		},
	}
}

func TestOpen_SeedsCanonicals(t *testing.T) {
	st := newTestStore(t)
	cs, err := st.ListCanonicals(context.Background())
	if err != nil {
		t.Fatalf("ListCanonicals: %v", err)
	}
	if len(cs) != len(seedCanonicals) {
		t.Fatalf("got %d canonicals, want %d", len(cs), len(seedCanonicals))
	}
	// All seed rows must be non-user-created.
	for _, c := range cs {
		if c.IsUserCreated {
			t.Errorf("seed canonical %q marked user-created", c.Name)
		}
	}
	// Sanity: basic + tds must be present with correct categories.
	basic, err := st.FindCanonicalByName(context.Background(), "basic")
	if err != nil {
		t.Fatalf("find basic: %v", err)
	}
	if basic.Category != CategoryEarning {
		t.Errorf("basic category = %q, want earning", basic.Category)
	}
	tds, err := st.FindCanonicalByName(context.Background(), "tds")
	if err != nil {
		t.Fatalf("find tds: %v", err)
	}
	if tds.Category != CategoryDeduction {
		t.Errorf("tds category = %q, want deduction", tds.Category)
	}
}

func TestOpen_IsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	st1, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	st1.Close()
	st2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer st2.Close()
	cs, _ := st2.ListCanonicals(context.Background())
	if len(cs) != len(seedCanonicals) {
		t.Fatalf("after reopen, got %d canonicals, want %d (re-seeding double-counted?)", len(cs), len(seedCanonicals))
	}
}

func TestSavePayslip_Roundtrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	p := samplePayslip(t, st, 7, 2026, "Google")
	if err := st.SavePayslip(ctx, &p); err != nil {
		t.Fatalf("SavePayslip: %v", err)
	}
	if p.ID == 0 {
		t.Fatal("SavePayslip did not set p.ID")
	}
	if p.Status != StatusPendingReview {
		t.Errorf("status = %q, want pending_review", p.Status)
	}
	for i, c := range p.Components {
		if c.ID == 0 {
			t.Errorf("component %d: ID not set", i)
		}
		if c.PayslipID != p.ID {
			t.Errorf("component %d: PayslipID = %d, want %d", i, c.PayslipID, p.ID)
		}
	}

	got, err := st.GetPayslip(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetPayslip: %v", err)
	}
	if got.EmployerName != "Google" || got.PayPeriodMonth != 7 || got.PayPeriodYear != 2026 {
		t.Errorf("got = %+v", got)
	}
	if got.GrossSalary != 50000 || got.NetPay != 45000 {
		t.Errorf("totals: gross=%v net=%v", got.GrossSalary, got.NetPay)
	}
	if len(got.Components) != 2 {
		t.Fatalf("got %d components, want 2", len(got.Components))
	}
	// Components ordered by category then label: earning (Basic) before deduction (TDS).
	if got.Components[0].RawLabel != "Basic" || got.Components[1].RawLabel != "TDS" {
		t.Errorf("component order = %s, %s", got.Components[0].RawLabel, got.Components[1].RawLabel)
	}
	if got.Components[0].YTDAmt != 280000 {
		t.Errorf("Basic YTD = %v, want 280000", got.Components[0].YTDAmt)
	}
}

func TestGetPayslip_NotFound(t *testing.T) {
	st := newTestStore(t)
	_, err := st.GetPayslip(context.Background(), 9999)
	if err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestListPayslips_FilterByStatus(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	// Two pending + one confirmed.
	p1 := samplePayslip(t, st, 6, 2026, "Google")
	st.SavePayslip(ctx, &p1)
	p2 := samplePayslip(t, st, 7, 2026, "Google")
	st.SavePayslip(ctx, &p2)
	p3 := samplePayslip(t, st, 5, 2026, "Google")
	st.SavePayslip(ctx, &p3)
	if err := st.ConfirmPayslip(ctx, p3.ID); err != nil {
		t.Fatalf("ConfirmPayslip: %v", err)
	}

	pending, err := st.ListPendingReview(ctx)
	if err != nil {
		t.Fatalf("ListPendingReview: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("got %d pending, want 2", len(pending))
	}
	for _, p := range pending {
		if p.Status != StatusPendingReview {
			t.Errorf("pending list returned status %q", p.Status)
		}
	}

	confirmed, err := st.ListPayslips(ctx, Filter{Status: StatusConfirmed})
	if err != nil {
		t.Fatalf("ListPayslips confirmed: %v", err)
	}
	if len(confirmed) != 1 {
		t.Fatalf("got %d confirmed, want 1", len(confirmed))
	}
	if confirmed[0].ID != p3.ID {
		t.Errorf("confirmed id = %d, want %d", confirmed[0].ID, p3.ID)
	}
}

func TestListPayslips_NewestFirst(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	for _, m := range []int{3, 1, 2} {
		p := samplePayslip(t, st, m, 2026, "Google")
		if err := st.SavePayslip(ctx, &p); err != nil {
			t.Fatalf("SavePayslip: %v", err)
		}
	}
	ps, err := st.ListPayslips(ctx, Filter{})
	if err != nil {
		t.Fatalf("ListPayslips: %v", err)
	}
	if len(ps) != 3 {
		t.Fatalf("got %d, want 3", len(ps))
	}
	if ps[0].PayPeriodMonth != 3 || ps[2].PayPeriodMonth != 1 {
		t.Errorf("order: %d, %d, %d (want 3,2,1)", ps[0].PayPeriodMonth, ps[1].PayPeriodMonth, ps[2].PayPeriodMonth)
	}
}

func TestListPayslips_FilterByEmployerAndDate(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	empA := "Acme"
	empB := "Beta"

	for _, p := range []Payslip{
		{EmployerName: empA, PayPeriodMonth: 1, PayPeriodYear: 2025},
		{EmployerName: empA, PayPeriodMonth: 6, PayPeriodYear: 2025},
		{EmployerName: empA, PayPeriodMonth: 1, PayPeriodYear: 2026},
		{EmployerName: empB, PayPeriodMonth: 1, PayPeriodYear: 2026},
	} {
		p.Components = nil
		if err := st.SavePayslip(ctx, &p); err != nil {
			t.Fatalf("SavePayslip: %v", err)
		}
	}

	got, err := st.ListPayslips(ctx, Filter{Employer: empA})
	if err != nil {
		t.Fatalf("ListPayslips: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("employer filter: got %d, want 3", len(got))
	}

	got, err = st.ListPayslips(ctx, Filter{YearFrom: 2026, YearTo: 2026})
	if err != nil {
		t.Fatalf("ListPayslips: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("year filter: got %d, want 2", len(got))
	}
}

func TestConfirmPayslip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	p := samplePayslip(t, st, 7, 2026, "Google")
	if err := st.SavePayslip(ctx, &p); err != nil {
		t.Fatalf("SavePayslip: %v", err)
	}
	if err := st.ConfirmPayslip(ctx, p.ID); err != nil {
		t.Fatalf("ConfirmPayslip: %v", err)
	}
	got, _ := st.GetPayslip(ctx, p.ID)
	if got.Status != StatusConfirmed {
		t.Errorf("status = %q, want confirmed", got.Status)
	}
	if got.ConfirmedAt == "" {
		t.Error("ConfirmedAt empty after confirm")
	}
}

func TestConfirmPayslip_NotFound(t *testing.T) {
	st := newTestStore(t)
	if err := st.ConfirmPayslip(context.Background(), 9999); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestDeletePayslip_RemovesRowAndComponents(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	p := samplePayslip(t, st, 7, 2026, "Google")
	if err := st.SavePayslip(ctx, &p); err != nil {
		t.Fatalf("SavePayslip: %v", err)
	}

	if err := st.DeletePayslip(ctx, p.ID); err != nil {
		t.Fatalf("DeletePayslip: %v", err)
	}

	if _, err := st.GetPayslip(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetPayslip after delete: err = %v, want ErrNotFound", err)
	}

	// Components must also be gone — the schema's ON DELETE CASCADE handles this.
	var n int
	if err := st.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM payslip_components WHERE payslip_id = ?`, p.ID).Scan(&n); err != nil {
		t.Fatalf("count components: %v", err)
	}
	if n != 0 {
		t.Errorf("components after delete = %d, want 0 (cascade failed?)", n)
	}
}

func TestDeletePayslip_NotFound(t *testing.T) {
	st := newTestStore(t)
	if err := st.DeletePayslip(context.Background(), 9999); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeletePayslip unknown id: err = %v, want ErrNotFound", err)
	}
}

func TestDeletePayslipsByIDs(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	p1 := samplePayslip(t, st, 1, 2026, "Google")
	st.SavePayslip(ctx, &p1)
	p2 := samplePayslip(t, st, 2, 2026, "Google")
	st.SavePayslip(ctx, &p2)
	p3 := samplePayslip(t, st, 3, 2026, "Google")
	st.SavePayslip(ctx, &p3)

	deleted, err := st.DeletePayslipsByIDs(ctx, []int64{p1.ID, p3.ID})
	if err != nil {
		t.Fatalf("DeletePayslipsByIDs: %v", err)
	}
	if len(deleted) != 2 {
		t.Fatalf("deleted = %d, want 2", len(deleted))
	}
	for _, p := range deleted {
		if p.RawPDFPath == "" {
			t.Errorf("deleted payslip %d: RawPDFPath empty", p.ID)
		}
	}
	if _, err := st.GetPayslip(ctx, p2.ID); err != nil {
		t.Errorf("p2 should still exist: %v", err)
	}
	if _, err := st.GetPayslip(ctx, p1.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("p1 should be deleted: %v", err)
	}
}

func TestDeletePayslipsByIDs_Empty(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.DeletePayslipsByIDs(context.Background(), nil); err == nil {
		t.Error("want error for empty IDs, got nil")
	}
}

func TestConfirmPayslipsByStatus(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	p1 := samplePayslip(t, st, 1, 2026, "Google")
	st.SavePayslip(ctx, &p1)
	p2 := samplePayslip(t, st, 2, 2026, "Google")
	st.SavePayslip(ctx, &p2)

	n, err := st.ConfirmPayslipsByStatus(ctx, StatusPendingReview)
	if err != nil {
		t.Fatalf("ConfirmPayslipsByStatus: %v", err)
	}
	if n != 2 {
		t.Errorf("confirmed = %d, want 2", n)
	}
	got, _ := st.GetPayslip(ctx, p1.ID)
	if got.Status != StatusConfirmed {
		t.Errorf("p1 status = %q, want confirmed", got.Status)
	}
}

func TestConfirmPayslipsByIDs(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	p1 := samplePayslip(t, st, 1, 2026, "Google")
	st.SavePayslip(ctx, &p1)
	p2 := samplePayslip(t, st, 2, 2026, "Google")
	st.SavePayslip(ctx, &p2)
	p3 := samplePayslip(t, st, 3, 2026, "Google")
	st.SavePayslip(ctx, &p3)

	n, err := st.ConfirmPayslipsByIDs(ctx, []int64{p1.ID, p3.ID})
	if err != nil {
		t.Fatalf("ConfirmPayslipsByIDs: %v", err)
	}
	if n != 2 {
		t.Errorf("confirmed = %d, want 2", n)
	}
	got, _ := st.GetPayslip(ctx, p2.ID)
	if got.Status != StatusPendingReview {
		t.Errorf("p2 status = %q, want pending_review", got.Status)
	}
}

func TestConfirmPayslipsByIDs_Empty(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.ConfirmPayslipsByIDs(context.Background(), nil); err == nil {
		t.Error("want error for empty IDs, got nil")
	}
}

func TestGetIncomeTimeline_ConfirmedOnly(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	// Three payslips across 2025-2026; one pending, two confirmed.
	p1 := samplePayslip(t, st, 1, 2025, "Google")
	st.SavePayslip(ctx, &p1)
	st.ConfirmPayslip(ctx, p1.ID)

	p2 := samplePayslip(t, st, 6, 2025, "Google")
	st.SavePayslip(ctx, &p2)
	st.ConfirmPayslip(ctx, p2.ID)

	p3 := samplePayslip(t, st, 1, 2026, "Google")
	st.SavePayslip(ctx, &p3) // pending

	// Default: confirmed only, oldest first.
	ps, err := st.GetIncomeTimeline(ctx, false)
	if err != nil {
		t.Fatalf("GetIncomeTimeline: %v", err)
	}
	if len(ps) != 2 {
		t.Fatalf("got %d, want 2", len(ps))
	}
	if ps[0].PayPeriodYear != 2025 || ps[0].PayPeriodMonth != 1 {
		t.Errorf("oldest first: got (%d, %d), want (2025, 1)", ps[0].PayPeriodYear, ps[0].PayPeriodMonth)
	}
	if ps[1].PayPeriodMonth != 6 {
		t.Errorf("second: month = %d, want 6", ps[1].PayPeriodMonth)
	}

	// Include pending: three rows.
	ps, _ = st.GetIncomeTimeline(ctx, true)
	if len(ps) != 3 {
		t.Fatalf("with pending: got %d, want 3", len(ps))
	}
}

func TestGetComponentTimeline(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	basic, err := st.FindCanonicalByName(ctx, "basic")
	if err != nil {
		t.Fatalf("find basic: %v", err)
	}

	// Three confirmed + one pending. Pending must NOT appear in the series.
	months := []struct {
		month, year int
		amount      float64
		pending     bool
	}{
		{1, 2025, 30000, false},
		{6, 2025, 35000, false},
		{1, 2026, 40000, false},
		{2, 2026, 41000, true},
	}
	for _, m := range months {
		p := samplePayslip(t, st, m.month, m.year, "Google")
		// Override the basic component's amount.
		p.Components[0].Amount = m.amount
		if err := st.SavePayslip(ctx, &p); err != nil {
			t.Fatalf("SavePayslip: %v", err)
		}
		if !m.pending {
			st.ConfirmPayslip(ctx, p.ID)
		}
	}

	pts, err := st.GetComponentTimeline(ctx, basic.ID, "")
	if err != nil {
		t.Fatalf("GetComponentTimeline: %v", err)
	}
	if len(pts) != 3 {
		t.Fatalf("got %d points, want 3 (pending excluded)", len(pts))
	}
	if pts[0].Amount != 30000 || pts[2].Amount != 40000 {
		t.Errorf("series: %v", pts)
	}
	if pts[0].PayPeriodYear != 2025 || pts[2].PayPeriodYear != 2026 {
		t.Errorf("not chronological: %v", pts)
	}

	// Employer filter excludes everything if no match.
	pts, _ = st.GetComponentTimeline(ctx, basic.ID, "OtherCo")
	if len(pts) != 0 {
		t.Errorf("employer filter: got %d points, want 0", len(pts))
	}
}

func TestCreateCanonical_UserCreated(t *testing.T) {
	st := newTestStore(t)
	c, err := st.CreateCanonical(context.Background(), "rsu", CategoryEarning)
	if err != nil {
		t.Fatalf("CreateCanonical: %v", err)
	}
	if !c.IsUserCreated {
		t.Error("user-created canonical not flagged")
	}
	if c.ID == 0 {
		t.Error("ID not set")
	}
	// Unique constraint: re-creating same name must fail.
	if _, err := st.CreateCanonical(context.Background(), "rsu", CategoryEarning); err == nil {
		t.Error("duplicate name: expected error")
	}
}

func TestCanonical_DisplayName(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	// Seed canonicals get explicit display names from the map.
	basic, _ := st.FindCanonicalByName(ctx, "basic")
	if got := basic.DisplayName(); got != "Basic" {
		t.Errorf("basic.DisplayName() = %q, want %q", got, "Basic")
	}

	epf, _ := st.FindCanonicalByName(ctx, "epf")
	if got := epf.DisplayName(); got != "EPF" {
		t.Errorf("epf.DisplayName() = %q, want %q", got, "EPF")
	}

	special, _ := st.FindCanonicalByName(ctx, "special_allowance")
	if got := special.DisplayName(); got != "Special Allowance" {
		t.Errorf("special_allowance.DisplayName() = %q, want %q", got, "Special Allowance")
	}

	// User-created canonicals return their Name as-is — the user typed a
	// human-readable label, not a slug.
	rsu, _ := st.CreateCanonical(ctx, "RSU Grant", CategoryEarning)
	if got := rsu.DisplayName(); got != "RSU Grant" {
		t.Errorf("user-created DisplayName() = %q, want %q", got, "RSU Grant")
	}
}

func TestGetConfirmedTimeline_IncludesComponents(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	// Two confirmed payslips + one pending. Only confirmed should return.
	p1 := samplePayslip(t, st, 1, 2025, "Google")
	st.SavePayslip(ctx, &p1)
	st.ConfirmPayslip(ctx, p1.ID)

	p2 := samplePayslip(t, st, 6, 2025, "Google")
	// Tweak the basic amount to verify the component row is attached correctly.
	p2.Components[0].Amount = 45000
	st.SavePayslip(ctx, &p2)
	st.ConfirmPayslip(ctx, p2.ID)

	p3 := samplePayslip(t, st, 9, 2025, "Google")
	st.SavePayslip(ctx, &p3) // pending

	ps, err := st.GetConfirmedTimeline(ctx)
	if err != nil {
		t.Fatalf("GetConfirmedTimeline: %v", err)
	}
	if len(ps) != 2 {
		t.Fatalf("got %d, want 2 (pending excluded)", len(ps))
	}
	// Chronological order.
	if ps[0].ID != p1.ID || ps[1].ID != p2.ID {
		t.Errorf("order: got %d,%d want %d,%d", ps[0].ID, ps[1].ID, p1.ID, p2.ID)
	}
	// Components attached and in earning-first order.
	if len(ps[1].Components) != 2 {
		t.Fatalf("latest components: got %d, want 2", len(ps[1].Components))
	}
	if ps[1].Components[0].Category != CategoryEarning {
		t.Errorf("expected earning first, got %q", ps[1].Components[0].Category)
	}
	if ps[1].Components[0].Amount != 45000 {
		t.Errorf("tweaked amount not stored: got %v", ps[1].Components[0].Amount)
	}
}

func TestGetConfirmedTimeline_PayslipWithNoComponents(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	// Edge case: a payslip with zero components (manually inserted).
	p := samplePayslip(t, st, 1, 2025, "Google")
	p.Components = nil
	if err := st.SavePayslip(ctx, &p); err != nil {
		t.Fatalf("SavePayslip: %v", err)
	}
	st.ConfirmPayslip(ctx, p.ID)

	// SavePayslip currently requires at least one component (its loop is
	// unguarded). If that changes, this test catches a regression where
	// GetConfirmedTimeline's LEFT JOIN mis-scans the empty row.
	ps, err := st.GetConfirmedTimeline(ctx)
	if err != nil {
		t.Fatalf("GetConfirmedTimeline: %v", err)
	}
	// Whatever it returns must not panic or return partial payslips.
	for _, got := range ps {
		if got.ID == 0 {
			t.Errorf("zero ID returned: %+v", got)
		}
	}
}

func TestGetRawLabelUsage(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	basic, _ := st.FindCanonicalByName(ctx, "basic")
	tds, _ := st.FindCanonicalByName(ctx, "tds")

	// Two confirmed payslips, both using "Basic" raw label; one pending using
	// "Basic Pay" — pending should be excluded from usage counts.
	p1 := samplePayslip(t, st, 1, 2025, "Google")
	p1.Components[0].CanonicalID = basic.ID
	p1.Components[0].RawLabel = "Basic"
	p1.Components[1].CanonicalID = tds.ID
	st.SavePayslip(ctx, &p1)
	st.ConfirmPayslip(ctx, p1.ID)

	p2 := samplePayslip(t, st, 2, 2025, "Google")
	p2.Components[0].CanonicalID = basic.ID
	p2.Components[0].RawLabel = "Basic"
	p2.Components[1].CanonicalID = tds.ID
	st.SavePayslip(ctx, &p2)
	st.ConfirmPayslip(ctx, p2.ID)

	p3 := samplePayslip(t, st, 3, 2025, "Google")
	p3.Components[0].CanonicalID = basic.ID
	p3.Components[0].RawLabel = "Basic Pay" // only on pending
	st.SavePayslip(ctx, &p3) // pending

	uses, err := st.GetRawLabelUsage(ctx, basic.ID)
	if err != nil {
		t.Fatalf("GetRawLabelUsage: %v", err)
	}
	if len(uses) != 1 {
		t.Fatalf("got %d labels, want 1 (pending excluded); got %+v", len(uses), uses)
	}
	if uses[0].RawLabel != "Basic" || uses[0].Count != 2 {
		t.Errorf("usage: %+v, want Basic/2", uses[0])
	}
	if uses[0].LastSeen != "2025-02" {
		t.Errorf("last_seen = %q, want 2025-02", uses[0].LastSeen)
	}

	// Unused canonical returns empty.
	uses, _ = st.GetRawLabelUsage(ctx, 99999)
	if len(uses) != 0 {
		t.Errorf("unknown canonical: got %d rows, want 0", len(uses))
	}
}

func TestGetFYConfirmedTimeline_FYBoundary(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	// FY 2025-26 spans April 2025 through March 2026.
	cases := []struct {
		name       string
		month, year int
		confirm    bool
	}{
		{"March 2025 — previous FY", 3, 2025, true},
		{"April 2025 — FY start", 4, 2025, true},
		{"Jan 2026 — still in FY 2025-26", 1, 2026, true},
		{"March 2026 — FY end", 3, 2026, true},
		{"April 2026 — next FY", 4, 2026, true},
		{"March 2025 pending — excluded anyway", 3, 2025, false},
	}
	created := make(map[string]int64, len(cases))
	for _, c := range cases {
		p := samplePayslip(t, st, c.month, c.year, "Google")
		if err := st.SavePayslip(ctx, &p); err != nil {
			t.Fatalf("SavePayslip %s: %v", c.name, err)
		}
		if c.confirm {
			if err := st.ConfirmPayslip(ctx, p.ID); err != nil {
				t.Fatalf("ConfirmPayslip %s: %v", c.name, err)
			}
		}
		created[c.name] = p.ID
	}

	ps, err := st.GetFYConfirmedTimeline(ctx, 2025)
	if err != nil {
		t.Fatalf("GetFYConfirmedTimeline: %v", err)
	}
	// April 2025, Jan 2026, March 2026 → 3 in FY 2025-26.
	// March 2025 pending excluded; April 2026 is in FY 2026-27.
	wantCount := 3
	if len(ps) != wantCount {
		t.Fatalf("got %d payslips, want %d", len(ps), wantCount)
	}
	// Chronological: April 2025, Jan 2026, March 2026.
	wantOrder := []struct {
		month, year int
	}{
		{4, 2025}, {1, 2026}, {3, 2026},
	}
	for i, want := range wantOrder {
		if ps[i].PayPeriodMonth != want.month || ps[i].PayPeriodYear != want.year {
			t.Errorf("position %d: got (%d, %d), want (%d, %d)", i, ps[i].PayPeriodMonth, ps[i].PayPeriodYear, want.month, want.year)
		}
	}
	// Components attached.
	if len(ps[0].Components) != 2 {
		t.Errorf("expected 2 components attached to first FY payslip, got %d", len(ps[0].Components))
	}
}

func TestGetFYConfirmedTimeline_Empty(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	ps, err := st.GetFYConfirmedTimeline(ctx, 2099)
	if err != nil {
		t.Fatalf("GetFYConfirmedTimeline empty: %v", err)
	}
	if len(ps) != 0 {
		t.Errorf("got %d, want 0", len(ps))
	}
}

func TestGetYoYPair_Found(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	// Latest: July 2026 (FY 2026-27). Prev-FY same-month: July 2025 (FY 2025-26).
	latest := samplePayslip(t, st, 7, 2026, "Google")
	latest.Components[0].Amount = 60000
	if err := st.SavePayslip(ctx, &latest); err != nil {
		t.Fatalf("SavePayslip latest: %v", err)
	}
	st.ConfirmPayslip(ctx, latest.ID)

	prevFY := samplePayslip(t, st, 7, 2025, "Google")
	prevFY.Components[0].Amount = 50000
	if err := st.SavePayslip(ctx, &prevFY); err != nil {
		t.Fatalf("SavePayslip prevFY: %v", err)
	}
	st.ConfirmPayslip(ctx, prevFY.ID)

	// Distractor: June 2025 — same FY but wrong month, must NOT be returned.
	wrongMonth := samplePayslip(t, st, 6, 2025, "Google")
	if err := st.SavePayslip(ctx, &wrongMonth); err != nil {
		t.Fatalf("SavePayslip wrongMonth: %v", err)
	}
	st.ConfirmPayslip(ctx, wrongMonth.ID)

	gotLatest, gotPrev, err := st.GetYoYPair(ctx, latest.ID)
	if err != nil {
		t.Fatalf("GetYoYPair: %v", err)
	}
	if gotLatest.ID != latest.ID {
		t.Errorf("latest ID = %d, want %d", gotLatest.ID, latest.ID)
	}
	if gotPrev.ID != prevFY.ID {
		t.Errorf("prevFY ID = %d, want %d (wrong-month payslip must not match)", gotPrev.ID, prevFY.ID)
	}
	// Components attached.
	if len(gotLatest.Components) != 2 {
		t.Errorf("latest components: got %d, want 2", len(gotLatest.Components))
	}
	if len(gotPrev.Components) != 2 {
		t.Errorf("prevFY components: got %d, want 2", len(gotPrev.Components))
	}
}

func TestGetYoYPair_NoPrevFYMatch(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	// Latest: July 2026. No July 2025 payslip exists.
	latest := samplePayslip(t, st, 7, 2026, "Google")
	st.SavePayslip(ctx, &latest)
	st.ConfirmPayslip(ctx, latest.ID)

	_, _, err := st.GetYoYPair(ctx, latest.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("GetYoYPair without prev-FY match: err = %v, want ErrNotFound", err)
	}
}

func TestGetYoYPair_PendingLatestExcluded(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	// Pending payslip — should not be returned as "latest".
	pending := samplePayslip(t, st, 7, 2026, "Google")
	st.SavePayslip(ctx, &pending) // not confirmed

	_, _, err := st.GetYoYPair(ctx, pending.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("GetYoYPair with pending latest: err = %v, want ErrNotFound", err)
	}
}
