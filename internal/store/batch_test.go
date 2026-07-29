package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// oldPayslipsSchema is the pre-batch payslips DDL — the shape the legacy
// migrate() function is responsible for upgrading. Used to prove backward
// compatibility works on databases created before goose was introduced.
const oldPayslipsSchema = `
CREATE TABLE payslips (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    employer_name       TEXT NOT NULL,
    pay_period_month    INTEGER NOT NULL CHECK (pay_period_month BETWEEN 1 AND 12),
    pay_period_year     INTEGER NOT NULL,
    employee_id         TEXT NOT NULL DEFAULT '',
    designation         TEXT NOT NULL DEFAULT '',
    pay_days            INTEGER NOT NULL DEFAULT 0,
    total_days          INTEGER NOT NULL DEFAULT 0,
    gross_salary        REAL NOT NULL DEFAULT 0,
    total_deductions    REAL NOT NULL DEFAULT 0,
    net_pay             REAL NOT NULL DEFAULT 0,
    status              TEXT NOT NULL DEFAULT 'pending_review'
                            CHECK (status IN ('pending_review', 'confirmed')),
    raw_pdf_path        TEXT NOT NULL DEFAULT '',
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    confirmed_at        TEXT
);
CREATE TABLE canonicals (
    id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL UNIQUE,
    category TEXT NOT NULL, is_user_created INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
`

// TestMigrate_AddsBatchColumns opens a DB built with the old schema, inserts
// a row, then re-opens through Open (which runs legacy migrate + goose). The
// existing row must survive, and the new columns + expanded status constraint
// must be usable.
func TestMigrate_AddsBatchColumns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	// Build a pre-batch database: old payslips schema + a real row + a confirmed row.
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open old db: %v", err)
	}
	if _, err := db.Exec(oldPayslipsSchema); err != nil {
		t.Fatalf("create old schema: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO canonicals (name, category) VALUES ('basic', 'earning')`); err != nil {
		t.Fatalf("seed canonical: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO payslips (employer_name, pay_period_month, pay_period_year, status, raw_pdf_path)
		VALUES ('Acme', 7, 2026, 'pending_review', '/tmp/x.pdf')`); err != nil {
		t.Fatalf("insert pending: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO payslips (employer_name, pay_period_month, pay_period_year, status, raw_pdf_path)
		VALUES ('Acme', 6, 2026, 'confirmed', '/tmp/y.pdf')`); err != nil {
		t.Fatalf("insert confirmed: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close old db: %v", err)
	}

	// Open through the store, which runs legacy migrate + goose up.
	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open after migration: %v", err)
	}
	defer st.Close()
	ctx := context.Background()

	// Existing rows survived.
	ps, err := st.ListPayslips(ctx, Filter{})
	if err != nil {
		t.Fatalf("ListPayslips: %v", err)
	}
	if len(ps) != 2 {
		t.Fatalf("got %d payslips, want 2 (migration lost data?)", len(ps))
	}

	// New statuses are now writable (would violate the old CHECK constraint).
	p := Payslip{EmployerName: "New", PayPeriodMonth: 1, PayPeriodYear: 2026, Status: StatusProcessing}
	if err := st.SavePayslip(ctx, &p); err != nil {
		t.Fatalf("SavePayslip with processing status: %v", err)
	}
	if err := st.MarkPayslipFailed(ctx, p.ID, "boom"); err != nil {
		t.Fatalf("MarkPayslipFailed: %v", err)
	}
	got, err := st.GetPayslip(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetPayslip: %v", err)
	}
	if got.Status != StatusFailed {
		t.Errorf("status = %q, want failed", got.Status)
	}
	if got.ErrorMessage != "boom" {
		t.Errorf("error_message = %q, want boom", got.ErrorMessage)
	}
	if got.BatchID != "" {
		t.Errorf("batch_id should default to empty, got %q", got.BatchID)
	}

	// Re-opening is a no-op migration (idempotent).
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer st2.Close()
	if _, err := st2.GetBatch(ctx, "nope"); err != ErrNotFound {
		t.Errorf("GetBatch on missing: err = %v, want ErrNotFound", err)
	}
}

