package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"math"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"cresto/internal/config"
	"cresto/internal/groww"
	"cresto/internal/kite"
	"cresto/internal/llm"
	"cresto/internal/pdfstore"
	"cresto/internal/store"

	"github.com/dustin/go-humanize"
)

//go:embed templates static
var contentFS embed.FS

// tmplFuncs are the formatting helpers exposed to every page template.
// Kept deliberately small — most presentation is done with Basecoat + Tailwind
// utility classes, not template-side transforms.
var tmplFuncs = template.FuncMap{
	"money":            formatMoney,
	"money2":           formatMoney2,
	"monthName":        monthName,
	"monthShort":       monthShort,
	"periodLabel":      periodLabel,
	"urlquery":         url.QueryEscape,
	"json":             toJSONJS,
	"abs":              abs,
	"neg":              neg,
	"sign":             sign,
	"mul":              mulFloat,
	"inc":              incInt,
	"dec":              decInt,
	"sparklineSVG":     sparklineSVG,
	"yoySlopegraphSVG": yoySlopegraphSVG,
	"reversePoints":    reversePoints,
	"canonName":        canonName,
}

// incInt / decInt add/subtract 1 from an int. Used by the annual page's
// prev/next FY nav so the template can stay arithmetic-free.
func incInt(n int) int { return n + 1 }
func decInt(n int) int { return n - 1 }

// mulFloat multiplies two float64s. Used by the portfolio table to compute
// per-row value (LTP × qty) and invested (avgPrice × qty) for the data
// attributes that drive client-side totals recomputation on filter.
func mulFloat(a, b float64) float64 { return a * b }

// reversePoints returns a copy of the slice in reverse order. Used by the
// component detail page: the chart wants chronological (oldest first),
// the monthly contributions table wants newest first.
func reversePoints(points []store.ComponentPoint) []store.ComponentPoint {
	if len(points) == 0 {
		return points
	}
	out := make([]store.ComponentPoint, len(points))
	for i, p := range points {
		out[len(points)-1-i] = p
	}
	return out
}

// canonName finds the canonical with the given ID in a list. Used by the
// read-only payslip detail view to render the canonical's display name for
// each component — the dropdown list is category-scoped, so the template
// can't just take the first element.
func canonName(canons []store.Canonical, id int64) store.Canonical {
	for _, c := range canons {
		if c.ID == id {
			return c
		}
	}
	return store.Canonical{}
}

// payslipCrumbHref returns the breadcrumb link for the "Payslips" segment on
// the detail page. Review-queue payslips (pending mode) link back to the
// filtered review list; confirmed or failed payslips link to the general list.
func payslipCrumbHref(mode string) string {
	if mode == "pending" {
		return "/payslips?status=pending_review"
	}
	return "/payslips"
}

// activePeriodPreset determines which period preset button (if any) matches
// the current filter values. Returns "this-fy", "last-fy", "all", or ""
// (custom range / no filter).
func activePeriodPreset(f store.Filter) string {
	if f.MonthFrom == 0 && f.YearFrom == 0 && f.MonthTo == 0 && f.YearTo == 0 {
		return "all"
	}
	thisFY := currentFYStartYear()
	if f.MonthFrom == 4 && f.MonthTo == 3 {
		if f.YearFrom == thisFY && f.YearTo == thisFY+1 {
			return "this-fy"
		}
		if f.YearFrom == thisFY-1 && f.YearTo == thisFY {
			return "last-fy"
		}
	}
	return ""
}

