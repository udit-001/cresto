package web

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"time"

	"cresto/internal/store"
)

// FinancialYear derives the Indian financial year (April–March) that the given
// calendar (month, year) falls in. Month ≥ 4 → FY = (year, year+1); month ≤ 3
// → FY = (year-1, year). The returned startYear is the April-starting year;
// the label is human-formatted as "FY 2026-27".
//
// The schema stores pay_period_year as the calendar year; FY is derived at
// read time so we never need a schema migration for the April–March semantics.
func FinancialYear(month, year int) (startYear int, label string) {
	if month >= 4 {
		startYear = year
	} else {
		startYear = year - 1
	}
	label = fmt.Sprintf("FY %d-%02d", startYear, (startYear+1)%100)
	return startYear, label
}

// currentFYStartYear returns the start year of the FY containing today's
// date. Used as the default when no payslips exist and no ?fy= query param
// was supplied.
func currentFYStartYear() int {
	now := time.Now().UTC()
	start, _ := FinancialYear(int(now.Month()), now.Year())
	return start
}

// fyLabel is the inverse of FinancialYear's label half: given a start year,
// produce "FY 2026-27". Kept as a small helper so the formatting rule lives
// in one place even when we already have a start year (e.g. parsed from a
// query param).
func fyLabel(startYear int) string {
	return fmt.Sprintf("FY %d-%02d", startYear, (startYear+1)%100)
}

// annualView is the payload the annual template renders. Each section carries
// its own gating flag so the template can show honest empty states per the
// spec — no half-empty charts.
type annualView struct {
	// FY selection + navigation.
	FYStartYear int
	FYLabel     string
	HasPrevFY   bool // there is at least one confirmed payslip in any earlier FY
	HasNextFY   bool // there is at least one confirmed payslip in any later FY

	// Section gates. A section renders its content only when its gate is true;
	// otherwise the template shows the empty-state notice.
	YTDCumulativeOK bool // ≥2 confirmed payslips in this FY
	YoYOK           bool // latest month has a matching payslip in previous FY
	AnnualSummaryOK bool // ≥1 confirmed payslip in this FY

	// Raw counts the template needs for its empty-state copy.
	FYPayslipCount       int
	FYDistinctMonthCount int

	// YTDCumulative is the chart payload when YTDCumulativeOK is true;
	// zero-valued otherwise.
	YTDCumulative ytdCumulative

	// AnnualSummary is the table payload when AnnualSummaryOK is true;
	// zero-valued otherwise.
	AnnualSummary annualSummary

	// YoYSlopegraph is the slopegraph payload when YoYOK is true;
	// zero-valued otherwise.
	YoYSlopegraph []slopegraphRow

	// Canonicals is the vocabulary list — passed through so the template
	// can render display names without re-fetching.
	Canonicals []store.Canonical
}