// TestBatch_Lifecycle exercises batch creation, increment, and lookup.
func TestBatch_Lifecycle(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	if err := st.CreateBatch(ctx, "batch-1", 3); err != nil {
		t.Fatalf("CreateBatch: %v", err)
	}
	b, err := st.GetBatch(ctx, "batch-1")
	if err != nil {
		t.Fatalf("GetBatch: %v", err)
	}
	if b.Total != 3 || b.ProcessedCount != 0 || b.FailedCount != 0 {
		t.Errorf("initial batch = %+v, want total=3/0/0", b)
	}

	if err := st.IncrementBatchProcessed(ctx, "batch-1"); err != nil {
		t.Fatalf("IncrementBatchProcessed: %v", err)
	}
	if err := st.IncrementBatchFailed(ctx, "batch-1"); err != nil {
		t.Fatalf("IncrementBatchFailed: %v", err)
	}
	b, _ = st.GetBatch(ctx, "batch-1")
	if b.ProcessedCount != 1 || b.FailedCount != 1 {
		t.Errorf("after increments = %+v, want 1/1", b)
	}
}

func TestGetBatch_NotFound(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.GetBatch(context.Background(), "ghost"); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestListPendingReviewChronological(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	// Insert out of order by ID to prove the sort ignores insertion order.
	for _, m := range []int{8, 3, 11, 5} {
		// Vary the year too so we exercise year-then-month ordering.
		year := 2026
		if m > 9 {
			year = 2025
		}
		p := samplePayslip(t, st, m, year, "Google")
		if err := st.SavePayslip(ctx, &p); err != nil {
			t.Fatalf("SavePayslip: %v", err)
		}
	}

	ps, err := st.ListPendingReviewChronological(ctx)
	if err != nil {
		t.Fatalf("ListPendingReviewChronological: %v", err)
	}
	if len(ps) != 4 {
		t.Fatalf("got %d, want 4", len(ps))
	}
	// Expected chronological order: (2025, 11), (2026, 3), (2026, 5), (2026, 8).
	want := []struct{ month, year int }{{11, 2025}, {3, 2026}, {5, 2026}, {8, 2026}}
	for i, w := range want {
		if ps[i].PayPeriodMonth != w.month || ps[i].PayPeriodYear != w.year {
			t.Errorf("position %d = %d/%d, want %d/%d", i, ps[i].PayPeriodMonth, ps[i].PayPeriodYear, w.month, w.year)
		}
	}
}

func TestListPendingReviewChronological_ZeroPeriodSortsLast(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	// A payslip whose period failed to parse (0/0) should sort after real periods.
	p0 := samplePayslip(t, st, 0, 0, "Unknown")
	if err := st.SavePayslip(ctx, &p0); err != nil {
		t.Fatalf("SavePayslip 0/0: %v", err)
	}
	p6 := samplePayslip(t, st, 6, 2026, "Google")
	if err := st.SavePayslip(ctx, &p6); err != nil {
		t.Fatalf("SavePayslip 6/2026: %v", err)
	}

	ps, _ := st.ListPendingReviewChronological(ctx)
	if len(ps) != 2 {
		t.Fatalf("got %d, want 2", len(ps))
	}
	if ps[0].ID != p6.ID {
		t.Errorf("real period should sort first; got ID %d, want %d", ps[0].ID, p6.ID)
	}
	if ps[1].ID != p0.ID {
		t.Errorf("zero period should sort last; got ID %d, want %d", ps[1].ID, p0.ID)
	}
}

func TestListFailed(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	// One pending, two failed.
	p1 := samplePayslip(t, st, 1, 2026, "Google")
	st.SavePayslip(ctx, &p1) // pending
	p2 := samplePayslip(t, st, 2, 2026, "Google")
	st.SavePayslip(ctx, &p2)
	st.MarkPayslipFailed(ctx, p2.ID, "LLM timed out")
	p3 := samplePayslip(t, st, 3, 2026, "Google")
	st.SavePayslip(ctx, &p3)
	st.MarkPayslipFailed(ctx, p3.ID, "render failed")

	failed, err := st.ListFailed(ctx)
	if err != nil {
		t.Fatalf("ListFailed: %v", err)
	}
	if len(failed) != 2 {
		t.Fatalf("got %d failed, want 2", len(failed))
	}
	for _, f := range failed {
		if f.Status != StatusFailed {
			t.Errorf("status = %q, want failed", f.Status)
		}
		if f.ErrorMessage == "" {
			t.Errorf("failed payslip %d missing error_message", f.ID)
		}
	}
}

func TestMarkPayslipProcessing_ClearsError(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	p := samplePayslip(t, st, 1, 2026, "Google")
	st.SavePayslip(ctx, &p)
	st.MarkPayslipFailed(ctx, p.ID, "first error")

	if err := st.MarkPayslipProcessing(ctx, p.ID); err != nil {
		t.Fatalf("MarkPayslipProcessing: %v", err)
	}
	got, _ := st.GetPayslip(ctx, p.ID)
	if got.Status != StatusProcessing {
		t.Errorf("status = %q, want processing", got.Status)
	}
	if got.ErrorMessage != "" {
		t.Errorf("error_message = %q, want empty after MarkPayslipProcessing", got.ErrorMessage)
	}
}

func TestMarkPayslipFailed_NotFound(t *testing.T) {
	st := newTestStore(t)
	if err := st.MarkPayslipFailed(context.Background(), 9999, "x"); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestMarkPayslipProcessing_NotFound(t *testing.T) {
	st := newTestStore(t)
	if err := st.MarkPayslipProcessing(context.Background(), 9999); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestUpdatePayslip_SetsStatusAndClearsError(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	p := samplePayslip(t, st, 1, 2026, "Google")
	st.SavePayslip(ctx, &p)
	st.MarkPayslipFailed(ctx, p.ID, "broken")

	// Retry succeeds: load it, tweak, mark pending via UpdatePayslip.
	got, _ := st.GetPayslip(ctx, p.ID)
	got.Status = StatusPendingReview
	got.EmployerName = "Fixed"
	if err := st.UpdatePayslip(ctx, &got); err != nil {
		t.Fatalf("UpdatePayslip: %v", err)
	}
	reloaded, _ := st.GetPayslip(ctx, p.ID)
	if reloaded.Status != StatusPendingReview {
		t.Errorf("status = %q, want pending_review", reloaded.Status)
	}
	if reloaded.ErrorMessage != "" {
		t.Errorf("error_message = %q, want cleared", reloaded.ErrorMessage)
	}
	if reloaded.EmployerName != "Fixed" {
		t.Errorf("employer = %q, want Fixed", reloaded.EmployerName)
	}
}

func TestSavePayslip_PreservesBatchID(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	p := samplePayslip(t, st, 1, 2026, "Google")
	p.BatchID = "batch-42"
	if err := st.SavePayslip(ctx, &p); err != nil {
		t.Fatalf("SavePayslip: %v", err)
	}
	got, _ := st.GetPayslip(ctx, p.ID)
	if got.BatchID != "batch-42" {
		t.Errorf("batch_id = %q, want batch-42", got.BatchID)
	}

	// Filter by batch_id returns only matching rows.
	filtered, err := st.ListPayslips(ctx, Filter{BatchID: "batch-42"})
	if err != nil {
		t.Fatalf("ListPayslips by batch: %v", err)
	}
	if len(filtered) != 1 {
		t.Fatalf("batch filter: got %d, want 1", len(filtered))
	}
}