// sparklineSVG renders an inline SVG sparkline of the given amounts.
// Width/height are fixed (72×20) so the column stays compact; the line auto-
// scales to the data's min/max. When accentLatest is true, the rightmost
// point is drawn in the chart accent color so the eye lands on the latest
// observation. When points have less than two entries, no line is drawn (a
// single dot would be misleading without a baseline).
//
// Returns template.HTML so html/template emits the SVG verbatim — without
// that, the angle brackets would be escaped and the SVG would render as text.
func sparklineSVG(points []float64, accentLatest bool) template.HTML {
	if len(points) == 0 {
		return template.HTML("")
	}
	const (
		w = 72
		h = 20
		pad = 2
	)
	min, max := points[0], points[0]
	for _, p := range points[1:] {
		if p < min {
			min = p
		}
		if p > max {
			max = p
		}
	}
	span := max - min
	if span == 0 {
		span = 1 // avoid divide-by-zero; everything lands on the midline
	}
	// Map point i → (x, y) inside the padded box.
	xFor := func(i int) float64 {
		if len(points) == 1 {
			return float64(w) / 2
		}
		return pad + float64(i)*(float64(w)-2*pad)/float64(len(points)-1)
	}
	yFor := func(v float64) float64 {
		return pad + (1 - (v-min)/span) * float64(h-2*pad)
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<svg width="%d" height="%d" viewBox="0 0 %d %d" preserveAspectRatio="none" aria-hidden="true">`, w, h, w, h)
	// Polyline first (so dots overlay on top).
	if len(points) >= 2 {
		b.WriteString(`<polyline points="`)
		for i, p := range points {
			if i > 0 {
				b.WriteString(" ")
			}
			fmt.Fprintf(&b, "%.1f,%.1f", xFor(i), yFor(p))
		}
		b.WriteString(`" fill="none" stroke="var(--color-muted-foreground)" stroke-width="1" stroke-linejoin="round" stroke-linecap="round" />`)
	}
	// Dots — the latest gets the accent color when accentLatest is true.
	last := len(points) - 1
	for i, p := range points {
		x, y := xFor(i), yFor(p)
		if accentLatest && i == last {
			fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="2" fill="var(--chart-anomaly, var(--chart-accent))" />`, x, y)
			continue
		}
		fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="1.5" fill="var(--color-muted-foreground)" />`, x, y)
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

// yoySlopegraphSVG renders a row-based slopegraph as an inline SVG. Each
// component gets its own horizontal band at a fixed y-position — lines never
// cross, and labels never overlap. Within each band, the left and right dots
// are positioned by their respective values so the line slope encodes the
// direction and magnitude of change. The component with the largest absolute
// delta is highlighted in the chart accent; zero-delta rows are faded to
// reduce visual noise. Long component names are truncated to keep labels
// readable.
//
// Returns template.HTML so the SVG emits verbatim (same pattern as
// sparklineSVG).
func yoySlopegraphSVG(rows []slopegraphRow) template.HTML {
	if len(rows) == 0 {
		return template.HTML("")
	}
	const (
		w         = 600
		rowH      = 36
		padTop    = 24
		padBottom = 16
		leftX     = 120
		rightX    = w - 120
		maxLabel  = 14
	)
	h := padTop + padBottom + len(rows)*rowH

	focalID := int64(-1)
	if len(rows) > 0 {
		focalID = rows[0].CanonicalID
	}

	offsetRange := float64(rowH) * 0.35

	var b strings.Builder
	fmt.Fprintf(&b, `<svg id="yoy-slopegraph" width="%d" height="%d" viewBox="0 0 %d %d" class="w-full" aria-label="Year-over-year slopegraph">`, w, h, w, h)

	for i, r := range rows {
		rowCenterY := float64(padTop) + float64(i)*float64(rowH) + float64(rowH)/2

		minV := math.Min(r.PrevFYAmount, r.LatestAmount)
		maxV := math.Max(r.PrevFYAmount, r.LatestAmount)
		var yPrev, yLatest float64
		if maxV == minV {
			yPrev = rowCenterY
			yLatest = rowCenterY
		} else {
			yPrev = rowCenterY + offsetRange/2 - (r.PrevFYAmount-minV)/(maxV-minV)*offsetRange
			yLatest = rowCenterY + offsetRange/2 - (r.LatestAmount-minV)/(maxV-minV)*offsetRange
		}

		isZero := r.Delta == 0
		if isZero {
			yPrev = rowCenterY
			yLatest = rowCenterY
		}

		stroke := "var(--color-muted-foreground)"
		strokeWidth := "1"
		opacity := "0.35"
		if r.CanonicalID == focalID {
			stroke = "var(--chart-accent)"
			strokeWidth = "2"
			opacity = "1"
		} else if !isZero {
			opacity = "0.7"
		}

		fmt.Fprintf(&b, `<line x1="%d" y1="%.1f" x2="%d" y2="%.1f" stroke="%s" stroke-width="%s" stroke-linecap="round" opacity="%s"/>`,
			leftX, yPrev, rightX, yLatest, stroke, strokeWidth, opacity)

		fmt.Fprintf(&b, `<circle cx="%d" cy="%.1f" r="3" fill="%s" opacity="%s"/>`, leftX, yPrev, stroke, opacity)

		name := truncateLabel(r.Name, maxLabel)
		fmt.Fprintf(&b, `<text x="%d" y="%.1f" text-anchor="end" font-size="11" fill="var(--color-foreground)" opacity="%s">`, leftX-8, rowCenterY+4, opacity)
		b.WriteString(template.HTMLEscapeString(name))
		b.WriteString(`</text>`)
		fmt.Fprintf(&b, `<text x="%d" y="%.1f" text-anchor="end" font-size="10" fill="var(--color-muted-foreground)" font-family="ui-monospace, monospace" opacity="%s" class="money">₹`,
			leftX-8, rowCenterY+16, opacity)
		b.WriteString(formatMoneyPlain(r.PrevFYAmount))
		b.WriteString(`</text>`)

		fmt.Fprintf(&b, `<circle cx="%d" cy="%.1f" r="3" fill="%s" opacity="%s"/>`, rightX, yLatest, stroke, opacity)

		deltaSign := "+"
		deltaColor := "var(--color-success)"
		if r.Delta < 0 {
			deltaSign = ""
			deltaColor = "var(--color-destructive)"
		}
		rightName := name
		if r.Label != "" {
			rightName = r.Label + " " + rightName
			rightName = truncateLabel(rightName, maxLabel+5)
		}
		fmt.Fprintf(&b, `<text x="%d" y="%.1f" text-anchor="start" font-size="11" fill="var(--color-foreground)" opacity="%s">`, rightX+8, rowCenterY+4, opacity)
		b.WriteString(template.HTMLEscapeString(rightName))
		b.WriteString(`</text>`)
		fmt.Fprintf(&b, `<text x="%d" y="%.1f" text-anchor="start" font-size="10" fill="%s" font-family="ui-monospace, monospace" opacity="%s" class="money">₹`,
			rightX+8, rowCenterY+16, deltaColor, opacity)
		b.WriteString(deltaSign)
		b.WriteString(formatMoneyPlain(abs(r.Delta)))
		b.WriteString(`</text>`)
	}

	fmt.Fprintf(&b, `<text x="%d" y="%d" text-anchor="middle" font-size="10" fill="var(--color-muted-foreground)" font-weight="600">PREV FY</text>`, leftX, padTop-6)
	fmt.Fprintf(&b, `<text x="%d" y="%d" text-anchor="middle" font-size="10" fill="var(--color-muted-foreground)" font-weight="600">LATEST</text>`, rightX, padTop-6)

	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

func truncateLabel(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}

// toJSONJS marshals v to JSON and returns it as template.JS so html/template
// emits it verbatim into a <script> block. Used to hand chart series to the
// dashboard's Chart.js calls without a separate fetch round-trip.
func toJSONJS(v any) (template.JS, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return template.JS(b), nil
}

// abs returns the absolute value of v. Used by the dashboard template so a
// negative net-pay delta is rendered as "+/- ₹X" with the sign controlled by
// the template, not the formatting.
func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// neg reports whether v is negative. Template comparison functions can't
// compare float64 with an int literal (0), so this bridges the gap for
// P&L colour/sign logic without precomputing bools on every holding.
func neg(v float64) bool { return v < 0 }

// sign returns "+" for non-negative values and "" for negative ones. Used
// by the Groww holdings table to prefix positive P&L amounts — the same
// float64-vs-int limitation that neg addresses applies to the ge builtin.
func sign(v float64) string {
	if v < 0 {
		return ""
	}
	return "+"
}

// Breadcrumb is one segment of the TopBar's breadcrumb trail. The last
// segment (empty Href) is the current page, rendered as plain text.
type Breadcrumb struct {
	Label string
	Href  string
}

// pageData is the common payload every page renders into the layout.
// Embed it in page-specific data structs so the nav can show the pending badge
// and mark the active nav item via aria-current.
type pageData struct {
	Title          string
	PendingCount   int
	ActiveBatchID  string
	// Active marks the current nav item: "dashboard", "annual", "payslips",
	// "review", or "" (none — e.g. upload pages are actions, not places).
	Active string
	// Breadcrumbs drives the TopBar trail. Empty = just show Title.
	// Detail pages set this (e.g. Payslips > July 2026); list pages leave it empty.
	Breadcrumbs []Breadcrumb
	// ContainerClass overrides the layout's default max-width (max-w-6xl).
	// Pages with wide content (e.g. payslip review form) can set "max-w-7xl".
	ContainerClass string
}

// LLMClient is the interface web needs from the LLM layer. The concrete
// *llm.Client satisfies it; tests inject a fake so they never hit a real
// LM Studio server. Two adapters justify this seam (production + test).
type LLMClient interface {
	Health() string
	Extract(img []byte) (*llm.Extraction, error)
	Classify(ext *llm.Extraction, canonicals []llm.CanonicalRef) (*llm.Classification, error)
	Start()
	Stop()
	UpdateConfig(baseURL, model, apiKey string)
}

// Server is the HTTP layer. Construct with New; one instance serves all routes.
type Server struct {
	store     *store.Store
	llmClient LLMClient
	cfg       config.Config
	pdfs      *pdfstore.Store
	groww     *groww.Client
	kite      *kite.Client
	pages     map[string]*template.Template
}

// New wires the server's dependencies but does not start listening. The caller
// owns the store, LLMClient, and pdfs lifetimes; closing them is the caller's job.
func New(s *store.Store, client LLMClient, cfg config.Config, pdfs *pdfstore.Store, growwClient *groww.Client, kiteClient *kite.Client) (*Server, error) {
	pageNames := []string{"dashboard", "upload", "batch_progress", "payslip_detail", "payslips_list", "component_detail", "annual", "error", "portfolio", "settings"}
	pages := make(map[string]*template.Template, len(pageNames))
	for _, name := range pageNames {
		t, err := template.New("").Funcs(tmplFuncs).ParseFS(contentFS,
			"templates/layout.html",
			"templates/pages/"+name+".html")
		if err != nil {
			return nil, fmt.Errorf("parse template %q: %w", name, err)
		}
		pages[name] = t
	}
	return &Server{store: s, llmClient: client, cfg: cfg, pdfs: pdfs, groww: growwClient, kite: kiteClient, pages: pages}, nil
}

// formatMoney renders a float as a grouped decimal, dropping the fractional
// part when it's .00. The output is wrapped in <span class="money"> so the
// privacy-mode CSS can blur all amounts with one selector. Returns
// template.HTML (not string) so the span renders as markup, not escaped text.
func formatMoney(v float64) template.HTML {
	return template.HTML(`<span class="money">` + formatMoneyPlain(v) + `</span>`)
}

// formatMoneyPlain is the underlying number formatter without the privacy span.
// Used by internal callers that build SVG/strings (sparklines, slopegraphs)
// where the span would break the output.
func formatMoneyPlain(v float64) string {
	if v == float64(int64(v)) {
		return humanize.Comma(int64(v))
	}
	return humanize.Commaf(v)
}

// formatMoney2 formats a float with comma grouping and exactly 2 decimal
// places, wrapped in the privacy span. Used by the broker holdings tables
// where prices always show decimals (unlike payslip amounts which drop .00).
func formatMoney2(v float64) template.HTML {
	return template.HTML(`<span class="money">` + humanize.CommafWithDigits(v, 2) + `</span>`)
}

func monthName(m int) string {
	if m < 1 || m > 12 {
		return ""
	}
	return time.Month(m).String()
}

func monthShort(m int) string {
	if m < 1 || m > 12 {
		return ""
	}
	return time.Month(m).String()[:3]
}

// periodLabel renders (month, year) as "July 2026". Returns an em dash when
// either is missing (pending payslips where the LLM couldn't parse the period).
func periodLabel(m, y int) string {
	if m == 0 || y == 0 {
		return "—"
	}
	return fmt.Sprintf("%s %d", monthName(m), y)
}

// Routes returns an *http.ServeMux with all routes registered. Mount it directly.
func (s *Server) Routes() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /", s.handleDashboard)
	mux.HandleFunc("GET /upload", s.handleUploadForm)
	mux.HandleFunc("POST /upload", s.handleUpload)
	mux.HandleFunc("GET /upload/batch/{id}", s.handleBatchProgress)
	mux.HandleFunc("GET /api/batch/{id}", s.handleBatchStatusAPI)
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /api/llm/status", s.handleLLMStatus)
	mux.HandleFunc("GET /payslips", s.handlePayslipsList)
	mux.HandleFunc("GET /payslip/{id}", s.handleReviewDetail)
	mux.HandleFunc("POST /payslip/{id}", s.handleReviewSubmit)
	mux.HandleFunc("GET /payslip/{id}/skip", s.handleReviewSkip)
	mux.HandleFunc("POST /payslip/{id}/retry", s.handleRetry)
	mux.HandleFunc("POST /payslip/{id}/delete", s.handleDeletePayslip)
	mux.HandleFunc("POST /payslips/delete-failed", s.handleDeleteFailed)
	mux.HandleFunc("GET /component/{id}", s.handleComponentDetail)
	mux.HandleFunc("GET /annual", s.handleAnnual)
	mux.HandleFunc("GET /pdf/{id}", s.handleServePDF)

	// Groww integration: connect/disconnect action routes. The /groww page
	// is replaced by /portfolio (PF-63); these action routes stay.
	mux.HandleFunc("GET /groww/connect", s.handleGrowwConnect)
	mux.HandleFunc("GET /groww/disconnect", s.handleGrowwDisconnect)

	// Kite integration: connect/disconnect action routes. Same as Groww.
	mux.HandleFunc("GET /kite/connect", s.handleKiteConnect)
	mux.HandleFunc("GET /kite/disconnect", s.handleKiteDisconnect)

	// Portfolio: consolidated view of all broker holdings (PF-60). Replaces
	// the separate /groww and /kite pages.
	mux.HandleFunc("GET /portfolio", s.handlePortfolio)
	mux.HandleFunc("GET /api/portfolio/holdings", s.handlePortfolioHoldingsAPI)

	// Settings: broker + LLM connection management (PF-64).
	mux.HandleFunc("GET /settings", s.handleSettings)
	mux.HandleFunc("POST /settings/llm", s.handleSettingsLLM)

	// Static assets: CSS, JS. fs.Sub scopes the embed to /static.
	staticFS, err := fs.Sub(contentFS, "static")
	if err != nil {
		// embed.FS sub of an existing dir cannot fail at init; if it does,
		// it's a build-time mistake worth panicking on.
		panic(fmt.Sprintf("static sub: %v", err))
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	return mux
}

// render writes the page into the layout. On template error, it logs and sends
// a 500 — we never partial-write a broken page.
func (s *Server) render(w http.ResponseWriter, page string, data any) {
	if err := s.pages[page].ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) renderError(w http.ResponseWriter, status int, msg string) {
	w.WriteHeader(status)
	s.render(w, "error", struct {
		pageData
		Status  int
		Message string
	}{
		pageData: pageData{Title: "Error", ActiveBatchID: ""},
		Status:   status,
		Message:  msg,
	})
}

// pendingCount is computed per request for the nav badge. Cheap query (indexed column).
func (s *Server) pendingCount(ctx context.Context) int {
	ps, err := s.store.ListPendingReview(ctx)
	if err != nil {
		return 0
	}
	return len(ps)
}

func (s *Server) activeBatchID(ctx context.Context) string {
	return s.store.GetActiveBatchID(ctx)
}

// --- handlers ---

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pending, _ := s.store.ListPendingReview(ctx)
	confirmed, err := s.store.GetConfirmedTimeline(ctx)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Could not load dashboard: "+err.Error())
		return
	}
	canonicals, err := s.store.ListCanonicals(ctx)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Could not load canonicals: "+err.Error())
		return
	}
	view := buildDashboardView(confirmed, canonicals)
	s.render(w, "dashboard", struct {
		pageData
		Dashboard dashboardView
		Pending   []store.Payslip
	}{
		pageData:  pageData{Title: "Dashboard", PendingCount: len(pending), ActiveBatchID: s.activeBatchID(ctx), Active: "dashboard"},
		Dashboard: view,
		Pending:   pending,
	})
}

