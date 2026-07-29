package web

import (
	"math"
	"sort"

	"cresto/internal/store"
)

// dashboardView aggregates everything the dashboard template renders. Built
// once per request from the confirmed timeline + canonical vocabulary.
type dashboardView struct {
	HasConfirmed bool

	// Headline — latest month's net pay.
	Latest       store.Payslip
	LatestNet    float64
	LatestPeriod string

	// Month-over-month delta (latest vs previous confirmed).
	HasPrevious bool
	NetDelta    float64
	NetDeltaPct float64
	NetDeltaUp  bool

	// YTD totals — summed across payslips whose pay_period_year matches the
	// latest's year. YTDYear is what we filtered on (0 when no confirmed).
	YTDYear  int
	YTDNet   float64
	YTDGross float64
	YTDTax   float64
	YTDPF    float64

	// "Why did in-hand change?" — components sorted by abs magnitude, capped
	// at ComponentDeltasCap visible entries. ComponentDeltasMore is the count
	// of non-zero deltas beyond the cap (0 when everything fits). Only
	// populated when HasPrevious is true.
	ComponentDeltas      []componentDelta
	ComponentDeltasMore  int

	// Chart series (oldest → newest), all 1:1 with Periods.
	Periods   []string
	NetSeries []float64

	// DrillDown lists canonicals used across confirmed payslips, with their
	// latest amount and use-count. Sorted category-then-name.
	DrillDown []drillDownEntry
}

type componentDelta struct {
	CanonicalID int64
	Name        string
	RawLabel    string // latest payslip's raw label
	Category    store.Category
	Delta       float64 // latest − previous; positive means it grew
	DeltaUp     bool    // Delta >= 0; precomputed so templates don't compare float to int
	NonZero     bool   // Delta != 0; precomputed for the same reason
}

type drillDownEntry struct {
	CanonicalID  int64
	Name         string
	Category     store.Category
	LatestAmount float64
	UseCount     int

	// Sparkline is the last SparklineWindow monthly amounts (oldest → newest)
	// for inline SVG rendering. Empty when the component has no history.
	Sparkline []float64
	// Anomalous is true when the latest month-over-month delta exceeds 2×
	// the mean absolute delta across the window. The template uses this to
	// decide whether the sparkline's latest point gets --chart-accent.
	Anomalous bool
}

// SparklineWindow is how many monthly points each row's sparkline shows.
// Six months is enough to show a trend without making the column dominate the
// table; matching the callout's component cap keeps the two views aligned.
const SparklineWindow = 6

// bucketing canonicals by name. The names here match the seed vocabulary in
// internal/store/store.go; user-created canonicals land in "other".
const (
	canonPF = "epf"
	canonTDS = "tds"
	canonProfTax = "professional_tax"

	// ComponentDeltasCap is the maximum number of component deltas the
	// "why did it change?" callout shows. The remainder surfaces as a
	// "and N more" link to the components table below.
	ComponentDeltasCap = 6
)

// buildDashboardView assembles the view from the confirmed timeline. Both
// inputs must come from the store: GetConfirmedTimeline for payslips-with-
// components, ListCanonicals for the vocabulary. Returns the zero view when
// confirmed is empty.
func buildDashboardView(confirmed []store.Payslip, canonicals []store.Canonical) dashboardView {
	if len(confirmed) == 0 {
		return dashboardView{}
	}

	// Build a canonical lookup so we can name components and decide buckets.
	// Store the display name (not the slug) so every view struct and chart
	// label that uses this map shows a human-readable string.
	canonicalName := make(map[int64]string, len(canonicals))
	for _, c := range canonicals {
		canonicalName[c.ID] = c.DisplayName()
	}

	view := dashboardView{
		HasConfirmed: true,
		Latest:       confirmed[len(confirmed)-1],
		LatestNet:    confirmed[len(confirmed)-1].NetPay,
		LatestPeriod: periodLabel(confirmed[len(confirmed)-1].PayPeriodMonth, confirmed[len(confirmed)-1].PayPeriodYear),
	}

	// MoM delta vs the previous confirmed payslip.
	if len(confirmed) >= 2 {
		prev := confirmed[len(confirmed)-2]
		view.HasPrevious = true
		view.NetDelta = view.LatestNet - prev.NetPay
		if prev.NetPay > 0 {
			view.NetDeltaPct = view.NetDelta / prev.NetPay * 100
		}
		view.NetDeltaUp = view.NetDelta >= 0
		view.ComponentDeltas, view.ComponentDeltasMore = capComponentDeltas(
			computeComponentDeltas(prev, view.Latest, canonicalName))
	}

	// YTD totals: sum payslips in the latest's year.
	view.YTDYear = view.Latest.PayPeriodYear
	for _, p := range confirmed {
		if p.PayPeriodYear != view.YTDYear {
			continue
		}
		view.YTDNet += p.NetPay
		view.YTDGross += p.GrossSalary
		// Deduction buckets — recomputed below per-payslip so the chart and
		// the YTD numbers share one source of truth.
		buckets := bucketDeductions(p, canonicalName)
		view.YTDTax += buckets.tax
		view.YTDPF += buckets.pf
	}

	// Chart series — every confirmed payslip, oldest first.
	n := len(confirmed)
	view.Periods = make([]string, n)
	view.NetSeries = make([]float64, n)
	for i, p := range confirmed {
		view.Periods[i] = periodLabel(p.PayPeriodMonth, p.PayPeriodYear)
		view.NetSeries[i] = p.NetPay
	}

	view.DrillDown = buildDrillDown(confirmed, canonicals)
	return view
}

// deductionBuckets splits a payslip's deductions into the four stacked-bar
// buckets: in-hand (net), PF, tax, other.
type deductionBuckets struct {
	pf    float64
	tax   float64
	other float64 // = total_deductions − pf − tax, clamped at 0
}

