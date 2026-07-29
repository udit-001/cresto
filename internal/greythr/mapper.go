package greythr

import (
	"fmt"
	"strings"
	"time"

	"cresto/internal/store"
)

// greythrNameToCanonical maps greytHR's internal item names directly to
// Cresto canonical slugs. This is more precise than keyword matching —
// greytHR uses consistent uppercase keys (BASIC, PF, INCOME_TAX) that
// don't always match the keyword mapper (e.g. "PF" is too short to match
// "provident fund").
var greythrNameToCanonical = map[string]string{
	"BASIC":                            "basic",
	"HRA":                              "hra",
	"DA":                               "da",
	"CONVEYANCE":                       "conveyance",
	"MEDICAL":                          "medical",
	"MEDICAL_INSURANCE":                "medical_insurance_earning",
	"LTA":                              "lta",
	"EDUCATION_ALLOWANCE":              "education",
	"TELEPHONE":                        "telephone",
	"SPECIAL_ALLOWANCE":                "special_allowance",
	"BONUS":                            "bonus",
	"STATUTORY_BONUS":                  "bonus",
	"ARREARS":                          "arrears",
	"LEAVE_ENCASHMENT":                 "leave_encashment",
	"TERM_INSURANCE_PREMIUM":           "term_insurance_earning",
	"PF":                               "epf",
	"INCOME_TAX":                       "tds",
	"PROF_TAX":                         "professional_tax",
	"ESI":                              "esi",
	"LWF":                              "lwf",
	"LOP":                              "lop",
	"LOAN":                             "loan_recovery",
	"MEDICAL_DEDUCTION":                "medical_insurance_deduction",
	"TERM_INSURANCE_PREMIUM_DEDUCTION": "term_insurance_deduction",
}

// canonicalKeywords is the keyword-based fallback mapper, duplicated from
// web/mapper.go. Used when a greytHR item name isn't in the direct map —
// the description field is matched against known substrings.
var canonicalKeywords = map[string][]string{
	"basic":        {"basic"},
	"hra":          {"house rent allowance", "house rent", "hra", "rent allowance"},
	"da":           {"dearness allowance", "dearness"},
	"conveyance":   {"conveyance", "transport allowance", "transport"},
	"medical":      {"medical reimbursement", "medical allowance", "medical"},
	"medical_insurance_earning":  {"medical insurance"},
	"medical_insurance_deduction": {"medical insurance deduction"},
	"lta":          {"leave travel allowance", "leave travel", "lta"},
	"education":    {"education allowance", "educational allowance", "education"},
	"telephone":    {"telephone reimbursement", "telephone allowance", "telephone", "phone allowance", "mobile reimbursement"},
	"special_allowance": {"special allowance", "special"},
	"bonus":        {"bonus", "incentive", "performance pay", "performance bonus", "exgratia"},
	"arrears":      {"arrears", "arrear"},
	"leave_encashment": {"leave encashment", "encashment"},
	"epf":          {"employees provident fund", "employee provident fund", "employee pf", "provident fund", "epf"},
	"professional_tax": {"professional tax", "prof tax", "ptax"},
	"tds":          {"income tax", "tax deducted at source", "tds"},
	"esi":          {"esi", "employee state insurance"},
	"lwf":          {"lwf", "labour welfare fund", "labor welfare"},
	"lop":          {"lop", "loss of pay", "leave without pay"},
	"loan_recovery": {"loan", "advance recovery"},
}

func mapLabelToCanonical(label string) string {
	n := strings.ToLower(strings.TrimSpace(label))
	if n == "" {
		return ""
	}
	n = strings.Join(strings.Fields(n), " ")
	bestName := ""
	bestLen := 0
	for canon, kws := range canonicalKeywords {
		for _, kw := range kws {
			k := strings.ToLower(strings.TrimSpace(kw))
			if k == "" || !strings.Contains(n, k) {
				continue
			}
			if len(k) > bestLen {
				bestLen = len(k)
				bestName = canon
			}
		}
	}
	return bestName
}

// MapToPayslip converts a greytHR PayslipData response into a store.Payslip
// ready for SavePayslip. It extracts earnings and deductions from the
// hierarchical JSON, maps them to canonical slugs (direct map first,
// keyword fallback second), and parses the pay period from the month's
// fromDate. Zero-value components are skipped (they're configured but
// didn't apply this period).
//
// Employee metadata (designation, employee number) is fetched separately via
// FetchEmployeeInfo and stamped onto the payslip by the caller — the mapper
// only derives fields it can compute from the payslip data itself.
func MapToPayslip(data *PayslipData, month PayslipMonth, host string, canonicals []store.Canonical) (store.Payslip, error) {
	byName := make(map[string]store.Canonical, len(canonicals))
	for _, c := range canonicals {
		byName[c.Name] = c
	}

	payMonth, payYear := parseFromDate(month.FromDate)

	p := store.Payslip{
		EmployerName:   deriveEmployerName(host),
		PayPeriodMonth: payMonth,
		PayPeriodYear:  payYear,
		Status:         store.StatusPendingReview,
	}

	fallbackEarning := "other_earnings"
	fallbackDeduction := "other_deductions"

	for _, item := range data.Content {
		def := item.Item
		if !def.Show {
			continue
		}

		switch def.Name {
		case "TOT_COST":
			p.NetPay = item.Value
		case "INCOME":
			p.GrossSalary = item.Value
		case "DEDUCT":
			p.TotalDeductions = abs(item.Value)
		case "EFFWORKDAYS":
			p.PayDays = int(item.Value)
		case "DAYSINMONTH":
			p.TotalDays = int(item.Value)
		}

		if item.Value == 0 {
			continue
		}

		var category store.Category
		switch def.Parent {
		case "INCOME":
			category = store.CategoryEarning
		case "DEDUCT":
			category = store.CategoryDeduction
		default:
			continue
		}

		slug := resolveCanonicalSlug(def.Name, def.Description, category, byName, fallbackEarning, fallbackDeduction)
		canon, ok := byName[slug]
		if !ok {
			return store.Payslip{}, fmt.Errorf("canonical %q missing from DB", slug)
		}

		p.Components = append(p.Components, store.Component{
			CanonicalID: canon.ID,
			RawLabel:    def.Description,
			Amount:      item.Value,
			Category:    category,
		})
	}

	return p, nil
}

func resolveCanonicalSlug(name, description string, category store.Category, byName map[string]store.Canonical, fallbackEarning, fallbackDeduction string) string {
	if slug, ok := greythrNameToCanonical[name]; ok {
		if _, exists := byName[slug]; exists {
			return slug
		}
	}
	slug := mapLabelToCanonical(description)
	if slug != "" {
		if _, exists := byName[slug]; exists {
			return slug
		}
	}
	if category == store.CategoryEarning {
		return fallbackEarning
	}
	return fallbackDeduction
}

func ParseFromDate(fromDate string) (month, year int) {
	return parseFromDate(fromDate)
}

func parseFromDate(fromDate string) (month, year int) {
	t, err := time.Parse("2006-01-02", fromDate)
	if err != nil {
		return 0, 0
	}
	return int(t.Month()), t.Year()
}

func deriveEmployerName(host string) string {
	parts := strings.SplitN(host, ".", 2)
	name := parts[0]
	if name == "" {
		return host
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
