package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"path/filepath"
	"time"

	"cresto/internal/llm"
	"cresto/internal/render"
	"cresto/internal/store"
)

// processPDF runs the full extraction pipeline on an already-saved PDF and
// returns a Payslip ready for SavePayslip. It hides render → LLM extract →
// classify → map — the four-step pipeline that both single and bulk upload share.
//
// relPath is the PDFStore-relative filename (the value stored in RawPDFPath).
// The function resolves it to an absolute path for the render step; the
// returned Payslip.RawPDFPath is relPath so the caller stores the relative form.
// Status is left zero so the caller picks the appropriate lifecycle
// (pending_review for fresh parses).
func (s *Server) processPDF(ctx context.Context, relPath string) (store.Payslip, error) {
	img, err := render.Render(s.pdfs.Abs(relPath))
	if err != nil {
		return store.Payslip{}, fmt.Errorf("render pdf: %w", err)
	}
	ext, err := s.llmClient.Extract(img)
	if err != nil {
		return store.Payslip{}, fmt.Errorf("llm extract: %w", err)
	}
	canonicals, err := s.store.ListCanonicals(ctx)
	if err != nil {
		return store.Payslip{}, fmt.Errorf("load canonicals: %w", err)
	}
	// Stage 2: text-only classification. Convert canonicals to the llm package's
	// CanonicalRef at the seam so llm stays decoupled from store. On failure,
	// pass nil so MapExtraction falls back to the keyword mapper — extraction
	// still succeeds, just with less accurate canonical assignment.
	refs := canonicalsToRefs(canonicals)
	class, err := s.llmClient.Classify(ext, refs)
	if err != nil || class == nil {
		log.Printf("classify: falling back to keyword mapper: %v", err)
		class = nil
	} else {
		log.Printf("classify: earnings=%v deductions=%v", class.Earnings, class.Deductions)
	}
	p, err := MapExtraction(ext, class, canonicals)
	if err != nil {
		return store.Payslip{}, fmt.Errorf("map extraction: %w", err)
	}
	p.RawPDFPath = relPath
	return p, nil
}

// canonicalsToRefs converts store canonicals to the llm package's CanonicalRef.
// This is the adapter at the seam between store and llm — keeps llm from
// importing store. Display names come from Canonical.DisplayName() so the
// classifier sees human-readable labels to match against.
func canonicalsToRefs(canonicals []store.Canonical) []llm.CanonicalRef {
	refs := make([]llm.CanonicalRef, len(canonicals))
	for i, c := range canonicals {
		refs[i] = llm.CanonicalRef{
			Slug:     c.Name,
			Name:     c.DisplayName(),
			Category: string(c.Category),
		}
	}
	return refs
}

// processBatch runs the extraction pipeline sequentially across a batch's
// saved PDFs in a background goroutine. For each PDF it inserts a payslip as
// pending_review (on success) or failed (with error_message), then bumps the
// batch's corresponding counter. Sequential — not parallel — so a local
// CPU-bound LLM isn't overloaded. Errors are logged, not returned: the
// batch's failed_count is the durable record.
//
// relPaths are PDFStore-relative filenames; they're stored verbatim in
// RawPDFPath for both successful and failed parses.
func (s *Server) processBatch(batchID string, relPaths []string) {
	ctx := context.Background()
	for _, rel := range relPaths {
		_ = s.store.UpdateBatchProgress(ctx, batchID, rel, "processing")
		p, err := s.processPDF(ctx, rel)
		if err != nil {
			failed := store.Payslip{
				EmployerName: filepath.Base(rel),
				Status:       store.StatusFailed,
				RawPDFPath:   rel,
				BatchID:      batchID,
				ErrorMessage: err.Error(),
			}
			if saveErr := s.store.SavePayslip(ctx, &failed); saveErr != nil {
				log.Printf("batch %s: save failed payslip %s: %v", batchID, rel, saveErr)
			}
			if incErr := s.store.IncrementBatchFailed(ctx, batchID); incErr != nil {
				log.Printf("batch %s: increment failed: %v", batchID, incErr)
			}
			continue
		}
		p.BatchID = batchID
		p.Status = store.StatusPendingReview
		if err := s.store.SavePayslip(ctx, &p); err != nil {
			log.Printf("batch %s: save payslip %s: %v", batchID, rel, err)
		}
		if err := s.store.IncrementBatchProcessed(ctx, batchID); err != nil {
			log.Printf("batch %s: increment processed: %v", batchID, err)
	}
	_ = s.store.UpdateBatchProgress(ctx, batchID, "", "")
}
}

// processRetry re-runs the pipeline on a single failed payslip in a background
// goroutine. The payslip is marked processing first so the queue shows the
// retry is underway; on completion it moves to pending_review or back to
// failed with a fresh error_message. Batch counters are not touched — retries
// are independent of the original batch run.
func (s *Server) processRetry(payslipID int64) {
	ctx := context.Background()
	p, err := s.store.GetPayslip(ctx, payslipID)
	if err != nil {
		log.Printf("retry %d: load payslip: %v", payslipID, err)
		return
	}
	if p.RawPDFPath == "" {
		_ = s.store.MarkPayslipFailed(ctx, payslipID, "no source PDF on disk")
		return
	}
	result, err := s.processPDF(ctx, p.RawPDFPath)
	if err != nil {
		_ = s.store.MarkPayslipFailed(ctx, payslipID, err.Error())
		return
	}
	result.ID = payslipID
	result.Status = store.StatusPendingReview
	result.BatchID = p.BatchID
	result.RawPDFPath = p.RawPDFPath
	if err := s.store.UpdatePayslip(ctx, &result); err != nil {
		log.Printf("retry %d: update payslip: %v", payslipID, err)
	}
}

// newBatchID returns a short random hex string suitable as a batch identifier.
// Not a security primitive — just unique enough to avoid collisions between
// rapid successive uploads.
func newBatchID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("b%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