func bucketDeductions(p store.Payslip, canonicalName map[int64]string) deductionBuckets {
	var b deductionBuckets
	for _, c := range p.Components {
		if c.Category != store.CategoryDeduction {
			continue
		}
		switch canonicalName[c.CanonicalID] {
		case canonPF:
			b.pf += c.Amount
		case canonTDS, canonProfTax:
			b.tax += c.Amount
		default:
			b.other += c.Amount
		}
	}
	// If a payslip's component breakdown doesn't sum to total_deductions (e.g.
	// an old import missing some rows), the leftover still belongs to "other".
	if leftover := p.TotalDeductions - b.pf - b.tax - b.other; leftover > 0 {
		b.other += leftover
	}
	return b
}

// computeComponentDeltas pairs matching components between prev and latest by
// canonical ID and returns them sorted by absolute delta (largest first).
// Missing-on-either-side components are treated as zero. We do NOT invent
// "phantom" entries: a component present only on one side is included with
// its full amount as the delta, which is exactly what the callout wants.
func computeComponentDeltas(prev, latest store.Payslip, canonicalName map[int64]string) []componentDelta {
	prevByCanon := indexByCanonical(prev.Components)

	seen := map[int64]struct{}{}
	var deltas []componentDelta
	for _, c := range latest.Components {
		if _, ok := seen[c.CanonicalID]; ok {
			continue
		}
		seen[c.CanonicalID] = struct{}{}
		prevAmt := prevByCanon[c.CanonicalID]
		delta := c.Amount - prevAmt
		deltas = append(deltas, componentDelta{
			CanonicalID: c.CanonicalID,
			Name:        canonicalName[c.CanonicalID],
			RawLabel:    c.RawLabel,
			Category:    c.Category,
			Delta:       delta,
			DeltaUp:     delta >= 0,
			NonZero:     delta != 0,
		})
	}
	// Components present in prev but gone in latest.
	for _, c := range prev.Components {
		if _, ok := seen[c.CanonicalID]; ok {
			continue
		}
		seen[c.CanonicalID] = struct{}{}
		deltas = append(deltas, componentDelta{
			CanonicalID: c.CanonicalID,
			Name:        canonicalName[c.CanonicalID],
			Category:    c.Category,
			Delta:       -c.Amount, // existed before, zero now
			DeltaUp:     false,
			NonZero:     c.Amount != 0,
		})
	}
	sort.SliceStable(deltas, func(i, j int) bool {
		return math.Abs(deltas[i].Delta) > math.Abs(deltas[j].Delta)
	})
	return deltas
}

// capComponentDeltas keeps only the first ComponentDeltasCap non-zero deltas
// from the sorted slice and returns the count of additional non-zero deltas
// that didn't fit. Zero-magnitude deltas never count toward the cap — the
// callout is "why did it change", and no-change rows would just be noise.
func capComponentDeltas(deltas []componentDelta) (visible []componentDelta, more int) {
	visible = make([]componentDelta, 0, ComponentDeltasCap)
	for _, d := range deltas {
		if !d.NonZero {
			continue
		}
		if len(visible) < ComponentDeltasCap {
			visible = append(visible, d)
			continue
		}
		more++
	}
	return visible, more
}

func indexByCanonical(comps []store.Component) map[int64]float64 {
	out := make(map[int64]float64, len(comps))
	for _, c := range comps {
		out[c.CanonicalID] += c.Amount
	}
	return out
}

// buildDrillDown lists canonicals used across confirmed payslips, with their
// latest amount, use-count, and last-N-months sparkline. Sorted by category
// then name (matches how ListCanonicals itself is sorted, so the dashboard
// list is stable).
func buildDrillDown(confirmed []store.Payslip, canonicals []store.Canonical) []drillDownEntry {
	// For each canonical, walk confirmed oldest→newest collecting (month,
	// year, amount). The latest amount is the last hit; the chronological
	// amount list feeds the sparkline + anomaly check. Confirmed is small
	// (one user's history), so the O(canonicals × payslips × components)
	// pass is fine.
	out := make([]drillDownEntry, 0, len(canonicals))
	for _, can := range canonicals {
		var (
			amountsByMonth []amountAt
			count          int
		)
		for _, p := range confirmed {
			for _, c := range p.Components {
				if c.CanonicalID != can.ID {
					continue
				}
				count++
				amountsByMonth = append(amountsByMonth, amountAt{
					year: p.PayPeriodYear, month: p.PayPeriodMonth, amount: c.Amount,
				})
			}
		}
		if len(amountsByMonth) == 0 {
			continue
		}
		// amountsByMonth is in the same order as confirmed (chronological).
		// Sparkline: last SparklineWindow amounts.
		spark := make([]float64, len(amountsByMonth))
		for i, a := range amountsByMonth {
			spark[i] = a.amount
		}
		if len(spark) > SparklineWindow {
			spark = spark[len(spark)-SparklineWindow:]
		}
		// Anomaly: deltas between successive points within the sparkline window.
		deltas := make([]float64, 0, len(spark)-1)
		for i := 1; i < len(spark); i++ {
			deltas = append(deltas, spark[i]-spark[i-1])
		}
		out = append(out, drillDownEntry{
			CanonicalID:  can.ID,
			Name:         can.DisplayName(),
			Category:     can.Category,
			LatestAmount: amountsByMonth[len(amountsByMonth)-1].amount,
			UseCount:     count,
			Sparkline:    spark,
			Anomalous:    IsAnomalous(deltas),
		})
	}
	return out
}

// amountAt is a single (year, month, amount) observation used internally by
// buildDrillDown to walk a canonical's history in chronological order.
type amountAt struct {
	year   int
	month  int
	amount float64
}