// buildAnnualView derives the view from the full confirmed timeline and the
// requested FY start year. The timeline must be the same slice
// GetConfirmedTimeline returns (oldest → newest, components included but not
// required here). Returns the zero view for the empty-timeline case so the
// page still renders all three gating notices.
//
// canonicalName is the same lookup buildDashboardView uses — required so the
// YTD cumulative series can bucket deductions into tax/PF/other consistently
// with the dashboard.
func buildAnnualView(confirmed []store.Payslip, canonicalName map[int64]string, canonicals []store.Canonical, fyStartYear int) annualView {
	v := annualView{
		FYStartYear: fyStartYear,
		FYLabel:     fyLabel(fyStartYear),
		Canonicals:  canonicals,
	}

	if len(confirmed) == 0 {
		return v
	}

	// Count payslips in this FY and distinct months covered. A month is
	// "covered" when any confirmed payslip in this FY has that (month, year)
	// pair. The YTD cumulative chart needs ≥2 distinct months so a line has
	// at least two points; payslip count alone could be satisfied by two
	// same-month uploads which would render a degenerate single-point chart.
	seenMonth := map[[2]int]struct{}{}
	var (
		inFY          int
		earliestStart int
		latestStart   int
	)
	// FY timeline in chronological order — needed by BuildYTDCumulative and
	// BuildAnnualSummary. prevFY timeline likewise, for the delta column.
	var fyTimeline, prevFYTimeline []store.Payslip
	prevFYStart := fyStartYear - 1
	for _, p := range confirmed {
		pStart, _ := FinancialYear(p.PayPeriodMonth, p.PayPeriodYear)
		if earliestStart == 0 || pStart < earliestStart {
			earliestStart = pStart
		}
		if pStart > latestStart {
			latestStart = pStart
		}
		if pStart == fyStartYear {
			inFY++
			seenMonth[[2]int{p.PayPeriodMonth, p.PayPeriodYear}] = struct{}{}
			fyTimeline = append(fyTimeline, p)
		} else if pStart == prevFYStart {
			prevFYTimeline = append(prevFYTimeline, p)
		}
	}
	v.FYPayslipCount = inFY
	v.FYDistinctMonthCount = len(seenMonth)

	v.HasPrevFY = earliestStart < fyStartYear
	v.HasNextFY = latestStart > fyStartYear

	// Section gates — see AnnualSummaryOK/YoYOK/YTDCumulativeOK field docs.
	v.AnnualSummaryOK = inFY >= 1
	v.YTDCumulativeOK = len(seenMonth) >= 2
	if v.YTDCumulativeOK {
		v.YTDCumulative = BuildYTDCumulative(fyTimeline, canonicalName)
	}
	if v.AnnualSummaryOK {
		v.AnnualSummary = BuildAnnualSummary(fyTimeline, prevFYTimeline, canonicals)
	}

	// YoY needs the latest confirmed payslip to have a same-month match in
	// the previous FY. Latest = last in the timeline (it's ordered newest-last).
	latest := confirmed[len(confirmed)-1]
	latestFYStart, _ := FinancialYear(latest.PayPeriodMonth, latest.PayPeriodYear)
	prevFY := latestFYStart - 1
	for _, p := range confirmed {
		if p.PayPeriodMonth != latest.PayPeriodMonth {
			continue
		}
		pStart, _ := FinancialYear(p.PayPeriodMonth, p.PayPeriodYear)
		if pStart == prevFY {
			v.YoYOK = true
			v.YoYSlopegraph = BuildSlopegraph(latest, p, canonicalName)
			break
		}
	}

	return v
}

// BuildSparklinePoints extracts the last N monthly amounts from a component
// timeline (oldest → newest) for inline sparkline rendering. The timeline is
// whatever GetComponentTimeline returns — chronological, possibly shorter
// than N, possibly with gaps in the calendar.
//
// The function deliberately does NOT zero-fill missing months. A 3-month-old
// component shows three real points; padding the front with zeros would lie
// about the component's history (a sudden zero implies a recovery, not a
// new component). The template renders whatever points it gets.
//
// N ≤ 0 returns an empty slice; len(timeline) ≤ N returns the whole timeline
// unchanged.
func BuildSparklinePoints(timeline []store.ComponentPoint, n int) []float64 {
	if n <= 0 || len(timeline) == 0 {
		return []float64{}
	}
	if len(timeline) > n {
		timeline = timeline[len(timeline)-n:]
	}
	out := make([]float64, len(timeline))
	for i, p := range timeline {
		out[i] = p.Amount
	}
	return out
}

// IsAnomalous returns true when the latest delta exceeds 2× the mean absolute
// delta across the window. The threshold is intentionally crude — the point
// is to draw the eye, not to be statistically rigorous. With fewer than two
// deltas there's no meaningful mean to compare against, so the function
// returns false.
//
// deltas are absolute month-over-month changes for one component, oldest →
// newest; the last element is the latest delta.
func IsAnomalous(deltas []float64) bool {
	if len(deltas) < 2 {
		return false
	}
	var sumAbs float64
	for _, d := range deltas {
		sumAbs += math.Abs(d)
	}
	meanAbs := sumAbs / float64(len(deltas))
	if meanAbs == 0 {
		return false // no movement to compare against
	}
	latest := deltas[len(deltas)-1]
	return math.Abs(latest) > 2*meanAbs
}

