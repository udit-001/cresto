package web

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"cresto/internal/store"
)

// seedFailedPayslip inserts a payslip and marks it failed with an error message.
func seedFailedPayslip(t *testing.T, srv *Server, errMsg string) store.Payslip {
	t.Helper()
	p := seedPayslip(t, srv)
	if err := srv.store.MarkPayslipFailed(context.Background(), p.ID, errMsg); err != nil {
		t.Fatalf("MarkPayslipFailed: %v", err)
	}
	p.Status = store.StatusFailed
	p.ErrorMessage = errMsg
	return p
}

// seedProcessingPayslip inserts a payslip and flips it to processing.
func seedProcessingPayslip(t *testing.T, srv *Server) store.Payslip {
	t.Helper()
	p := seedPayslip(t, srv)
	if err := srv.store.MarkPayslipProcessing(context.Background(), p.ID); err != nil {
		t.Fatalf("MarkPayslipProcessing: %v", err)
	}
	p.Status = store.StatusProcessing
	return p
}

func TestBatchProgress_NotFound(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	rec, _ := doGet(srv, "/upload/batch/ghost")
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestBatchProgress_MissingID(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	// Route pattern is /upload/batch/{id}; an empty {id} won't match the
	// registered pattern, so Go's mux returns 404. This is acceptable — the
	// URL is only ever generated server-side.
	rec, _ := doGet(srv, "/upload/batch/")
	if rec.Code == 500 {
		t.Errorf("status = %d, want non-500 (404 expected for missing id)", rec.Code)
	}
}

func TestBatchProgress_RendersProgress(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	ctx := context.Background()
	if err := srv.store.CreateBatch(ctx, "batch-1", 5); err != nil {
		t.Fatalf("CreateBatch: %v", err)
	}
	srv.store.IncrementBatchProcessed(ctx, "batch-1")
	srv.store.IncrementBatchFailed(ctx, "batch-1")

	rec, _ := doGet(srv, "/upload/batch/batch-1")
	if rec.Code != 200 {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Batch Progress") {
		t.Errorf("missing title; got: %s", body[:min(200, len(body))])
	}
	// 2 of 5 done (1 processed + 1 failed). The progress text shows "2 of 5".
	if !strings.Contains(body, "2 of 5") {
		t.Errorf("missing '2 of 5' progress; got: %s", body[:min(300, len(body))])
	}
	// Failed counter is rendered server-side; processed is implied by "done".
	if !strings.Contains(body, "1 failed") {
		t.Errorf("missing '1 failed' counter; got: %s", body[:min(300, len(body))])
	}
	// Not done (2 < 5) → auto-refresh script present.
	if !strings.Contains(body, "location.reload()") {
		t.Errorf("missing auto-refresh for in-flight batch")
	}
}

func TestBatchProgress_DoneHidesRefresh(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	ctx := context.Background()
	if err := srv.store.CreateBatch(ctx, "batch-2", 2); err != nil {
		t.Fatalf("CreateBatch: %v", err)
	}
	srv.store.IncrementBatchProcessed(ctx, "batch-2")
	srv.store.IncrementBatchProcessed(ctx, "batch-2")

	rec, _ := doGet(srv, "/upload/batch/batch-2")
	body := rec.Body.String()
	if strings.Contains(body, "location.reload()") {
		t.Errorf("done batch should not auto-refresh; got: %s", body[:min(200, len(body))])
	}
	if !strings.Contains(body, "Review pending") {
		t.Errorf("done batch should link to review pending")
	}
}

func TestRetry_FailedPayslipRedirectsToQueue(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	p := seedFailedPayslip(t, srv, "broken")

	rec := doPostForm(srv, "/payslip/"+itoa(p.ID)+"/retry", "")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body: %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "/payslips?status=pending_review") {
		t.Errorf("redirect = %q, want /payslips?status=pending_review...", loc)
	}
}

func TestRetry_NonFailedRedirectsToReview(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	p := seedPayslip(t, srv) // pending, not failed

	rec := doPostForm(srv, "/payslip/"+itoa(p.ID)+"/retry", "")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/payslip/"+itoa(p.ID)) {
		t.Errorf("redirect = %q, want /payslip/%d (non-failed should not retry)", loc, p.ID)
	}
	got, _ := srv.store.GetPayslip(context.Background(), p.ID)
	if got.Status != store.StatusPendingReview {
		t.Errorf("status = %q, want pending_review (retry should not touch non-failed)", got.Status)
	}
}

func TestRetry_NotFound(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	rec := doPostForm(srv, "/payslip/9999/retry", "")
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// TestNextPendingID_Chronological proves the "next" logic walks the pending
// list in list-order (which ListPendingReviewChronological makes chronological)
// rather than by ID.
func TestNextPendingID_Chronological(t *testing.T) {
	// Pending in chronological order: periods (3, 5, 7) with IDs (30, 10, 20).
	// nextPendingID must return the next in the slice, not the next by ID.
	pending := []store.Payslip{
		{ID: 30, PayPeriodMonth: 3, PayPeriodYear: 2026},
		{ID: 10, PayPeriodMonth: 5, PayPeriodYear: 2026},
		{ID: 20, PayPeriodMonth: 7, PayPeriodYear: 2026},
	}
	if got := nextPendingID(pending, 30); got != 10 {
		t.Errorf("after ID 30: got %d, want 10 (next chronological)", got)
	}
	if got := nextPendingID(pending, 10); got != 20 {
		t.Errorf("after ID 10: got %d, want 20", got)
	}
	if got := nextPendingID(pending, 20); got != 0 {
		t.Errorf("last in list: got %d, want 0", got)
	}
	if got := nextPendingID(pending, 999); got != 0 {
		t.Errorf("not in list: got %d, want 0", got)
	}
}

func TestNewBatchID_Unique(t *testing.T) {
	seen := make(map[string]bool, 100)
	for i := 0; i < 100; i++ {
		id := newBatchID()
		if seen[id] {
			t.Fatalf("collision after %d IDs: %s", i, id)
		}
		seen[id] = true
	}
	if len(newBatchID()) < 8 {
		t.Errorf("batch ID too short")
	}
}
