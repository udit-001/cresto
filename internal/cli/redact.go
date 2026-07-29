// Package cli — redact.go: PII redaction layer.
//
// This is the single source of truth for what store data is safe to expose
// through the CLI to agents. Every command that outputs payslip data crosses
// this seam; no command touches store.Payslip fields directly for output.
//
// PII policy:
//   - employer_name  → hashed ("employer_<4hex>")
//   - employee_id    → DROPPED
//   - designation    → DROPPED
//   - raw_pdf_path   → DROPPED
//   - batch_id       → DROPPED
//   - component raw_label → KEPT (not PII)
//   - timestamps     → KEPT
package cli

import (
	"encoding/hex"
	"hash/fnv"

	"cresto/internal/store"
)

// payslipJSON is the agent-safe representation of a payslip. Every PII field
// from store.Payslip is either transformed (employer) or omitted.
type payslipJSON struct {
	ID              int64           `json:"id"`
	Employer        string          `json:"employer"`
	PayPeriodMonth  int             `json:"pay_period_month"`
	PayPeriodYear   int             `json:"pay_period_year"`
	PayDays         int             `json:"pay_days"`
	TotalDays       int             `json:"total_days"`
	GrossSalary     float64         `json:"gross_salary"`
	TotalDeductions float64         `json:"total_deductions"`
	NetPay          float64         `json:"net_pay"`
	Status          string          `json:"status"`
	CreatedAt       string          `json:"created_at"`
	ConfirmedAt     string          `json:"confirmed_at,omitempty"`
	Components      []componentJSON `json:"components,omitempty"`
}

// componentJSON is the agent-safe representation of a payslip line item.
// RawLabel is kept — it's the payslip's wording for a salary component
// (e.g. "Basic Salary", "House Rent Allowance"), not personal data.
type componentJSON struct {
	CanonicalID int64   `json:"canonical_id"`
	RawLabel    string  `json:"raw_label"`
	Amount      float64 `json:"amount"`
	YTDAmt      float64 `json:"ytd_amount"`
	Category    string  `json:"category"`
}

// redactPayslip converts a store.Payslip into an agent-safe payslipJSON,
// applying the full PII policy: employer hashed, identifying fields dropped.
func redactPayslip(p store.Payslip) payslipJSON {
	out := payslipJSON{
		ID:              p.ID,
		Employer:        employerHash(p.EmployerName),
		PayPeriodMonth:  p.PayPeriodMonth,
		PayPeriodYear:   p.PayPeriodYear,
		PayDays:         p.PayDays,
		TotalDays:       p.TotalDays,
		GrossSalary:     p.GrossSalary,
		TotalDeductions: p.TotalDeductions,
		NetPay:          p.NetPay,
		Status:          string(p.Status),
		CreatedAt:       p.CreatedAt,
		ConfirmedAt:     p.ConfirmedAt,
	}
	if len(p.Components) > 0 {
		out.Components = make([]componentJSON, len(p.Components))
		for i, c := range p.Components {
			out.Components[i] = componentJSON{
				CanonicalID: c.CanonicalID,
				RawLabel:    c.RawLabel,
				Amount:      c.Amount,
				YTDAmt:      c.YTDAmt,
				Category:    string(c.Category),
			}
		}
	}
	return out
}

// redactPayslips converts a slice of store.Payslips into agent-safe JSON.
// Components are NOT included — use redactPayslipsWithComponents when the
// caller has populated them (e.g. GetConfirmedTimeline, GetPayslip).
func redactPayslips(ps []store.Payslip) []payslipJSON {
	out := make([]payslipJSON, 0, len(ps))
	for _, p := range ps {
		out = append(out, redactPayslip(p))
	}
	return out
}

// employerHash returns a stable, anonymized identifier for an employer name.
// Uses FNV-1a (deterministic across invocations, no schema change needed).
// Format: "employer_" + first 4 hex chars of the 32-bit hash.
func employerHash(name string) string {
	h := fnv.New32a()
	h.Write([]byte(name))
	return "employer_" + hex.EncodeToString(h.Sum(nil))[:4]
}

// resolveEmployerHash reverse-looks-up a hash to the real employer name by
// scanning the known names. Returns the matched name and true, or "" and
// false if no name hashes to the given value.
func resolveEmployerHash(hash string, names []string) (string, bool) {
	for _, name := range names {
		if employerHash(name) == hash {
			return name, true
		}
	}
	return "", false
}

// employerSummaryJSON is the agent-safe employer listing entry.
type employerSummaryJSON struct {
	ID            string `json:"id"`
	PayslipCount  int    `json:"payslip_count"`
}

// canonicalJSON is the agent-safe canonical component vocabulary entry.
type canonicalJSON struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	DisplayName   string `json:"display_name"`
	Category      string `json:"category"`
	IsUserCreated bool   `json:"is_user_created"`
}