// ytdCumulative is the chart payload for /annual's YTD cumulative section.
// Each slice is 1:1 with Labels and holds the running total of one stream
// across the FY (oldest → newest). The chart's x-axis is Labels; three line
// datasets draw NetSeries, TaxSeries, PFSeries.
type ytdCumulative struct {
	Labels    []string
	NetSeries []float64
	TaxSeries []float64
	PFSeries  []float64
}

// BuildYTDCumulative walks an FY's confirmed payslips (chronological) and
// produces three running-total series: net pay, tax (TDS + professional
// tax), and PF (EPF deduction). Tax and PF bucketing reuse bucketDeductions
// from dashboard.go so the annual chart and the dashboard's YTD numbers
// agree on what counts as "tax" vs "PF" vs "other".
//
// The input timeline should already be FY-scoped (use GetFYConfirmedTimeline).
// Each payslip contributes one data point; duplicate months are kept as
// separate points in chronological order. canonicalName is the same lookup
// buildDashboardView uses — required so bucketDeductions can map canonical
// IDs to their seed-name buckets.
func BuildYTDCumulative(timeline []store.Payslip, canonicalName map[int64]string) ytdCumulative {
	if len(timeline) == 0 {
		return ytdCumulative{}
	}
	out := ytdCumulative{
		Labels:    make([]string, len(timeline)),
		NetSeries: make([]float64, len(timeline)),
		TaxSeries: make([]float64, len(timeline)),
		PFSeries:  make([]float64, len(timeline)),
	}
	var cumNet, cumTax, cumPF float64
	for i, p := range timeline {
		out.Labels[i] = monthShort(p.PayPeriodMonth) + " " + strconv.Itoa(p.PayPeriodYear)
		cumNet += p.NetPay
		buckets := bucketDeductions(p, canonicalName)
		cumTax += buckets.tax
		cumPF += buckets.pf
		out.NetSeries[i] = cumNet
		out.TaxSeries[i] = cumTax
		out.PFSeries[i] = cumPF
	}
	return out
}

// annualSummaryRow is one canonical's row in the annual summary table.
// Sparkline is 12 points: April (index 0) through March (index 11), zero-
// filled for months without a confirmed payslip for this canonical. Delta is
// this FY total minus prev FY total; HasPrevFY is false when the canonical
// had no activity in the previous FY (or prev FY has no data at all), in
// which case the template renders "—" rather than a misleading "0".
type annualSummaryRow struct {
	CanonicalID  int64
	Name         string
	Category     store.Category
	FYTotal      float64
	PrevFYTotal  float64
	Delta        float64
	DeltaUp      bool // Delta >= 0; precomputed so templates don't compare float to int
	HasPrevFY    bool
	Sparkline    []float64
}

// annualSummaryFooter holds the gross/deductions/net totals for the FY's
// summary footer row. Sourced from the same fyPayslips as the rows.
type annualSummaryFooter struct {
	Gross        float64 // sum of all earning components across the FY
	Deductions   float64 // sum of all deduction components across the FY
	Net          float64 // sum of payslip.NetPay across the FY
}

// annualSummary is the chart/table payload for /annual's summary section.
type annualSummary struct {
	Rows   []annualSummaryRow
	Footer annualSummaryFooter
}

// slopegraphRow is one component's year-over-year comparison: prev-FY amount
// on the left, latest amount on the right, delta in between. Label is "new"
// when the component appears in latest but not prev FY, "gone" when it
// appeared in prev FY but not latest, and empty when it appears in both.
type slopegraphRow struct {
	CanonicalID   int64
	Name          string
	Category      store.Category
	LatestAmount  float64
	PrevFYAmount  float64
	Delta         float64
	DeltaPct      float64 // delta as a percentage of prev FY amount; 0 when prev is 0
	Label         string  // "new", "gone", or ""
}