// handlePayslipsList is the canonical browse view: every payslip on one page,
// filterable by status via query string. Default sort is newest-period first
// (ListPayslips's built-in ORDER BY). The page is the IA replacement for the
// /review queue — Review is now just /payslips?status=pending_review.
func (s *Server) handlePayslipsList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	status := r.URL.Query().Get("status")
	employer := r.URL.Query().Get("employer")
	var filter store.Filter
	switch store.Status(status) {
	case store.StatusPendingReview:
		filter.Status = store.StatusPendingReview
	case store.StatusFailed:
		filter.Status = store.StatusFailed
	default:
		// Unknown or absent status → show all. Reset so the template doesn't
		// echo a junk value back into the active chip.
		status = ""
	}
	if employer != "" {
		filter.Employer = employer
	}
	if yf := r.URL.Query().Get("year_from"); yf != "" {
		filter.YearFrom, _ = strconv.Atoi(yf)
	}
	if yt := r.URL.Query().Get("year_to"); yt != "" {
		filter.YearTo, _ = strconv.Atoi(yt)
	}
	if mf := r.URL.Query().Get("month_from"); mf != "" {
		filter.MonthFrom, _ = strconv.Atoi(mf)
	}
	if mt := r.URL.Query().Get("month_to"); mt != "" {
		filter.MonthTo, _ = strconv.Atoi(mt)
	}

	// Pagination.
	const pageSize = 21
	page := 1
	if p := r.URL.Query().Get("page"); p != "" {
		page, _ = strconv.Atoi(p)
		if page < 1 {
			page = 1
		}
	}
	filter.Limit = pageSize
	filter.Offset = (page - 1) * pageSize

	payslips, err := s.store.ListPayslips(ctx, filter)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Could not load payslips: "+err.Error())
		return
	}

	totalCount, err := s.store.CountPayslips(ctx, filter)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Could not count payslips: "+err.Error())
		return
	}
	totalPages := (totalCount + pageSize - 1) / pageSize

	// Chip counts: three cheap queries against an indexed column.
	all, _ := s.store.ListPayslips(ctx, store.Filter{})
	pending, _ := s.store.ListPendingReview(ctx)
	failed, _ := s.store.ListFailed(ctx)

	// Distinct employers for the company dropdown.
	employers, err := s.store.ListEmployers(ctx)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Could not load employers: "+err.Error())
		return
	}

	// Distinct years for the period filter dropdown.
	years, err := s.store.ListYears(ctx)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Could not load years: "+err.Error())
		return
	}

	// Active nav: "review" when the pending filter is active, else "payslips".
	active := "payslips"
	if store.Status(status) == store.StatusPendingReview {
		active = "review"
	}

	// Determine which period preset (if any) is active based on the filter values.
	periodPreset := activePeriodPreset(filter)

	s.render(w, "payslips_list", struct {
		pageData
		Payslips       []store.Payslip
		StatusFilter   string
		EmployerFilter string
		Employers      []string
		Years          []int
		YearFrom       int
		YearTo         int
		MonthFrom      int
		MonthTo        int
		PeriodPreset   string
		CountAll       int
		CountPending   int
		CountFailed    int
		Page           int
		TotalPages     int
		TotalCount     int
	}{
		pageData:       pageData{Title: "Payslips", PendingCount: len(pending), ActiveBatchID: s.activeBatchID(ctx), Active: active},
		Payslips:       payslips,
		StatusFilter:   status,
		EmployerFilter: employer,
		Employers:      employers,
		Years:          years,
		Page:           page,
		TotalPages:     totalPages,
		TotalCount:     totalCount,
		YearFrom:       filter.YearFrom,
		YearTo:         filter.YearTo,
		MonthFrom:      filter.MonthFrom,
		MonthTo:        filter.MonthTo,
		PeriodPreset:   periodPreset,
		CountAll:       len(all),
		CountPending:   len(pending),
		CountFailed:    len(failed),
	})
}

