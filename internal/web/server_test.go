package web

import (
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cresto/internal/config"
	"cresto/internal/groww"
	"cresto/internal/kite"
	"cresto/internal/llm"
	"cresto/internal/pdfstore"
	"cresto/internal/store"
)

// fakeLLMClient is a test stub for the LLMClient interface. It never touches
// a real LM Studio server — Health() returns instantly, Extract() returns
// a canned extraction. This keeps the web test suite from hitting localhost:1234
// (which was causing 60+ second runs when LM Studio was down).
type fakeLLMClient struct{}

func (fakeLLMClient) Health() string                    { return "loaded" }
func (fakeLLMClient) Start()                            {}
func (fakeLLMClient) Stop()                             {}
func (fakeLLMClient) Extract([]byte) (*llm.Extraction, error) {
	return &llm.Extraction{Company: "Test Co", PayPeriod: "January 2026"}, nil
}
func (fakeLLMClient) Classify(ext *llm.Extraction, _ []llm.CanonicalRef) (*llm.Classification, error) {
	return nil, nil // pipeline falls back to keyword mapper
}

// newTestServer returns a Server backed by a temp SQLite DB. The LLM client is
// real but never hit in these tests (no uploads); they exercise the review
// queue, detail page, confirm flow, and PDF serving.
func newTestServer(t *testing.T) (*Server, func()) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	cfg := config.Config{
		LMStudioBaseURL: "http://localhost:1234/v1",
		ModelName:       "test-model",
		DataDir:         dir,
		SQLitePath:      filepath.Join(dir, "test.db"),
		PDFStoragePath:  filepath.Join(dir, "payslips"),
	}
	pdfs := pdfstore.New(cfg.PDFStoragePath)
	growwClient := groww.New(filepath.Join(dir, "groww_token.json"))
	kiteClient := kite.New(filepath.Join(dir, "kite_session.json"))
	srv, err := New(st, fakeLLMClient{}, cfg, pdfs, growwClient, kiteClient)
	if err != nil {
		st.Close()
		t.Fatalf("New: %v", err)
	}
	return srv, func() { st.Close() }
}

// seedPayslip inserts a pending payslip with two components and returns its ID.
func seedPayslip(t *testing.T, srv *Server) store.Payslip {
	t.Helper()
	ctx := context.Background()
	basic, err := srv.store.FindCanonicalByName(ctx, "basic")
	if err != nil {
		t.Fatalf("find basic: %v", err)
	}
	tds, err := srv.store.FindCanonicalByName(ctx, "tds")
	if err != nil {
		t.Fatalf("find tds: %v", err)
	}
	p := store.Payslip{
		EmployerName:    "Acme Corp",
		PayPeriodMonth:  7,
		PayPeriodYear:   2026,
		GrossSalary:     50000,
		TotalDeductions: 5000,
		NetPay:          45000,
		Components: []store.Component{
			{CanonicalID: basic.ID, RawLabel: "Basic", Amount: 40000, YTDAmt: 280000, Category: store.CategoryEarning},
			{CanonicalID: tds.ID, RawLabel: "TDS", Amount: 5000, YTDAmt: 35000, Category: store.CategoryDeduction},
		},
	}
	if err := srv.store.SavePayslip(ctx, &p); err != nil {
		t.Fatalf("SavePayslip: %v", err)
	}
	return p
}

func doGet(srv *Server, path string) (*httptest.ResponseRecorder, *http.Request) {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	return rec, req
}

func doPostForm(srv *Server, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	return rec
}

func TestDashboard_Empty(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	rec, _ := doGet(srv, "/")
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Nothing here yet") {
		t.Errorf("empty dashboard missing placeholder; got: %s", body)
	}
}

func TestDashboard_WithPendingButNoConfirmed(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	seedPayslip(t, srv) // pending
	rec, _ := doGet(srv, "/")
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	// Should nudge toward review, not show empty placeholder.
	if !strings.Contains(body, "Confirm your first payslip") {
		t.Errorf("missing confirm-CTA; got: %s", body)
	}
	if !strings.Contains(body, "Acme Corp") {
		t.Errorf("missing pending employer; got: %s", body)
	}
	// Nav badge should show 1 pending.
	if !strings.Contains(body, `>1</span>`) {
		t.Errorf("nav missing pending badge")
	}
}

// seedConfirmedPayslip inserts a CONFIRMED payslip with two components and
// returns it. Used by dashboard and drill-down tests.
func seedConfirmedPayslip(t *testing.T, srv *Server, month, year int, basicAmt, tdsAmt, net float64) store.Payslip {
	t.Helper()
	ctx := context.Background()
	basic, err := srv.store.FindCanonicalByName(ctx, "basic")
	if err != nil {
		t.Fatalf("find basic: %v", err)
	}
	tds, err := srv.store.FindCanonicalByName(ctx, "tds")
	if err != nil {
		t.Fatalf("find tds: %v", err)
	}
	p := store.Payslip{
		EmployerName:    "Acme Corp",
		PayPeriodMonth:  month,
		PayPeriodYear:   year,
		GrossSalary:     basicAmt,
		TotalDeductions: tdsAmt,
		NetPay:          net,
		Components: []store.Component{
			{CanonicalID: basic.ID, RawLabel: "Basic", Amount: basicAmt, Category: store.CategoryEarning},
			{CanonicalID: tds.ID, RawLabel: "TDS", Amount: tdsAmt, Category: store.CategoryDeduction},
		},
	}
	if err := srv.store.SavePayslip(ctx, &p); err != nil {
		t.Fatalf("SavePayslip: %v", err)
	}
	if err := srv.store.ConfirmPayslip(ctx, p.ID); err != nil {
		t.Fatalf("ConfirmPayslip: %v", err)
	}
	return p
}

// compSeed is a (canonical name, amount, category) triple used by
// seedConfirmedPayslipFull when a test needs more than the basic+tds pair
// that seedConfirmedPayslip provides.
type compSeed struct {
	CanonicalName string
	Amount        float64
	Category      store.Category
}

// seedConfirmedPayslipFull inserts a CONFIRMED payslip with an arbitrary set
// of components. Used by dashboard callout-cap tests that need >6 deltas.
func seedConfirmedPayslipFull(t *testing.T, srv *Server, month, year int, comps []compSeed, gross, deduct, net float64) store.Payslip {
	t.Helper()
	ctx := context.Background()
	components := make([]store.Component, 0, len(comps))
	for _, cs := range comps {
		canon, err := srv.store.FindCanonicalByName(ctx, cs.CanonicalName)
		if err != nil {
			t.Fatalf("find canonical %q: %v", cs.CanonicalName, err)
		}
		components = append(components, store.Component{
			CanonicalID: canon.ID,
			RawLabel:    cs.CanonicalName,
			Amount:      cs.Amount,
			Category:    cs.Category,
		})
	}
	p := store.Payslip{
		EmployerName:    "Acme Corp",
		PayPeriodMonth:  month,
		PayPeriodYear:   year,
		GrossSalary:     gross,
		TotalDeductions: deduct,
		NetPay:          net,
		Components:      components,
	}
	if err := srv.store.SavePayslip(ctx, &p); err != nil {
		t.Fatalf("SavePayslip: %v", err)
	}
	if err := srv.store.ConfirmPayslip(ctx, p.ID); err != nil {
		t.Fatalf("ConfirmPayslip: %v", err)
	}
	return p
}

