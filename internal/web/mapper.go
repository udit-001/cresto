// Package web hosts the HTTP server for payslip upload and review.
// mapper.go converts an llm.Extraction into a store.Payslip ready for review.
package web

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"cresto/internal/llm"
	"cresto/internal/store"
)

// canonicalKeywords maps canonical names to the substrings that signal them in
// raw payslip labels. Longer keywords win over shorter ones (so "house rent
// allowance" beats "hra" beats a bare "da"). Order inside each list doesn't
// matter — only the longest matching keyword's length is used as the score.
var canonicalKeywords = map[string][]string{
	"basic": {"basic"},
	"hra": {
		"house rent allowance", "house rent", "hra", "rent allowance",
	},
	"da": {"dearness allowance", "dearness"},
	"conveyance": {
		"conveyance allowance", "conveyance", "transport allowance", "transport",
		"travel reimbursement",
	},
	"medical": {
		"medical reimbursement", "medical allowance", "medical",
	},
	"medical_insurance_earning": {"medical insurance"},
	"medical_insurance_deduction": {"medical insurance deduction"},
	"lta": {
		"leave travel allowance", "leave travel", "lta",
	},
	"education": {
		"education allowance", "educational allowance", "education",
	},
	"telephone": {
		"telephone reimbursement", "telephone allowance", "telephone",
		"phone allowance", "mobile reimbursement",
	},
	"special_allowance": {"special allowance", "special"},
	"bonus": {
		"bonus", "incentive", "performance pay", "performance bonus", "exgratia",
	},
	"arrears": {"arrears", "arrear"},
	"leave_encashment": {"leave encashment", "encashment"},
	"epf": {
		"employees provident fund", "employee provident fund", "employee pf",
		"provident fund", "epf",
	},
	"professional_tax": {
		"professional tax", "prof tax", "ptax",
	},
	"tds": {
		"income tax", "tax deducted at source", "tds",
	},
	"esi": {"esi", "employee state insurance"},
	"lwf": {
		"lwf", "labour welfare fund", "labor welfare",
	},
	"lop": {
		"lop", "loss of pay", "leave without pay",
	},
	"loan_recovery": {"loan", "advance recovery"},
}

var whitespaceRE = regexp.MustCompile(`\s+`)
var yearRE = regexp.MustCompile(`(?:19|20)\d{2}`)
var numericMonthRE = regexp.MustCompile(`\b(\d{1,2})\b`)

// mapLabelToCanonical returns the canonical name (e.g. "basic", "epf") whose
// keyword best matches the raw label, or "" if no keyword matches. The fallback
// bucket (other_earnings / other_deductions) is applied by the caller, since it
// depends on the component's category which the mapper doesn't know here.
func mapLabelToCanonical(label string) string {
	n := normalizeLabel(label)
	if n == "" {
		return ""
	}
	bestName := ""
	bestLen := 0
	for canon, kws := range canonicalKeywords {
		for _, kw := range kws {
			k := normalizeLabel(kw)
			if k == "" {
				continue
			}
			if !strings.Contains(n, k) {
				continue
			}
			// Longer keyword = more specific match. Ties keep the first found,
			// which is fine because canonicals don't share keywords of equal length.
			if len(k) > bestLen {
				bestLen = len(k)
				bestName = canon
			}
		}
	}
	return bestName
}

func normalizeLabel(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return whitespaceRE.ReplaceAllString(s, " ")
}

// normalizeEmployer canonicalises a company name so the same employer
// extracted from different payslips (with different whitespace or casing)
// filters consistently. Trims, collapses internal whitespace, and applies
// title casing. "GYANSYS INFOTECH PRIVATE LIMITED" → "Gyansys Infotech Private Limited".
func normalizeEmployer(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	s = whitespaceRE.ReplaceAllString(s, " ")
	lowered := strings.ToLower(s)
	return strings.Title(lowered) //nolint:staticcheck // Title is fine for ASCII company names
}

// periodLayouts tried in order. "January 2006" is Go's reference format — the
// literal "2006" is the year placeholder, "January" the full month name.
var periodLayouts = []string{
	"January 2006",
	"Jan 2006",
	"January, 2006",
	"Jan, 2006",
	"01/2006",
	"1/2006",
	"2006-01",
	"2006-1",
}