// handleComponentDetail renders the per-canonical drill-down: a line chart of
// the canonical's amount over time plus the raw labels that have been mapped
// to it. Confirmed payslips only — pending data would be noise here.
func (s *Server) handleComponentDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	canonicals, err := s.store.ListCanonicals(ctx)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Could not load canonicals: "+err.Error())
		return
	}
	var canonical store.Canonical
	for _, c := range canonicals {
		if c.ID == id {
			canonical = c
			break
		}
	}
	if canonical.ID == 0 {
		s.renderError(w, http.StatusNotFound, fmt.Sprintf("Canonical #%d not found.", id))
		return
	}
	points, err := s.store.GetComponentTimeline(ctx, id, "")
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Could not load component timeline: "+err.Error())
		return
	}
	rawLabels, err := s.store.GetRawLabelUsage(ctx, id)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Could not load raw labels: "+err.Error())
		return
	}

	// Pre-compute the headline so the template doesn't have to slice/index.
	type headline struct {
		HasPoints bool
		LatestAmt float64
		LatestAt  string
		FirstAt   string
		Count     int
	}
	hd := headline{Count: len(points)}
	if len(points) > 0 {
		hd.HasPoints = true
		last := points[len(points)-1]
		first := points[0]
		hd.LatestAmt = last.Amount
		hd.LatestAt = periodLabel(last.PayPeriodMonth, last.PayPeriodYear)
		hd.FirstAt = periodLabel(first.PayPeriodMonth, first.PayPeriodYear)
	}

	pending, _ := s.store.ListPendingReview(ctx)
	s.render(w, "component_detail", struct {
		pageData
		Canonical store.Canonical
		Points    []store.ComponentPoint
		RawLabels []store.RawLabelUsage
		Headline  headline
	}{
		pageData:  pageData{Title: canonical.DisplayName(), PendingCount: len(pending), ActiveBatchID: s.activeBatchID(ctx), Active: "dashboard", Breadcrumbs: []Breadcrumb{{Label: "Dashboard", Href: "/"}, {Label: canonical.DisplayName()}}},
		Canonical: canonical,
		Points:    points,
		RawLabels: rawLabels,
		Headline:  hd,
	})
}