// BuildSlopegraph pairs components between two payslips (latest vs same-month
// prev FY) by canonical ID and returns them sorted by absolute delta
// descending. Components present in one year but not the other are included
// with their full amount as the delta and a "new" or "gone" label. Zero-delta
// components sort last (stable).
//
// canonicalName maps canonical IDs to display names — the same lookup
// buildDashboardView builds from ListCanonicals.
func BuildSlopegraph(latest, prevFY store.Payslip, canonicalName map[int64]string) []slopegraphRow {
	prevByCanon := indexByCanonical(prevFY.Components)

	seen := map[int64]struct{}{}
	var rows []slopegraphRow

	for _, c := range latest.Components {
		if _, ok := seen[c.CanonicalID]; ok {
			continue
		}
		seen[c.CanonicalID] = struct{}{}
		prevAmt := prevByCanon[c.CanonicalID]
		delta := c.Amount - prevAmt
		row := slopegraphRow{
			CanonicalID:  c.CanonicalID,
			Name:         canonicalName[c.CanonicalID],
			Category:     c.Category,
			LatestAmount: c.Amount,
			PrevFYAmount: prevAmt,
			Delta:        delta,
		}
		if prevAmt == 0 && c.Amount != 0 {
			row.Label = "new"
		}
		if prevAmt > 0 {
			row.DeltaPct = delta / prevAmt * 100
		}
		rows = append(rows, row)
	}
	// Components in prev FY but gone in latest.
	for _, c := range prevFY.Components {
		if _, ok := seen[c.CanonicalID]; ok {
			continue
		}
		seen[c.CanonicalID] = struct{}{}
		rows = append(rows, slopegraphRow{
			CanonicalID:  c.CanonicalID,
			Name:         canonicalName[c.CanonicalID],
			Category:     c.Category,
			LatestAmount: 0,
			PrevFYAmount: c.Amount,
			Delta:        -c.Amount,
			Label:        "gone",
		})
	}

	sort.SliceStable(rows, func(i, j int) bool {
		return math.Abs(rows[i].Delta) > math.Abs(rows[j].Delta)
	})
	return rows
}