// parsePayPeriod extracts month + year from a free-form pay period string.
// Returns (0, 0, err) if it cannot find both. Errors are soft — the caller
// stores (0, 0) and the user fixes the period in the review UI.
func parsePayPeriod(s string) (month, year int, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, fmt.Errorf("empty pay period")
	}
	for _, layout := range periodLayouts {
		if t, perr := time.Parse(layout, s); perr == nil {
			return int(t.Month()), t.Year(), nil
		}
	}
	// Fall back: scan for a 4-digit year plus a month name or numeric month.
	yearStr := yearRE.FindString(s)
	if yearStr == "" {
		return 0, 0, fmt.Errorf("no year found in %q", s)
	}
	year, _ = strconv.Atoi(yearStr)

	// Try month name (full or 3-letter abbrev) anywhere in the string.
	lower := strings.ToLower(s)
	for m := time.January; m <= time.December; m++ {
		name := strings.ToLower(m.String())
		abbr := name[:3]
		if strings.Contains(lower, name) || strings.Contains(lower, abbr) {
			return int(m), year, nil
		}
	}

	// Try numeric month: first 1-2 digit number that's 1-12 and isn't the year.
	for _, m := range numericMonthRE.FindAllString(s, -1) {
		n, _ := strconv.Atoi(m)
		if n >= 1 && n <= 12 && m != yearStr {
			return n, year, nil
		}
	}
	return 0, year, fmt.Errorf("no month found in %q", s)
}

// MapExtraction converts an LLM Extraction into a store.Payslip ready for
// SavePayslip. canonicals is the full list from store.ListCanonicals; it's used
// to resolve canonical IDs from either the classifier's slugs (stage 2 LLM)
// or best-effort label matching (keyword fallback). Unmatched labels fall back
// to other_earnings / other_deductions so the user can fix the mapping in the
// review UI. Returns an error only if a required canonical is missing from the
// DB (which would indicate a schema/seed bug).
//
// class is the stage-2 classifier output (parallel slug arrays). If nil, or if
// its array lengths don't match the extraction, the keyword mapper is used as
// fallback for every component — the pipeline passes nil when the classifier
// call fails so extraction still succeeds.
func MapExtraction(ext *llm.Extraction, class *llm.Classification, canonicals []store.Canonical) (store.Payslip, error) {
	byName := make(map[string]store.Canonical, len(canonicals))
	for _, c := range canonicals {
		byName[c.Name] = c
	}

	month, year, _ := parsePayPeriod(ext.PayPeriod) // (0,0) is fine — user fixes in review

	p := store.Payslip{
		EmployerName:    normalizeEmployer(ext.Company),
		EmployeeID:      ext.EmployeeID,
		Designation:     ext.Designation,
		PayPeriodMonth:  month,
		PayPeriodYear:   year,
		PayDays:         ext.Other.PayDays,
		TotalDays:       ext.Other.TotalDays,
		GrossSalary:     ext.Totals.Earnings,
		TotalDeductions: ext.Totals.Deductions,
		NetPay:          ext.Totals.NetPay,
		Status:          store.StatusPendingReview,
	}

	fallbackEarning := "other_earnings"
	fallbackDeduction := "other_deductions"

	// Use classifier slugs when available; fall back to keyword matching per
	// component when the classifier is nil or its output is malformed.
	useClass := class != nil &&
		len(class.Earnings) == len(ext.Earnings) &&
		len(class.Deductions) == len(ext.Deductions)

	for i, e := range ext.Earnings {
		name := fallbackEarning
		if useClass {
			name = resolveSlug(class.Earnings[i], byName, fallbackEarning)
		} else {
			name = mapLabelToCanonical(e.Label)
			if name == "" {
				name = fallbackEarning
			}
		}
		canon, ok := byName[name]
		if !ok {
			return store.Payslip{}, fmt.Errorf("canonical %q missing from DB", name)
		}
		p.Components = append(p.Components, store.Component{
			CanonicalID: canon.ID,
			RawLabel:    e.Label,
			Amount:      e.Amount,
			YTDAmt:      e.YTD,
			Category:    store.CategoryEarning,
		})
	}

	for i, d := range ext.Deductions {
		name := fallbackDeduction
		if useClass {
			name = resolveSlug(class.Deductions[i], byName, fallbackDeduction)
		} else {
			name = mapLabelToCanonical(d.Label)
			if name == "" {
				name = fallbackDeduction
			}
		}
		canon, ok := byName[name]
		if !ok {
			return store.Payslip{}, fmt.Errorf("canonical %q missing from DB", name)
		}
		p.Components = append(p.Components, store.Component{
			CanonicalID: canon.ID,
			RawLabel:    d.Label,
			Amount:      d.Amount,
			YTDAmt:      d.YTD,
			Category:    store.CategoryDeduction,
		})
	}

	return p, nil
}

// resolveSlug looks up a classifier-returned slug in the canonical map. If the
// slug isn't found (LLM hallucinated or used a stale canonical), it falls back
// to the category-specific other_* bucket rather than failing the whole
// extraction — the user can fix it in the review UI.
func resolveSlug(slug string, byName map[string]store.Canonical, fallback string) string {
	if _, ok := byName[slug]; ok {
		return slug
	}
	return fallback
}