// uploadPageData is the view model for the upload page. Shared by the
// initial render and the error re-render so the template's fields
// (LLM status pill) stay populated in both paths.
type uploadPageData struct {
	pageData
	Error        string
	LLMStatus    string
	LLMBaseURL   string
	LLMModelName string
}

func (s *Server) handleUploadForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, "upload", uploadPageData{
		pageData:     pageData{Title: "Upload Payslip", PendingCount: s.pendingCount(r.Context()), ActiveBatchID: s.activeBatchID(r.Context())},
		LLMStatus:    s.llmClient.Health(),
		LLMBaseURL:   s.cfg.LMStudioBaseURL,
		LLMModelName: s.cfg.ModelName,
	})
}

// handleLLMStatus reports LM Studio reachability and model availability
// as JSON for the upload page's status pill polling.
func (s *Server) handleLLMStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":    s.llmClient.Health(),
		"baseURL":   s.cfg.LMStudioBaseURL,
		"modelName": s.cfg.ModelName,
	})
}

// handleBatchProgress renders the live progress page for a bulk upload. The
// template auto-refreshes while the batch is in flight (processed + failed <
// total), then shows a "Review all" link once every PDF has been attempted.
func (s *Server) handleBatchProgress(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	batchID := r.PathValue("id")
	if batchID == "" {
		s.renderError(w, http.StatusBadRequest, "Missing batch id.")
		return
	}
	batch, err := s.store.GetBatch(ctx, batchID)
	if err != nil {
		s.renderError(w, http.StatusNotFound, "Batch not found.")
		return
	}
	// Payslips for this batch, newest-first (matches review queue's display order
	// pre-reverse; here we just want to list what landed).
	payslips, err := s.store.ListPayslips(ctx, store.Filter{BatchID: batchID})
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Could not load batch payslips: "+err.Error())
		return
	}
	attempted := batch.ProcessedCount + batch.FailedCount
	done := attempted >= batch.Total
	current := attempted + 1
	if current > batch.Total {
		current = batch.Total
	}
	pct := 0
	if batch.Total > 0 {
		pct = attempted * 100 / batch.Total
	}
	s.render(w, "batch_progress", struct {
		pageData
		Batch     store.Batch
		Payslips  []store.Payslip
		Done      bool
		Current   int
		Attempted int
		Pct       int
		Pending   int
	}{
		pageData:  pageData{Title: "Batch Progress", PendingCount: s.pendingCount(ctx), ActiveBatchID: s.activeBatchID(ctx)},
		Batch:     batch,
		Payslips:  payslips,
		Done:      done,
		Current:   current,
		Attempted: attempted,
		Pct:       pct,
		Pending:   len(payslips),
	})
}

// batchStatusJSON is the JSON payload returned by the batch status API.
type batchStatusJSON struct {
	Done         bool              `json:"done"`
	Total        int               `json:"total"`
	Processed    int               `json:"processed"`
	Failed       int               `json:"failed"`
	Pct          int               `json:"pct"`
	CurrentFile  string            `json:"current_file"`
	CurrentStage string            `json:"current_stage"`
	Payslips     []payslipSummary  `json:"payslips"`
}

type payslipSummary struct {
	ID           int64   `json:"id"`
	EmployerName string  `json:"employer_name"`
	PeriodLabel  string  `json:"period_label"`
	NetPay       float64 `json:"net_pay"`
	Status       string  `json:"status"`
	ErrorMessage string  `json:"error_message"`
}