func TestDashboard_SingleConfirmed(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	seedConfirmedPayslip(t, srv, 7, 2026, 50000, 5000, 45000)

	rec, _ := doGet(srv, "/")
	if rec.Code != 200 {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// Headline numbers render. Note: template emits the rupee sign as the
	// HTML entity &#8377; — assert against that, not the literal char.
	if !strings.Contains(body, "&#8377;<span class=\"money\">45,000</span>") {
		t.Errorf("missing latest net; got first 300 chars: %s", body[:min(300, len(body))])
	}
	if !strings.Contains(body, "July 2026") {
		t.Errorf("missing latest period label")
	}
	// No MoM callout (only one confirmed).
	if strings.Contains(body, "vs previous") {
		t.Errorf("single-payslip dashboard should not show MoM delta")
	}
	// Net-pay chart canvas is present; the redundant gross-breakdown chart is gone.
	if !strings.Contains(body, `id="net-chart"`) {
		t.Errorf("missing net chart canvas")
	}
	if strings.Contains(body, `id="breakdown-chart"`) {
		t.Errorf("breakdown chart should be removed from the dashboard")
	}
	// Drill-down table includes the basic canonical (now shown as display name).
	if !strings.Contains(body, "Basic") {
		t.Errorf("missing basic canonical in drill-down")
	}
}

func TestDashboard_DropsBreakdownChart(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	seedConfirmedPayslip(t, srv, 7, 2026, 50000, 5000, 45000)

	rec, _ := doGet(srv, "/")
	if rec.Code != 200 {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, `id="breakdown-chart"`) {
		t.Errorf("breakdown chart canvas should not render; got substring")
	}
	if strings.Contains(body, "Gross breakdown") {
		t.Errorf("breakdown chart card header should not render")
	}
}

func TestDashboard_CalloutCappedAtSix(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	// Previous month: only basic. Latest month: basic + 7 new earnings, each
	// up by ₹1 — 8 non-zero deltas. The callout should cap at 6 visible rows
	// and surface "and 2 more" linking to the components table below.
	seedConfirmedPayslipFull(t, srv, 6, 2026,
		[]compSeed{{"basic", 50000, store.CategoryEarning}},
		50000, 0, 50000)
	seedConfirmedPayslipFull(t, srv, 7, 2026,
		[]compSeed{
			{"basic", 51000, store.CategoryEarning},
			{"hra", 1000, store.CategoryEarning},
			{"da", 1000, store.CategoryEarning},
			{"conveyance", 1000, store.CategoryEarning},
			{"medical", 1000, store.CategoryEarning},
			{"lta", 1000, store.CategoryEarning},
			{"education", 1000, store.CategoryEarning},
			{"telephone", 1000, store.CategoryEarning},
		},
		58100, 0, 58100)

	rec, _ := doGet(srv, "/")
	if rec.Code != 200 {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// 8 non-zero deltas, cap = 6 → "and 2 more".
	if !strings.Contains(body, "and 2 more") {
		t.Errorf("missing 'and 2 more' link; got first 600 chars: %s", body[:min(600, len(body))])
	}
	// The "more" link should anchor to the components table.
	if !strings.Contains(body, `href="#components"`) {
		t.Errorf("missing #components anchor on 'more' link")
	}
}

func TestDashboard_CalloutNoMoreLinkWhenFewDeltas(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	// Two confirmed payslips, only basic changed → 1 non-zero delta.
	// Cap is 6, so no "and N more" link should render.
	seedConfirmedPayslip(t, srv, 6, 2026, 50000, 5000, 45000)
	seedConfirmedPayslip(t, srv, 7, 2026, 60000, 5000, 50000)

	rec, _ := doGet(srv, "/")
	if rec.Code != 200 {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "and ") && strings.Contains(body, "more") {
		t.Errorf("'and N more' link should not render with ≤6 deltas; got: %s", body[:min(500, len(body))])
	}
}

func TestAnnualRoute_Empty_ShowsAllSectionGates(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	rec, _ := doGet(srv, "/annual")
	if rec.Code != 200 {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// FY label renders (defaults to current FY since no confirmed payslips).
	if !strings.Contains(body, "FY 20") {
		t.Errorf("missing FY label; got first 600 chars: %s", body[:min(600, len(body))])
	}
	// All three section gates surface their empty-state notices.
	for _, want := range []string{
		"Needs ≥2 confirmed payslips",
		"Needs a matching payslip",
		"Needs ≥1 confirmed payslip",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing empty-state %q; got: %s", want, body[:min(500, len(body))])
		}
	}
}

func TestAnnualRoute_QueryParamSelectsFY(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	rec, _ := doGet(srv, "/annual?fy=2025")
	if rec.Code != 200 {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "FY 2025-26") {
		t.Errorf("missing FY 2025-26 label for ?fy=2025; got: %s", body[:min(400, len(body))])
	}
}

func TestAnnualRoute_InvalidQueryParamFallsBackToCurrentFY(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	// Non-numeric or malformed fy should fall back to the current FY rather
	// than 500ing. Current FY label always starts with "FY 20".
	rec, _ := doGet(srv, "/annual?fy=not-a-year")
	if rec.Code != 200 {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "FY 20") {
		t.Errorf("malformed fy should fall back to current FY label; got: %s", body[:min(400, len(body))])
	}
}

func TestAnnualRoute_SingleConfirmed_OnlyAnnualSummaryGated(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	seedConfirmedPayslip(t, srv, 7, 2026, 50000, 5000, 45000)

	rec, _ := doGet(srv, "/annual")
	if rec.Code != 200 {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// FY 2026-27 is the current FY for July 2026.
	if !strings.Contains(body, "FY 2026-27") {
		t.Errorf("missing FY 2026-27 label; got: %s", body[:min(400, len(body))])
	}
	// With one payslip, YTD (≥2 months) and YoY (no prev-FY match) stay gated.
	if !strings.Contains(body, "Needs ≥2 confirmed payslips") {
		t.Errorf("YTD section should still be gated with 1 payslip")
	}
	if !strings.Contains(body, "Needs a matching payslip") {
		t.Errorf("YoY section should still be gated without prev-FY match")
	}
	// Annual summary section's gating must NOT fire — it only needs ≥1.
	if strings.Contains(body, "Needs ≥1 confirmed payslip") {
		t.Errorf("Annual summary should NOT be gated with 1 confirmed payslip")
	}
}

func TestAnnualRoute_NavLinkPresentOnDashboard(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	rec, _ := doGet(srv, "/")
	body := rec.Body.String()
	if !strings.Contains(body, `href="/annual"`) {
		t.Errorf("Annual nav link missing from dashboard; got: %s", body[:min(300, len(body))])
	}
	if !strings.Contains(body, `aria-label="Annual"`) {
		t.Errorf("Annual nav link text missing")
	}
}

func TestAnnualRoute_YTDCumulativeChartPresent(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	// Seed ≥2 confirmed payslips in FY 2025-26 so the YTD gate opens.
	seedConfirmedPayslip(t, srv, 4, 2025, 50000, 5000, 45000)
	seedConfirmedPayslip(t, srv, 5, 2025, 50000, 5000, 45000)
	seedConfirmedPayslip(t, srv, 1, 2026, 52000, 5000, 47000)

	rec, _ := doGet(srv, "/annual?fy=2025")
	if rec.Code != 200 {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// YTD section should now render the chart canvas.
	if !strings.Contains(body, `id="ytd-cumulative-chart"`) {
		t.Errorf("missing YTD cumulative chart canvas; got: %s", body[:min(500, len(body))])
	}
	// The gating notice should NOT fire when ≥2 payslips exist.
	if strings.Contains(body, "Needs ≥2 confirmed payslips") {
		t.Errorf("YTD section should not be gated with ≥2 payslips in FY")
	}
}

func TestAnnualRoute_YTDCumulativeGatedBelowTwoPayslips(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	// Single confirmed payslip in FY 2025-26 → YTD gate stays closed.
	seedConfirmedPayslip(t, srv, 4, 2025, 50000, 5000, 45000)

	rec, _ := doGet(srv, "/annual?fy=2025")
	if rec.Code != 200 {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, `id="ytd-cumulative-chart"`) {
		t.Errorf("YTD chart canvas should not render below 2 payslips in FY")
	}
	if !strings.Contains(body, "Needs ≥2 confirmed payslips") {
		t.Errorf("missing YTD gating notice")
	}
}

func TestAnnualRoute_SummaryTablePresent(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	// One confirmed payslip in FY 2025-26 is enough to open the summary gate.
	seedConfirmedPayslip(t, srv, 4, 2025, 50000, 5000, 45000)

	rec, _ := doGet(srv, "/annual?fy=2025")
	if rec.Code != 200 {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// Table header cells.
	for _, want := range []string{"Component", "FY total", "Trend", "vs prev FY"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing summary table header %q; got: %s", want, body[:min(500, len(body))])
		}
	}
	// The basic component's display name appears in a row.
	if !strings.Contains(body, "Basic") {
		t.Errorf("missing 'Basic' row in summary table")
	}
	// Sparkline SVG present.
	if !strings.Contains(body, "<svg") {
		t.Errorf("missing sparkline svg in summary table")
	}
	// Footer totals render.
	for _, want := range []string{"Gross", "Deductions", "Net"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing footer total %q", want)
		}
	}
}

func TestAnnualRoute_SummaryTableGatedWithZeroConfirmed(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	rec, _ := doGet(srv, "/annual?fy=2025")
	if rec.Code != 200 {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Needs ≥1 confirmed payslip") {
		t.Errorf("missing summary gating notice")
	}
	// Footer totals must NOT render when the table is gated.
	for _, unwanted := range []string{">Gross<", ">Deductions<", ">Net<"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("footer %q should not render when summary is gated", unwanted)
		}
	}
}

func TestAnnualRoute_SummaryDeltaDashForMissingPrevFY(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	// Single payslip in FY 2025-26; no payslips in FY 2024-25. Delta column
	// should render the em-dash placeholder rather than "0".
	seedConfirmedPayslip(t, srv, 4, 2025, 50000, 5000, 45000)

	rec, _ := doGet(srv, "/annual?fy=2025")
	if rec.Code != 200 {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "—") {
		t.Errorf("missing em-dash placeholder for delta without prev FY data")
	}
}

func TestAnnualRoute_YoYSlopegraphPresent(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	// Latest: July 2026 (FY 2026-27). Prev-FY same month: July 2025 (FY 2025-26).
	// The /annual page defaults to the latest payslip's FY (2026-27), and YoY
	// looks at the previous FY (2025-26) for the same month.
	seedConfirmedPayslip(t, srv, 7, 2025, 50000, 5000, 45000)
	seedConfirmedPayslip(t, srv, 7, 2026, 60000, 5000, 55000)

	rec, _ := doGet(srv, "/annual")
	if rec.Code != 200 {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// Slopegraph SVG present.
	if !strings.Contains(body, `id="yoy-slopegraph"`) {
		t.Errorf("missing yoy-slopegraph SVG element; got: %s", body[:min(500, len(body))])
	}
	// The gating notice should NOT fire when a prev-FY match exists.
	if strings.Contains(body, "Needs a matching payslip") {
		t.Errorf("YoY section should not be gated when prev-FY match exists")
	}
	// At least one component name from seeded data appears in the slopegraph.
	if !strings.Contains(body, "Basic") {
		t.Errorf("missing 'Basic' in slopegraph rows")
	}
}

func TestAnnualRoute_YoYSlopegraphGated(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	// Only one payslip, no prev-FY match → YoY gate stays closed.
	seedConfirmedPayslip(t, srv, 7, 2026, 50000, 5000, 45000)

	rec, _ := doGet(srv, "/annual")
	if rec.Code != 200 {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, `id="yoy-slopegraph"`) {
		t.Errorf("slopegraph should not render without prev-FY match")
	}
	if !strings.Contains(body, "Needs a matching payslip") {
		t.Errorf("missing YoY gating notice")
	}
}

func TestDashboard_TwoConfirmed_ShowsMoM(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	// Previous month: net 45000.
	seedConfirmedPayslip(t, srv, 6, 2026, 50000, 5000, 45000)
	// Latest month: net 50000 — Basic +10000.
	seedConfirmedPayslip(t, srv, 7, 2026, 60000, 5000, 50000)

	rec, _ := doGet(srv, "/")
	if rec.Code != 200 {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// MoM delta shown: +5000.
	if !strings.Contains(body, "+&#8377;<span class=\"money\">5,000</span>") {
		t.Errorf("missing +5000 delta; got first 300 chars: %s", body[:min(300, len(body))])
	}
	// "Why did it change" callout present.
	if !strings.Contains(body, "Net went up") {
		t.Errorf("missing 'net went up' callout")
	}
	// Basic delta +10000 shown in callout (display name "Basic", not slug "basic").
	if !strings.Contains(body, "Basic") {
		t.Errorf("missing Basic in component deltas")
	}
}

func TestDashboard_DrillDownLinksUseCanonicalIDs(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	seedConfirmedPayslip(t, srv, 7, 2026, 50000, 5000, 45000)

	rec, _ := doGet(srv, "/")
	body := rec.Body.String()
	// Drill-down rows should link to /component/{id} via onclick.
	if !strings.Contains(body, `/component/`) {
		t.Errorf("missing drill-down link; got: %s", body[:min(200, len(body))])
	}
}

func TestDashboard_SparklineColumnInTable(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	// Two months of data is enough for a sparkline (one point per month).
	seedConfirmedPayslip(t, srv, 6, 2026, 50000, 5000, 45000)
	seedConfirmedPayslip(t, srv, 7, 2026, 50000, 5000, 45000)

	rec, _ := doGet(srv, "/")
	if rec.Code != 200 {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// An SVG sparkline should appear inside the component table.
	if !strings.Contains(body, "<svg") {
		t.Errorf("missing <svg> sparkline in component table; got: %s", body[:min(400, len(body))])
	}
	// The table header should announce the new column.
	if !strings.Contains(body, "Trend") {
		t.Errorf("missing 'Trend' column header in component table")
	}
	// Existing columns are still there.
	for _, want := range []string{"Name", "Category", "Latest", "Months"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing existing column header %q", want)
		}
	}
}

func TestDashboard_SparklineAnomalyColoredWithAccent(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	// Seed 4 months of basic with a large jump on the latest month:
	// deltas = [+1000, +0, +50000] — latest is way over the mean abs delta.
	// IsAnomalous returns true; the sparkline's latest point must use
	// --chart-accent.
	seedConfirmedPayslipFull(t, srv, 4, 2026,
		[]compSeed{{"basic", 49000, store.CategoryEarning}, {"tds", 5000, store.CategoryDeduction}},
		49000, 5000, 44000)
	seedConfirmedPayslipFull(t, srv, 5, 2026,
		[]compSeed{{"basic", 50000, store.CategoryEarning}, {"tds", 5000, store.CategoryDeduction}},
		50000, 5000, 45000)
	seedConfirmedPayslipFull(t, srv, 6, 2026,
		[]compSeed{{"basic", 50000, store.CategoryEarning}, {"tds", 5000, store.CategoryDeduction}},
		50000, 5000, 45000)
	seedConfirmedPayslipFull(t, srv, 7, 2026,
		[]compSeed{{"basic", 100000, store.CategoryEarning}, {"tds", 5000, store.CategoryDeduction}},
		100000, 5000, 95000)

	rec, _ := doGet(srv, "/")
	if rec.Code != 200 {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// The anomalous latest point references --chart-accent.
	if !strings.Contains(body, "var(--chart-accent)") {
		t.Errorf("anomalous sparkline point should reference --chart-accent; got first 500 chars: %s", body[:min(500, len(body))])
	}
}

func TestDashboard_SparklineNoAccentWhenNotAnomalous(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	// Steady deltas: [+1000, +1000, +1000]. Mean abs = 1000, latest = 1000;
	// latest is NOT > 2*mean → not anomalous. The chart-accent token must
	// not appear inside any sparkline mark.
	seedConfirmedPayslipFull(t, srv, 4, 2026,
		[]compSeed{{"basic", 48000, store.CategoryEarning}, {"tds", 5000, store.CategoryDeduction}},
		48000, 5000, 43000)
	seedConfirmedPayslipFull(t, srv, 5, 2026,
		[]compSeed{{"basic", 49000, store.CategoryEarning}, {"tds", 5000, store.CategoryDeduction}},
		49000, 5000, 44000)
	seedConfirmedPayslipFull(t, srv, 6, 2026,
		[]compSeed{{"basic", 50000, store.CategoryEarning}, {"tds", 5000, store.CategoryDeduction}},
		50000, 5000, 45000)
	seedConfirmedPayslipFull(t, srv, 7, 2026,
		[]compSeed{{"basic", 51000, store.CategoryEarning}, {"tds", 5000, store.CategoryDeduction}},
		51000, 5000, 46000)

	rec, _ := doGet(srv, "/")
	if rec.Code != 200 {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// Sparklines still render (<svg> present), but no chart-accent.
	if !strings.Contains(body, "<svg") {
		t.Errorf("expected sparkline svgs even without anomaly")
	}
	if strings.Contains(body, "var(--chart-accent)") {
		t.Errorf("non-anomalous sparkline should not reference --chart-accent")
	}
}

func TestComponentDetail_NotFound(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	rec, _ := doGet(srv, "/component/9999")
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestComponentDetail_InvalidID(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	rec, _ := doGet(srv, "/component/abc")
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestComponentDetail_RendersChart(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	seedConfirmedPayslip(t, srv, 7, 2026, 50000, 5000, 45000)
	ctx := context.Background()
	basic, _ := srv.store.FindCanonicalByName(ctx, "basic")

	rec, _ := doGet(srv, "/component/"+itoa(basic.ID))
	if rec.Code != 200 {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Amount over time") {
		t.Errorf("missing chart header")
	}
	if !strings.Contains(body, `id="component-chart"`) {
		t.Errorf("missing chart canvas")
	}
	// Headline numbers from the only data point (Basic = 50000).
	if !strings.Contains(body, "&#8377;<span class=\"money\">50,000</span>") {
		t.Errorf("missing latest amount; got: %s", body[:min(300, len(body))])
	}
	// Monthly contributions table replaces the old raw-labels aggregate.
	// Each row links to the source payslip and shows the raw label inline.
	if !strings.Contains(body, "Monthly contributions") {
		t.Errorf("missing monthly contributions section")
	}
	if !strings.Contains(body, ">Basic<") {
		t.Errorf("missing raw label 'Basic'")
	}
}

func TestComponentDetail_ToggleControlPresent(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	seedConfirmedPayslip(t, srv, 7, 2026, 50000, 5000, 45000)
	ctx := context.Background()
	basic, _ := srv.store.FindCanonicalByName(ctx, "basic")

	rec, _ := doGet(srv, "/component/"+itoa(basic.ID))
	if rec.Code != 200 {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// Toggle control present — either a button or checkbox with an id the
	// client script hooks into. We assert on the well-known id so the JS
	// contract stays stable.
	if !strings.Contains(body, `id="ytd-toggle"`) {
		t.Errorf("missing YTD toggle control with id=ytd-toggle; got: %s", body[:min(500, len(body))])
	}
	// The toggle has a label that mentions YTD.
	if !strings.Contains(body, "YTD") {
		t.Errorf("missing 'YTD' label on toggle control")
	}
}

func TestComponentDetail_YTDSeriesEmittedForClientToggle(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	// Seed two confirmed months so YTD actually accumulates across points.
	seedConfirmedPayslip(t, srv, 6, 2026, 50000, 5000, 45000)
	seedConfirmedPayslip(t, srv, 7, 2026, 50000, 5000, 45000)
	ctx := context.Background()
	basic, _ := srv.store.FindCanonicalByName(ctx, "basic")

	rec, _ := doGet(srv, "/component/"+itoa(basic.ID))
	if rec.Code != 200 {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// The template must hand the client a YTD series so the toggle can
	// switch without a round-trip. We assert the YTDAmt field is present in
	// the embedded JSON (basecoat chart helper embeds the points array).
	if !strings.Contains(body, "YTDAmt") {
		t.Errorf("missing YTDAmt field in embedded points JSON; toggle cannot work without YTD data")
	}
}

func TestComponentDetail_NoData(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	// 'lwf' is in the seed vocabulary but unused by any confirmed payslip.
	rec, _ := doGet(srv, "/component/18") // lwf — seeded last-ish but deterministic
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("status = %d, body: %s", rec.Code, body)
	}
	if !strings.Contains(body, "No data yet") {
		t.Errorf("unused canonical should show empty state; got: %s", body[:min(200, len(body))])
	}
}

func TestServePDF_Missing(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	p := seedPayslip(t, srv)
	// RawPDFPath is empty by default → 404.
	rec, _ := doGet(srv, "/pdf/"+itoa(p.ID))
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestServePDF_Exists(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	// Save a fake PDF via the PDFStore and store the relative path on the
	// payslip. SavePayslip is the right path: UpdatePayslip intentionally
	// doesn't touch raw_pdf_path (the PDF is immutable once uploaded;
	// review edits change parsed values only).
	rel, err := srv.pdfs.Save("fake.pdf", strings.NewReader("%PDF-1.4 fake"))
	if err != nil {
		t.Fatalf("pdfs.Save: %v", err)
	}
	ctx := context.Background()
	basic, _ := srv.store.FindCanonicalByName(ctx, "basic")
	tds, _ := srv.store.FindCanonicalByName(ctx, "tds")
	p := store.Payslip{
		EmployerName:   "Acme Corp",
		PayPeriodMonth: 7,
		PayPeriodYear:  2026,
		RawPDFPath:     rel,
		Components: []store.Component{
			{CanonicalID: basic.ID, RawLabel: "Basic", Amount: 40000, Category: store.CategoryEarning},
			{CanonicalID: tds.ID, RawLabel: "TDS", Amount: 5000, Category: store.CategoryDeduction},
		},
	}
	if err := srv.store.SavePayslip(ctx, &p); err != nil {
		t.Fatalf("SavePayslip: %v", err)
	}

	rec, _ := doGet(srv, "/pdf/"+itoa(p.ID))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("Content-Type = %q, want application/pdf", ct)
	}
	if !strings.HasPrefix(rec.Body.String(), "%PDF-1.4") {
		t.Errorf("body does not start with PDF header")
	}
}

func TestServePDF_NotFound(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	rec, _ := doGet(srv, "/pdf/9999")
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestUpload_NoFile(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	// Empty multipart body (no file field).
	body := strings.NewReader("")
	req := httptest.NewRequest(http.MethodPost, "/upload", body)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=----x")
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200 (re-renders upload page with error)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Upload failed") {
		t.Errorf("missing upload failure message")
	}
}

func TestUpload_RealPDF_SkipsIfNoLMStudio(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	// Find the prototype payslip PDF (if present).
	repoRoot := findRepoRoot(t)
	pdfPath := filepath.Join(repoRoot, "payslip-1784482033.pdf")
	if _, err := os.Stat(pdfPath); err != nil {
		t.Skipf("test PDF not found: %s", pdfPath)
	}

	// Build a multipart upload request with the real PDF.
	body := &strings.Builder{}
	w := multipart.NewWriter(body)
	part, err := w.CreateFormFile("pdf", "payslip.pdf")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	f, err := os.Open(pdfPath)
	if err != nil {
		t.Fatalf("open pdf: %v", err)
	}
	defer f.Close()
	if _, err := io.Copy(part, f); err != nil {
		t.Fatalf("copy: %v", err)
	}
	w.Close()

	req := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader(body.String()))
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	// Without a live LMStudio, the LLM step fails and we re-render the upload
	// page with an error. That's the correct behavior; if LMStudio IS running,
	// we'd get a 303 redirect to /review/{id}.
	if rec.Code == http.StatusSeeOther {
		t.Skip("LMStudio appears to be running; upload succeeded. Skipping assertion.")
	}
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200 (error page) or 303 (success)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Upload failed") {
		t.Errorf("missing upload failure message; body: %s", rec.Body.String()[:min(200, len(rec.Body.String()))])
	}
}

func TestParseComponentsFromForm_Minimal(t *testing.T) {
	// Direct unit test of the form parser, bypassing HTTP.
	req := httptest.NewRequest(http.MethodPost, "/review/1", strings.NewReader(
		"comp_0_raw_label=Basic&comp_0_category=earning&comp_0_canonical=1&comp_0_amount=100&comp_0_ytd=1200"+
			"&comp_1_raw_label=TDS&comp_1_category=deduction&comp_1_canonical=2&comp_1_amount=20&comp_1_ytd=240"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := req.ParseForm(); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}
	comps, err := parseComponentsFromForm(req)
	if err != nil {
		t.Fatalf("parseComponentsFromForm: %v", err)
	}
	if len(comps) != 2 {
		t.Fatalf("got %d comps, want 2", len(comps))
	}
	if comps[0].RawLabel != "Basic" || comps[0].Category != store.CategoryEarning {
		t.Errorf("comp[0] = %+v", comps[0])
	}
	if comps[1].Category != store.CategoryDeduction {
		t.Errorf("comp[1].Category = %q", comps[1].Category)
	}
}

func TestParseComponentsFromForm_NoRows(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/review/1", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.ParseForm()
	if _, err := parseComponentsFromForm(req); err == nil {
		t.Error("expected error for empty form")
	}
}

// itoa is a tiny local int64→string helper to avoid pulling in strconv everywhere.
func itoa(n int64) string {
	return strings.TrimSpace(strings.Replace(strings.Replace(
		strings.ToLower(strings.TrimSpace(string(rune('0'+n)))), // only safe for tiny test IDs
		"", "", -1), "", "", -1))
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not find go.mod")
	return ""
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestDeletePayslip_Handler_RemovesRowAndPDF(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	rel, err := srv.pdfs.Save("fake.pdf", strings.NewReader("%PDF-1.4 fake"))
	if err != nil {
		t.Fatalf("pdfs.Save: %v", err)
	}
	ctx := context.Background()
	basic, _ := srv.store.FindCanonicalByName(ctx, "basic")
	tds, _ := srv.store.FindCanonicalByName(ctx, "tds")
	p := store.Payslip{
		EmployerName:   "Acme Corp",
		PayPeriodMonth: 7,
		PayPeriodYear:  2026,
		RawPDFPath:     rel,
		Components: []store.Component{
			{CanonicalID: basic.ID, RawLabel: "Basic", Amount: 40000, Category: store.CategoryEarning},
			{CanonicalID: tds.ID, RawLabel: "TDS", Amount: 5000, Category: store.CategoryDeduction},
		},
	}
	if err := srv.store.SavePayslip(ctx, &p); err != nil {
		t.Fatalf("SavePayslip: %v", err)
	}
	if !srv.pdfs.Exists(rel) {
		t.Fatalf("precondition: PDF file missing on disk")
	}

	rec := doPostForm(srv, "/payslip/"+itoa(p.ID)+"/delete", "")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body: %s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/payslips") {
		t.Errorf("redirect = %q, want /payslips...", loc)
	}

	if _, err := srv.store.GetPayslip(ctx, p.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetPayslip after delete: err = %v, want ErrNotFound", err)
	}
	if srv.pdfs.Exists(rel) {
		t.Errorf("PDF file still on disk after delete")
	}
}

func TestDeletePayslip_Handler_MissingPDFNotError(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	ctx := context.Background()
	basic, _ := srv.store.FindCanonicalByName(ctx, "basic")
	tds, _ := srv.store.FindCanonicalByName(ctx, "tds")
	p := store.Payslip{
		EmployerName:   "Acme Corp",
		PayPeriodMonth: 7,
		PayPeriodYear:  2026,
		RawPDFPath:     "never_existed.pdf",
		Components: []store.Component{
			{CanonicalID: basic.ID, RawLabel: "Basic", Amount: 40000, Category: store.CategoryEarning},
			{CanonicalID: tds.ID, RawLabel: "TDS", Amount: 5000, Category: store.CategoryDeduction},
		},
	}
	if err := srv.store.SavePayslip(ctx, &p); err != nil {
		t.Fatalf("SavePayslip: %v", err)
	}

	rec := doPostForm(srv, "/payslip/"+itoa(p.ID)+"/delete", "")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; missing PDF must not fail the delete", rec.Code)
	}
	if _, err := srv.store.GetPayslip(ctx, p.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("row not deleted: err = %v", err)
	}
}

func TestDeletePayslip_Handler_NotFound(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	rec := doPostForm(srv, "/payslip/9999/delete", "")
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestPayslipsList_Empty(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	rec, _ := doGet(srv, "/payslips")
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Upload your first payslip") {
		t.Errorf("missing never-uploaded empty state; got: %s", body[:min(400, len(body))])
	}
}

func TestPayslipsList_RendersCards(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	seedConfirmedPayslip(t, srv, 7, 2026, 50000, 5000, 45000)

	rec, _ := doGet(srv, "/payslips")
	if rec.Code != 200 {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Acme Corp") {
		t.Errorf("missing employer name on card")
	}
	if !strings.Contains(body, "July 2026") {
		t.Errorf("missing period label on card")
	}
	if !strings.Contains(body, "&#8377;<span class=\"money\">45,000</span>") {
		t.Errorf("missing net pay on card")
	}
	// Card links to the detail page.
	if !strings.Contains(body, `href="/payslip/`) {
		t.Errorf("missing detail link on card")
	}
}

func TestPayslipsList_NewestPeriodFirst(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	seedConfirmedPayslip(t, srv, 3, 2026, 50000, 5000, 45000)
	seedConfirmedPayslip(t, srv, 7, 2026, 50000, 5000, 45000)

	rec, _ := doGet(srv, "/payslips")
	body := rec.Body.String()
	julyIdx := strings.Index(body, "July 2026")
	marchIdx := strings.Index(body, "March 2026")
	if julyIdx < 0 || marchIdx < 0 {
		t.Fatalf("missing periods; got: %s", body[:min(400, len(body))])
	}
	if julyIdx > marchIdx {
		t.Errorf("July should appear before March (newest first)")
	}
}

func TestPayslipsList_StatusFilter(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	// One pending, one confirmed.
	seedConfirmedPayslip(t, srv, 6, 2026, 50000, 5000, 45000)
	seedPayslip(t, srv) // pending July 2026

	rec, _ := doGet(srv, "/payslips?status=pending_review")
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	// Pending payslip's period (July 2026) should appear; confirmed (June) should not.
	if !strings.Contains(body, "July 2026") {
		t.Errorf("pending filter missing July payslip")
	}
	if strings.Contains(body, "June 2026") {
		t.Errorf("pending filter should exclude confirmed June payslip")
	}
}

func TestPayslipsList_FilterChips(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	seedConfirmedPayslip(t, srv, 6, 2026, 50000, 5000, 45000)
	seedPayslip(t, srv) // pending

	rec, _ := doGet(srv, "/payslips")
	body := rec.Body.String()
	// Three chips with counts.
	if !strings.Contains(body, "All") {
		t.Errorf("missing All chip")
	}
	if !strings.Contains(body, "Pending") {
		t.Errorf("missing Pending chip")
	}
	if !strings.Contains(body, "Failed") {
		t.Errorf("missing Failed chip")
	}
	// Counts render next to chip labels.
	if !strings.Contains(body, "(1)") {
		t.Errorf("missing count (1) for pending; got: %s", body)
	}
}

func TestPayslipsList_FilteredEmpty(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	// Only confirmed payslips, no failed.
	seedConfirmedPayslip(t, srv, 7, 2026, 50000, 5000, 45000)

	rec, _ := doGet(srv, "/payslips?status=failed")
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "All caught up") {
		t.Errorf("missing filtered-empty state; got: %s", body[:min(400, len(body))])
	}
}

func TestPayslipsList_DeleteButtonOnCard(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	seedConfirmedPayslip(t, srv, 7, 2026, 50000, 5000, 45000)

	rec, _ := doGet(srv, "/payslips")
	body := rec.Body.String()
	if !strings.Contains(body, `/payslip/`) {
		t.Errorf("missing delete form action")
	}
	if !strings.Contains(body, `class="alert-dialog"`) {
		t.Errorf("missing alert-dialog confirmation on delete")
	}
}

func TestPayslipDetail_PendingRendersEditable(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	// Build a pending payslip with a real PDF on disk, so the embed renders.
	rel, err := srv.pdfs.Save("fake.pdf", strings.NewReader("%PDF-1.4 fake"))
	if err != nil {
		t.Fatalf("pdfs.Save: %v", err)
	}
	ctx := context.Background()
	basic, _ := srv.store.FindCanonicalByName(ctx, "basic")
	tds, _ := srv.store.FindCanonicalByName(ctx, "tds")
	p := store.Payslip{
		EmployerName:   "Acme Corp",
		PayPeriodMonth: 7,
		PayPeriodYear:  2026,
		NetPay:         45000,
		GrossSalary:    50000,
		RawPDFPath:     rel,
		Components: []store.Component{
			{CanonicalID: basic.ID, RawLabel: "Basic", Amount: 40000, Category: store.CategoryEarning},
			{CanonicalID: tds.ID, RawLabel: "TDS", Amount: 5000, Category: store.CategoryDeduction},
		},
	}
	if err := srv.store.SavePayslip(ctx, &p); err != nil {
		t.Fatalf("SavePayslip: %v", err)
	}

	rec, _ := doGet(srv, "/payslip/"+itoa(p.ID))
	if rec.Code != 200 {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `value="Acme Corp"`) {
		t.Errorf("pending: missing editable employer field")
	}
	if !strings.Contains(body, `value="confirm"`) {
		t.Errorf("pending: missing confirm button")
	}
	if !strings.Contains(body, `src="/pdf/`) {
		t.Errorf("pending: missing PDF embed")
	}
}

func TestPayslipDetail_ConfirmedRendersReadOnly(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	p := seedConfirmedPayslip(t, srv, 7, 2026, 50000, 5000, 45000)

	rec, _ := doGet(srv, "/payslip/"+itoa(p.ID))
	if rec.Code != 200 {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// Read-only mode: no live form inputs for the scalar fields.
	if strings.Contains(body, `name="employer"`) {
		t.Errorf("confirmed: should not render editable employer input")
	}
	if strings.Contains(body, `value="confirm"`) {
		t.Errorf("confirmed: should not show confirm button (already confirmed)")
	}
	// Edit unlock button is present.
	if !strings.Contains(body, "Edit") {
		t.Errorf("confirmed: missing Edit unlock button")
	}
	// Data is still visible (read-only text).
	if !strings.Contains(body, "Acme Corp") {
		t.Errorf("confirmed: missing employer text")
	}
}

func TestPayslipDetail_FailedRendersErrorAndRetry(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	ctx := context.Background()
	p := seedPayslip(t, srv)
	if err := srv.store.MarkPayslipFailed(ctx, p.ID, "LLM exploded"); err != nil {
		t.Fatalf("MarkPayslipFailed: %v", err)
	}

	rec, _ := doGet(srv, "/payslip/"+itoa(p.ID))
	if rec.Code != 200 {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "LLM exploded") {
		t.Errorf("failed: missing error message")
	}
	if !strings.Contains(body, "Retry") {
		t.Errorf("failed: missing Retry button")
	}
}

func TestPayslipDetail_PDFMissingNote(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	// Payslip references a PDF that doesn't exist on disk.
	ctx := context.Background()
	basic, _ := srv.store.FindCanonicalByName(ctx, "basic")
	tds, _ := srv.store.FindCanonicalByName(ctx, "tds")
	p := store.Payslip{
		EmployerName:   "Acme Corp",
		PayPeriodMonth: 7,
		PayPeriodYear:  2026,
		RawPDFPath:     "ghost.pdf",
		Components: []store.Component{
			{CanonicalID: basic.ID, RawLabel: "Basic", Amount: 40000, Category: store.CategoryEarning},
			{CanonicalID: tds.ID, RawLabel: "TDS", Amount: 5000, Category: store.CategoryDeduction},
		},
	}
	if err := srv.store.SavePayslip(ctx, &p); err != nil {
		t.Fatalf("SavePayslip: %v", err)
	}

	rec, _ := doGet(srv, "/payslip/"+itoa(p.ID))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Source PDF not found") {
		t.Errorf("missing PDF-not-found note; got: %s", body[:min(400, len(body))])
	}
}

func TestPayslipDetail_NotFound(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	rec, _ := doGet(srv, "/payslip/9999")
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestPayslipDetail_InvalidID(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	rec, _ := doGet(srv, "/payslip/abc")
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestPayslipDetail_HasDeleteButton(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	p := seedPayslip(t, srv)

	rec, _ := doGet(srv, "/payslip/"+itoa(p.ID))
	body := rec.Body.String()
	if !strings.Contains(body, `/payslip/`+itoa(p.ID)+`/delete`) {
		t.Errorf("missing delete form action on detail page")
	}
	if !strings.Contains(body, `class="alert-dialog"`) {
		t.Errorf("missing alert-dialog confirmation on detail delete")
	}
}

func TestPayslipDetail_SubmitEdits(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	p := seedPayslip(t, srv)

	form := strings.Join([]string{
		"employer=Acme Renamed",
		"designation=",
		"month=7",
		"year=2026",
		"employee_id=",
		"pay_days=30",
		"total_days=30",
		"gross_salary=50000",
		"total_deductions=5000",
		"net_pay=45000",
		"comp_0_raw_label=Basic",
		"comp_0_category=earning",
		"comp_0_canonical=1",
		"comp_0_amount=40000",
		"comp_0_ytd=280000",
		"comp_1_raw_label=TDS",
		"comp_1_category=deduction",
		"comp_1_canonical=15",
		"comp_1_amount=5000",
		"comp_1_ytd=35000",
		"action=save",
	}, "&")
	rec := doPostForm(srv, "/payslip/"+itoa(p.ID), form)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d; body: %s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "/payslip/"+itoa(p.ID)+"?toast=Draft+saved") {
		t.Errorf("redirect = %q, want /payslip/%d?toast=Draft+saved...", loc, p.ID)
	}
}

func TestPayslipDetail_Skip(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	p := seedPayslip(t, srv)
	rec, _ := doGet(srv, "/payslip/"+itoa(p.ID)+"/skip")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/payslips?status=pending_review") {
		t.Errorf("skip with no next: redirect = %q, want /payslips?status=pending_review...", loc)
	}
}

func TestPayslipDetail_Retry(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	ctx := context.Background()
	p := seedPayslip(t, srv)
	if err := srv.store.MarkPayslipFailed(ctx, p.ID, "broken"); err != nil {
		t.Fatalf("MarkPayslipFailed: %v", err)
	}

	rec := doPostForm(srv, "/payslip/"+itoa(p.ID)+"/retry", "")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d; body: %s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/payslips?status=pending_review") {
		t.Errorf("retry redirect = %q, want /payslips?status=pending_review...", loc)
	}
}

func TestPayslipDetail_RetryNonFailedRedirects(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	p := seedPayslip(t, srv) // pending, not failed

	rec := doPostForm(srv, "/payslip/"+itoa(p.ID)+"/retry", "")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/payslip/"+itoa(p.ID)) {
		t.Errorf("non-failed retry redirect = %q, want /payslip/%d", loc, p.ID)
	}
}

func TestReviewRoutes_Dropped(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	// /review/{id} is no longer registered. Go's mux falls back to "GET /"
	// (the dashboard), so the response is 200 with the dashboard — not the
	// detail page. Assert the body doesn't contain the detail-page marker.
	rec, _ := doGet(srv, "/review/1")
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200 (catch-all dashboard)", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "Parsed data") {
		t.Errorf("/review/1 should not render the detail page; got Parsed data section")
	}
}

func TestPayslipDetail_ConfirmedEditUnlock(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	p := seedConfirmedPayslip(t, srv, 7, 2026, 50000, 5000, 45000)

	// ?edit=1 on a confirmed payslip unlocks the form.
	rec, _ := doGet(srv, "/payslip/"+itoa(p.ID)+"?edit=1")
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `name="employer"`) {
		t.Errorf("?edit=1 should render editable form for confirmed payslip")
	}
}

func TestPayslipDetail_SubmitConfirm(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	p := seedPayslip(t, srv)

	form := strings.Join([]string{
		"employer=Acme Renamed",
		"designation=SDE II",
		"month=7",
		"year=2026",
		"employee_id=E1",
		"pay_days=30",
		"total_days=30",
		"gross_salary=50000",
		"total_deductions=5000",
		"net_pay=45000",
		"comp_0_raw_label=Basic Pay",
		"comp_0_category=earning",
		"comp_0_canonical=1",
		"comp_0_amount=41000",
		"comp_0_ytd=287000",
		"comp_1_raw_label=TDS",
		"comp_1_category=deduction",
		"comp_1_canonical=15",
		"comp_1_amount=5000",
		"comp_1_ytd=35000",
		"action=confirm",
	}, "&")
	rec := doPostForm(srv, "/payslip/"+itoa(p.ID), form)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body: %s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/payslips?status=pending_review") && !strings.HasPrefix(loc, "/payslip/") {
		t.Errorf("redirect = %q, want /payslips?status=pending_review... or /payslip/{next}...", loc)
	}
	got, _ := srv.store.GetPayslip(context.Background(), p.ID)
	if got.Status != store.StatusConfirmed {
		t.Errorf("status = %q, want confirmed", got.Status)
	}
	if got.EmployerName != "Acme Renamed" {
		t.Errorf("employer = %q, want Acme Renamed", got.EmployerName)
	}
}

func TestPayslipDetail_SubmitBadCanonical(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	p := seedPayslip(t, srv)
	form := strings.Join([]string{
		"employer=X",
		"month=1&year=2026",
		"pay_days=30&total_days=30",
		"gross_salary=1&total_deductions=0&net_pay=1",
		"comp_0_raw_label=Basic",
		"comp_0_category=earning",
		"comp_0_canonical=not-a-number",
		"comp_0_amount=1&comp_0_ytd=1",
		"action=save",
	}, "&")
	rec := doPostForm(srv, "/payslip/"+itoa(p.ID), form)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestPayslipDetail_SubmitNotFound(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	rec := doPostForm(srv, "/payslip/9999", "action=confirm")
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestNav_ActiveState(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	seedConfirmedPayslip(t, srv, 7, 2026, 50000, 5000, 45000)

	tests := []struct {
		name       string
		path       string
		wantActive string // href of the link that should carry aria-current
	}{
		{"dashboard", "/", "/"},
		{"annual", "/annual", "/annual"},
		{"payslips list", "/payslips", "/payslips"},
		{"payslip detail", "/payslip/1", "/payslips"},
		{"review filter", "/payslips?status=pending_review", "/payslips?status=pending_review"},
		{"upload no active", "/upload", ""},
		{"component drilldown", "/component/1", "/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, _ := doGet(srv, tt.path)
			if rec.Code >= 500 {
				t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			// The active link must carry aria-current="page".
			if tt.wantActive == "" {
				if strings.Contains(body, `aria-current="page"`) {
					t.Errorf("path %q: no nav item should be active, but found aria-current", tt.path)
				}
				return
			}
			// Find the link with the expected href and check it has aria-current.
			// The href appears in an <a> tag; the aria-current is on the same tag.
			if !hasActiveLink(body, tt.wantActive) {
				t.Errorf("path %q: missing aria-current on link %q", tt.path, tt.wantActive)
			}
		})
	}
}

// hasActiveLink reports whether body contains an <a> tag with the given href
// AND aria-current="page" on the same tag. Searches for aria-current first,
// then checks the href appears within the same tag window — avoids false
// matches on the logo link (which also hrefs "/" but is never active).
func hasActiveLink(body, href string) bool {
	for {
		idx := strings.Index(body, `aria-current="page"`)
		if idx < 0 {
			return false
		}
		// Look in a window around the aria-current attribute for the href.
		start := idx - 200
		if start < 0 {
			start = 0
		}
		end := idx + len(`aria-current="page"`) + 40
		if end > len(body) {
			end = len(body)
		}
		if strings.Contains(body[start:end], `href="`+href+`"`) {
			return true
		}
		body = body[idx+1:]
	}
}

func TestNav_HasPayslipsAndReviewLinks(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	rec, _ := doGet(srv, "/")
	body := rec.Body.String()
	if !strings.Contains(body, `href="/payslips"`) {
		t.Errorf("missing Payslips nav link")
	}
	if !strings.Contains(body, `href="/payslips?status=pending_review"`) {
		t.Errorf("missing Review nav link (should point to /payslips?status=pending_review)")
	}
	if strings.Contains(body, `href="/review"`) {
		t.Errorf("stale /review nav link should be gone")
	}
}

func TestNav_UploadInRail(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	rec, _ := doGet(srv, "/")
	body := rec.Body.String()
	if !strings.Contains(body, `href="/upload"`) {
		t.Errorf("missing Upload link in rail")
	}
	if !strings.Contains(body, `aria-label="Upload payslip"`) {
		t.Errorf("missing Upload aria-label")
	}
}

func TestPortfolio_NotConnected_ShowsEmptyState(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	rec, _ := doGet(srv, "/portfolio")
	if rec.Code != 200 {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `class="empty"`) {
		t.Errorf("not-connected state should use .empty pattern; got: %s", body[:min(400, len(body))])
	}
	if !strings.Contains(body, "Connect your broker accounts") {
		t.Errorf("missing connect CTA heading")
	}
	if !strings.Contains(body, `href="/groww/connect"`) {
		t.Errorf("missing Groww connect link")
	}
	if !strings.Contains(body, `href="/kite/connect"`) {
		t.Errorf("missing Kite connect link")
	}
}

func TestPortfolio_GrowwSessionExpired_ShowsReconnect(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	if err := srv.groww.SaveExpiredTokenForTest(); err != nil {
		t.Fatalf("SaveExpiredTokenForTest: %v", err)
	}
	rec, _ := doGet(srv, "/portfolio")
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Session expired") {
		t.Errorf("expired session should show 'Session expired' in status strip; got: %s", body[:min(400, len(body))])
	}
	if !strings.Contains(body, "Reconnect") {
		t.Errorf("expired session should show Reconnect link")
	}
}

func TestPortfolio_NavItemPresent(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	rec, _ := doGet(srv, "/")
	body := rec.Body.String()
	if !strings.Contains(body, `href="/portfolio"`) {
		t.Errorf("missing Portfolio nav link")
	}
}

func TestGroww_DisconnectRedirects(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()
	rec, _ := doGet(srv, "/groww/disconnect")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/portfolio") {
		t.Errorf("redirect = %q, want /portfolio...", loc)
	}
}