// BuildAnnualSummary produces one row per canonical component used anywhere in
// the FY's confirmed payslips. Each row's sparkline covers April (index 0)
// through March (index 11); months with no confirmed payslip for this
// canonical contribute zero. Delta compares this FY's total to the previous
// FY's total; HasPrevFY is false when the canonical has no activity in the
// previous FY (or prevFYPayslips is empty).
//
// Rows are sorted: earnings first by FY total descending, then deductions by
// FY total descending — so the user sees their biggest income lines first
// and their biggest deductions last, mirroring how a payslip reads.
func BuildAnnualSummary(fyPayslips, prevFYPayslips []store.Payslip, canonicals []store.Canonical) annualSummary {
	// Per-canonical running totals (this FY + prev FY).
	type totals struct {
		fyTotal     float64
		prevFYTotal float64
		fyMonthly   [12]float64 // index 0 = April, index 11 = March
		hasPrev     bool
	}
	byCanon := map[int64]*totals{}

	monthIndex := func(month int) int {
		// April = 0, May = 1, ..., December = 8, January = 9, February = 10, March = 11.
		if month >= 4 {
			return month - 4
		}
		return month + 8 // Jan(1)=9, Feb(2)=10, Mar(3)=11
	}

	ensure := func(id int64) *totals {
		t, ok := byCanon[id]
		if !ok {
			t = &totals{}
			byCanon[id] = t
		}
		return t
	}

	for _, p := range fyPayslips {
		for _, c := range p.Components {
			t := ensure(c.CanonicalID)
			t.fyTotal += c.Amount
			t.fyMonthly[monthIndex(p.PayPeriodMonth)] += c.Amount
		}
	}
	for _, p := range prevFYPayslips {
		for _, c := range p.Components {
			t := ensure(c.CanonicalID)
			t.prevFYTotal += c.Amount
			t.hasPrev = true
		}
	}

	// Build a name lookup from canonicals so the row can carry a display name.
	// Canonicals that appear in payslips but aren't in the vocabulary fall
	// back to the slug-converted name (rare — only happens with stale data).
	nameByID := make(map[int64]string, len(canonicals))
	catByID := make(map[int64]store.Category, len(canonicals))
	for _, c := range canonicals {
		nameByID[c.ID] = c.DisplayName()
		catByID[c.ID] = c.Category
	}

	// Only canonicals with FY activity get a row. A canonical that exists in
	// the vocabulary but was never used is dropped — the table would be
	// misleading (zero total, flat sparkline, no delta).
	rows := make([]annualSummaryRow, 0, len(byCanon))
	for id, t := range byCanon {
		if t.fyTotal == 0 {
			// Used in prev FY only — not listed in this FY's summary.
			continue
		}
		spark := make([]float64, 12)
		copy(spark, t.fyMonthly[:])
		row := annualSummaryRow{
			CanonicalID: id,
			Name:        nameByID[id],
			Category:    catByID[id],
			FYTotal:     t.fyTotal,
			PrevFYTotal: t.prevFYTotal,
			Delta:       t.fyTotal - t.prevFYTotal,
			DeltaUp:     (t.fyTotal - t.prevFYTotal) >= 0,
			HasPrevFY:   t.hasPrev,
			Sparkline:    spark,
		}
		rows = append(rows, row)
	}

	// Sort: earnings first by FY total descending, then deductions by FY total
	// descending. Stable so equal totals keep insertion order (deterministic
	// across runs — important for golden tests).
	sort.SliceStable(rows, func(i, j int) bool {
		ri, rj := rows[i], rows[j]
		if ri.Category != rj.Category {
			return ri.Category == store.CategoryEarning
		}
		return ri.FYTotal > rj.FYTotal
	})

	// Footer totals: gross = sum of all earning components across the FY,
	// deductions = sum of all deduction components, net = sum of NetPay.
	// Gross and deductions come from the component rows (so the footer
	// agrees with the rows above); net comes from payslip.NetPay (so the
	// footer matches the YTD cumulative chart's final net point).
	var footer annualSummaryFooter
	for _, p := range fyPayslips {
		footer.Net += p.NetPay
		for _, c := range p.Components {
			if c.Category == store.CategoryEarning {
				footer.Gross += c.Amount
			} else {
				footer.Deductions += c.Amount
			}
		}
	}

	return annualSummary{Rows: rows, Footer: footer}
}

// handleAnnual renders the /annual page. The ?fy=YYYY query param selects a
// financial year (by start year); absent or invalid, it falls back to the FY
// of the latest confirmed payslip, or today's FY when none are confirmed.
func (s *Server) handleAnnual(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	confirmed, err := s.store.GetConfirmedTimeline(ctx)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Could not load annual view: "+err.Error())
		return
	}
	canonicals, err := s.store.ListCanonicals(ctx)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Could not load canonicals: "+err.Error())
		return
	}
	canonicalName := make(map[int64]string, len(canonicals))
	for _, c := range canonicals {
		canonicalName[c.ID] = c.DisplayName()
	}

	// Resolve the requested FY. Precedence: ?fy= query param → latest
	// confirmed payslip's FY → today's FY. Malformed param falls back silently
	// — the URL is a guess/typing, not a 400-worthy user error.
	fyStart := 0
	if raw := r.URL.Query().Get("fy"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 1900 && n <= 9999 {
			fyStart = n
		}
	}
	if fyStart == 0 {
		if len(confirmed) > 0 {
			last := confirmed[len(confirmed)-1]
			fyStart, _ = FinancialYear(last.PayPeriodMonth, last.PayPeriodYear)
		} else {
			fyStart = currentFYStartYear()
		}
	}

	view := buildAnnualView(confirmed, canonicalName, canonicals, fyStart)

	pending, _ := s.store.ListPendingReview(ctx)
	s.render(w, "annual", struct {
		pageData
		Annual annualView
	}{
		pageData: pageData{Title: fmt.Sprintf("%s — Annual", view.FYLabel), PendingCount: len(pending), ActiveBatchID: s.activeBatchID(ctx), Active: "annual"},
		Annual:   view,
	})
}