func (s *Server) handleBatchStatusAPI(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	batchID := r.PathValue("id")
	if batchID == "" {
		http.Error(w, "missing batch id", http.StatusBadRequest)
		return
	}
	batch, err := s.store.GetBatch(ctx, batchID)
	if err != nil {
		http.Error(w, "batch not found", http.StatusNotFound)
		return
	}
	attempted := batch.ProcessedCount + batch.FailedCount
	done := attempted >= batch.Total
	pct := 0
	if batch.Total > 0 {
		pct = attempted * 100 / batch.Total
	}

	payslips, _ := s.store.ListPayslips(ctx, store.Filter{BatchID: batchID})
	summaries := make([]payslipSummary, 0, len(payslips))
	for _, p := range payslips {
		summaries = append(summaries, payslipSummary{
			ID:           p.ID,
			EmployerName: p.EmployerName,
			PeriodLabel:  periodLabel(p.PayPeriodMonth, p.PayPeriodYear),
			NetPay:       p.NetPay,
			Status:       string(p.Status),
			ErrorMessage: p.ErrorMessage,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(batchStatusJSON{
		Done:         done,
		Total:        batch.Total,
		Processed:    batch.ProcessedCount,
		Failed:       batch.FailedCount,
		Pct:          pct,
		CurrentFile:  batch.CurrentFile,
		CurrentStage: batch.CurrentStage,
		Payslips:     summaries,
	})
}

// handleDeletePayslip hard-deletes a payslip: DB row (cascade removes
// components) and the source PDF file on disk if it exists. Missing PDF is
// not an error — the file may have been removed out-of-band. Always redirects
// to /payslips; the list view will preserve any filter via its query string.
func (s *Server) handleDeletePayslip(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	p, err := s.store.GetPayslip(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.renderError(w, http.StatusNotFound, "Payslip not found.")
			return
		}
		s.renderError(w, http.StatusInternalServerError, "Could not load payslip: "+err.Error())
		return
	}
	// Remove the PDF first — if this fails we still want the DB row gone so
	// the user isn't stuck looking at a dangling payslip. A missing file is
	// silently OK (already removed out-of-band).
	if p.RawPDFPath != "" && s.pdfs.Exists(p.RawPDFPath) {
		_ = os.Remove(s.pdfs.Abs(p.RawPDFPath))
	}
	if err := s.store.DeletePayslip(ctx, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.renderError(w, http.StatusNotFound, "Payslip not found.")
			return
		}
		s.renderError(w, http.StatusInternalServerError, "Could not delete payslip: "+err.Error())
		return
	}
	http.Redirect(w, r, "/payslips?toast=Deleted&variant=success", http.StatusSeeOther)
}

// handleDeleteFailed removes every failed payslip (DB rows + PDFs on disk).
// Used by the "Delete all failed" affordance on the payslips list when the
// Failed filter is active. Always deletes by status — it is not scoped to
// the current employer/period filter, so the confirmation copy says "all".
func (s *Server) handleDeleteFailed(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	deleted, err := s.store.DeletePayslipsByStatus(ctx, store.StatusFailed)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Could not delete failed payslips: "+err.Error())
		return
	}
	for _, p := range deleted {
		if p.RawPDFPath != "" && s.pdfs.Exists(p.RawPDFPath) {
			_ = os.Remove(s.pdfs.Abs(p.RawPDFPath))
		}
	}
	n := len(deleted)
	http.Redirect(w, r, fmt.Sprintf("/payslips?toast=Deleted+%d+failed+payslips&variant=success", n), http.StatusSeeOther)
}

// handleRetry re-processes a single failed payslip. Marks it processing (so the
// queue shows the retry is underway), kicks off a background goroutine, and
// redirects back to the review queue. The user sees the payslip flip from
// "failed" to "processing" to "pending" as the goroutine completes.
func (s *Server) handleRetry(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	p, err := s.store.GetPayslip(ctx, id)
	if err != nil {
		s.renderError(w, http.StatusNotFound, "Payslip not found.")
		return
	}
	// Only failed payslips are retryable from the UI. A pending/confirmed
	// payslip hitting this route is a stale form or a URL-editing user.
	if p.Status != store.StatusFailed {
		http.Redirect(w, r, fmt.Sprintf("/payslip/%d", id), http.StatusSeeOther)
		return
	}
	if err := s.store.MarkPayslipProcessing(ctx, id); err != nil {
		s.renderError(w, http.StatusInternalServerError, "Could not mark for retry: "+err.Error())
		return
	}
	go s.processRetry(id)
	http.Redirect(w, r, "/payslips?status=pending_review&toast=Reprocessing...&variant=info", http.StatusSeeOther)
}

// handleUpload accepts one or more payslip PDFs. A single upload is processed
// synchronously and redirects straight to the review page (~15s on CPU). Two
// or more trigger a batch: PDFs are saved, a batch row is created, and a
// background goroutine processes them sequentially while the user is redirected
// to a live progress page.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// 10 MB per file — payslip PDFs are typically 50-500 KB. The overall
	// request cap is higher so multi-file batches aren't rejected at parse.
	if err := r.ParseMultipartForm(60 << 20); err != nil {
		s.renderUploadError(w, r, "Could not read upload: "+err.Error())
		return
	}

	// Pull every file field named "pdf". r.MultipartForm.File is a map of
	// field → headers; one field can carry multiple files (input[multiple]).
	headers, ok := r.MultipartForm.File["pdf"]
	if !ok || len(headers) == 0 {
		s.renderUploadError(w, r, "No PDF file provided.")
		return
	}

	// Single-file fast path: process synchronously, land on the review page.
	if len(headers) == 1 {
		s.handleSingleUpload(ctx, w, r, headers[0])
		return
	}

	// Batch path: save all PDFs, kick off background processing.
	batchID := newBatchID()
	pdfPaths := make([]string, 0, len(headers))
	for _, hdr := range headers {
		f, err := hdr.Open()
		if err != nil {
			s.renderUploadError(w, r, fmt.Sprintf("Could not open %s: %v", hdr.Filename, err))
			return
		}
		rel, err := s.pdfs.Save(hdr.Filename, f)
		f.Close()
		if err != nil {
			s.renderUploadError(w, r, err.Error())
			return
		}
		pdfPaths = append(pdfPaths, rel)
	}

	if err := s.store.CreateBatch(ctx, batchID, len(pdfPaths)); err != nil {
		// Clean up the saved PDFs — nothing references them yet.
		for _, rel := range pdfPaths {
			os.Remove(s.pdfs.Abs(rel))
		}
		s.renderUploadError(w, r, "Could not create batch: "+err.Error())
		return
	}

	go s.processBatch(batchID, pdfPaths)

	http.Redirect(w, r, "/upload/batch/"+batchID, http.StatusSeeOther)
}

// handleSingleUpload is the one-PDF path: save → render → extract → save as
// pending_review → redirect to the review page for that payslip.
func (s *Server) handleSingleUpload(ctx context.Context, w http.ResponseWriter, r *http.Request, hdr *multipart.FileHeader) {
	file, err := hdr.Open()
	if err != nil {
		s.renderUploadError(w, r, "Could not open upload: "+err.Error())
		return
	}
	defer file.Close()

	rel, err := s.pdfs.Save(hdr.Filename, file)
	if err != nil {
		s.renderUploadError(w, r, err.Error())
		return
	}

	p, err := s.processPDF(ctx, rel)
	if err != nil {
		os.Remove(s.pdfs.Abs(rel))
		s.renderUploadError(w, r, "LLM extraction failed. Is LMStudio running at "+s.cfg.LMStudioBaseURL+"? Error: "+err.Error())
		return
	}
	p.Status = store.StatusPendingReview

	if err := s.store.SavePayslip(ctx, &p); err != nil {
		os.Remove(s.pdfs.Abs(rel))
		s.renderUploadError(w, r, "Could not save payslip: "+err.Error())
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/payslip/%d", p.ID), http.StatusSeeOther)
}

func (s *Server) renderUploadError(w http.ResponseWriter, r *http.Request, msg string) {
	s.render(w, "upload", uploadPageData{
		pageData:     pageData{Title: "Upload Payslip", PendingCount: s.pendingCount(r.Context()), ActiveBatchID: s.activeBatchID(r.Context())},
		Error:        msg,
		LLMStatus:    s.llmClient.Health(),
		LLMBaseURL:   s.cfg.LMStudioBaseURL,
		LLMModelName: s.cfg.ModelName,
	})
}

// componentRow pairs a component with the canonicals available for its category,
// plus its form index. The review template uses these to render the dropdown.
type componentRow struct {
	Component store.Component
	Index     int
	Canons    []store.Canonical
}

func (s *Server) handleReviewDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	p, err := s.store.GetPayslip(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.renderError(w, http.StatusNotFound, fmt.Sprintf("Payslip #%d not found.", id))
			return
		}
		s.renderError(w, http.StatusInternalServerError, "Could not load payslip: "+err.Error())
		return
	}

	canonicals, err := s.store.ListCanonicals(ctx)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Could not load canonicals: "+err.Error())
		return
	}

	// Split canonicals by category for the dropdowns.
	var earningCanons, deductionCanons []store.Canonical
	for _, c := range canonicals {
		switch c.Category {
		case store.CategoryEarning:
			earningCanons = append(earningCanons, c)
		case store.CategoryDeduction:
			deductionCanons = append(deductionCanons, c)
		}
	}

	// Build rows: components are already ordered earnings-first by GetPayslip.
	rows := make([]componentRow, 0, len(p.Components))
	for i, c := range p.Components {
		canons := earningCanons
		if c.Category == store.CategoryDeduction {
			canons = deductionCanons
		}
		rows = append(rows, componentRow{Component: c, Index: i, Canons: canons})
	}

	pending, _ := s.store.ListPendingReviewChronological(ctx)
	nextID := nextPendingID(pending, id)
	prevID := prevPendingID(pending, id)

	var reviewIndex int // 0 means "not in the pending queue"
	for i, p := range pending {
		if p.ID == id {
			reviewIndex = i + 1
			break
		}
	}

	// Mode-awareness: confirmed renders read-only, pending/failed editable.
	// The "mode" string is also the template's switch key for layout sections.
	// ?edit=1 unlocks the form for a confirmed payslip — the "Edit" button
	// on the read-only view links here with that query param.
	mode := "pending"
	if p.Status == store.StatusConfirmed {
		mode = "confirmed"
	} else if p.Status == store.StatusFailed {
		mode = "failed"
	}
	if r.URL.Query().Get("edit") == "1" && p.Status == store.StatusConfirmed {
		mode = "pending" // treat as editable for rendering
	}

	// PDF presence: detect once here so the template renders a graceful note
	// instead of a broken <embed> when the source file is missing.
	pdfMissing := p.RawPDFPath != "" && !s.pdfs.Exists(p.RawPDFPath)

	s.render(w, "payslip_detail", struct {
		pageData
		Payslip       store.Payslip
		Rows          []componentRow
		NextPendingID int64
		PrevPendingID int64
		Mode          string
		PDFMissing    bool
		ReviewIndex   int
		ReviewTotal   int
	}{
		// Review queue payslips link back to the filtered review list; confirmed
		// or failed payslips link to the general payslips list.
		pageData:      pageData{Title: periodLabel(p.PayPeriodMonth, p.PayPeriodYear), PendingCount: len(pending), ActiveBatchID: s.activeBatchID(ctx), Active: "payslips", ContainerClass: "max-w-7xl", Breadcrumbs: []Breadcrumb{{Label: "Payslips", Href: payslipCrumbHref(mode)}, {Label: periodLabel(p.PayPeriodMonth, p.PayPeriodYear)}}},
		ReviewIndex:   reviewIndex,
		ReviewTotal:   len(pending),
		Payslip:       p,
		Rows:          rows,
		NextPendingID: nextID,
		PrevPendingID: prevID,
		Mode:          mode,
		PDFMissing:    pdfMissing,
	})
}

func (s *Server) handleReviewSubmit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderError(w, http.StatusBadRequest, "Could not parse form: "+err.Error())
		return
	}
	action := r.FormValue("action")

	p, err := s.store.GetPayslip(ctx, id)
	if err != nil {
		s.renderError(w, http.StatusNotFound, "Payslip not found.")
		return
	}

	// Apply editable scalar fields from the form.
	p.EmployerName = normalizeEmployer(strings.TrimSpace(r.FormValue("employer")))
	p.EmployeeID = strings.TrimSpace(r.FormValue("employee_id"))
	p.Designation = strings.TrimSpace(r.FormValue("designation"))
	p.PayPeriodMonth = atoiOr(r.FormValue("month"), p.PayPeriodMonth)
	p.PayPeriodYear = atoiOr(r.FormValue("year"), p.PayPeriodYear)
	p.PayDays = atoiOr(r.FormValue("pay_days"), p.PayDays)
	p.TotalDays = atoiOr(r.FormValue("total_days"), p.TotalDays)
	p.GrossSalary = atofOr(r.FormValue("gross_salary"), p.GrossSalary)
	p.TotalDeductions = atofOr(r.FormValue("total_deductions"), p.TotalDeductions)
	p.NetPay = atofOr(r.FormValue("net_pay"), p.NetPay)

	// Rebuild components from indexed form fields.
	comps, err := parseComponentsFromForm(r)
	if err != nil {
		s.renderError(w, http.StatusBadRequest, "Could not parse components: "+err.Error())
		return
	}
	p.Components = comps

	if err := s.store.UpdatePayslip(ctx, &p); err != nil {
		s.renderError(w, http.StatusInternalServerError, "Could not save edits: "+err.Error())
		return
	}

	if action == "confirm" {
		// Snapshot the pending list BEFORE confirming so nextPendingID can
		// still find the current payslip's position and jump to the one that
		// follows it chronologically.
		pending, _ := s.store.ListPendingReviewChronological(ctx)
		if err := s.store.ConfirmPayslip(ctx, p.ID); err != nil {
			s.renderError(w, http.StatusInternalServerError, "Saved but could not confirm: "+err.Error())
			return
		}
		if next := nextPendingID(pending, p.ID); next > 0 {
			http.Redirect(w, r, fmt.Sprintf("/payslip/%d?toast=Confirmed&variant=success", next), http.StatusSeeOther)
		} else {
			http.Redirect(w, r, "/payslips?status=pending_review&toast=Confirmed&variant=success", http.StatusSeeOther)
		}
		return
	}

	// "save" action: stay on the page with a saved flag.
	http.Redirect(w, r, fmt.Sprintf("/payslip/%d?toast=Draft+saved&variant=success", p.ID), http.StatusSeeOther)
}

func (s *Server) handleReviewSkip(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	pending, _ := s.store.ListPendingReviewChronological(ctx)
	if next := nextPendingID(pending, id); next > 0 {
		http.Redirect(w, r, fmt.Sprintf("/payslip/%d?toast=Skipped&variant=info", next), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/payslips?status=pending_review&toast=Skipped&variant=info", http.StatusSeeOther)
}

func (s *Server) handleServePDF(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	p, err := s.store.GetPayslip(ctx, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if p.RawPDFPath == "" || !s.pdfs.Exists(p.RawPDFPath) {
		http.NotFound(w, r)
		return
	}
	// Inline so the browser renders it in the embed tag.
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=payslip-%d.pdf", p.ID))
	http.ServeFile(w, r, s.pdfs.Abs(p.RawPDFPath))
}

// --- Broker connect/disconnect handlers ---

// userFacingBrokerError translates a broker MCP/API error to a short,
// user-facing message. Raw Go error strings are developer-facing — the
// surface should diagnose the problem in plain language and point toward
// recovery. Shared by the portfolio view and connect/disconnect handlers.
func userFacingBrokerError(brokerName string, err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "unauthorized") || strings.Contains(msg, "401"):
		return brokerName + " rejected the connection. Your session may have expired — try reconnecting."
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "context deadline exceeded"):
		return brokerName + " took too long to respond. Check your internet connection and try again."
	case strings.Contains(msg, "no such host") || strings.Contains(msg, "connection refused"):
		return "Could not reach " + brokerName + "'s servers. The service may be down — try again in a moment."
	case strings.Contains(msg, "no holdings tool found"):
		return brokerName + "'s MCP did not expose a holdings tool. The integration may have changed — check for updates."
	default:
		return "Could not fetch holdings from " + brokerName + ": " + msg
	}
}

// handleGrowwConnect starts the OAuth flow and redirects the user's browser
// to Groww's authorize page. The transient localhost:52155 listener handles
// the callback and redirects back to /portfolio.
func (s *Server) handleGrowwConnect(w http.ResponseWriter, r *http.Request) {
	returnURL := "http://" + r.Host + "/portfolio"
	authURL, err := s.groww.StartAuth(returnURL)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Could not start Groww auth: "+err.Error())
		return
	}
	http.Redirect(w, r, authURL, http.StatusSeeOther)
}

// handleGrowwDisconnect deletes the stored token and redirects to /portfolio.
func (s *Server) handleGrowwDisconnect(w http.ResponseWriter, r *http.Request) {
	if err := s.groww.Disconnect(); err != nil {
		s.renderError(w, http.StatusInternalServerError, "Could not disconnect: "+err.Error())
		return
	}
	http.Redirect(w, r, "/portfolio?toast=Disconnected&variant=info", http.StatusSeeOther)
}

// --- Kite connect/disconnect ---

func (s *Server) handleKiteConnect(w http.ResponseWriter, r *http.Request) {
	authURL, err := s.kite.StartAuth()
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Could not start Kite auth: "+err.Error())
		return
	}
	http.Redirect(w, r, authURL, http.StatusSeeOther)
}

func (s *Server) handleKiteDisconnect(w http.ResponseWriter, r *http.Request) {
	if err := s.kite.Disconnect(); err != nil {
		s.renderError(w, http.StatusInternalServerError, "Could not disconnect: "+err.Error())
		return
	}
	http.Redirect(w, r, "/portfolio?toast=Disconnected&variant=info", http.StatusSeeOther)
}

// --- helpers ---

func parseID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

func atoiOr(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return fallback
	}
	return n
}

func atofOr(s string, fallback float64) float64 {
	if s == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return fallback
	}
	return f
}

// parseComponentsFromForm walks comp_{i}_* fields until a row is missing its
// raw_label field. Category must be present and valid — an invalid category is
// a hard error (form corruption).
func parseComponentsFromForm(r *http.Request) ([]store.Component, error) {
	var comps []store.Component
	for i := 0; ; i++ {
		prefix := fmt.Sprintf("comp_%d_", i)
		rawLabel := r.FormValue(prefix + "raw_label")
		if rawLabel == "" && r.FormValue(prefix+"canonical") == "" {
			break // no more rows
		}
		cat := store.Category(r.FormValue(prefix + "category"))
		if cat != store.CategoryEarning && cat != store.CategoryDeduction {
			return nil, fmt.Errorf("component %d: invalid category %q", i, cat)
		}
		canonID, err := strconv.ParseInt(r.FormValue(prefix+"canonical"), 10, 64)
		if err != nil || canonID <= 0 {
			return nil, fmt.Errorf("component %d: invalid canonical id", i)
		}
		comps = append(comps, store.Component{
			CanonicalID: canonID,
			RawLabel:    rawLabel,
			Amount:      atofOr(r.FormValue(prefix+"amount"), 0),
			YTDAmt:      atofOr(r.FormValue(prefix+"ytd"), 0),
			Category:    cat,
		})
	}
	if len(comps) == 0 {
		return nil, errors.New("no components in form")
	}
	return comps, nil
}

// nextPendingID returns the ID of the payslip immediately after currentID in
// the pending list, or 0 if there isn't one. The pending slice must be in the
// order the user should walk through (chronological — use
// ListPendingReviewChronological). Used to drive the "next" flow in review:
// after confirming/skipping the current payslip, jump to the one that follows
// it in time. Returns 0 when currentID is the last or not in the list, which
// the template renders as "Back to queue".
func nextPendingID(pending []store.Payslip, currentID int64) int64 {
	for i, p := range pending {
		if p.ID == currentID && i+1 < len(pending) {
			return pending[i+1].ID
		}
	}
	return 0
}

// prevPendingID returns the ID of the payslip immediately before currentID in
// the pending list, or 0 if there isn't one. Used for keyboard navigation (k).
func prevPendingID(pending []store.Payslip, currentID int64) int64 {
	for i, p := range pending {
		if p.ID == currentID && i > 0 {
			return pending[i-1].ID
		}
	}
	return 0
}
