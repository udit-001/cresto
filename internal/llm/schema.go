package llm

type Component struct {
	Label  string  `json:"label"`
	Amount float64 `json:"amount"`
	YTD    float64 `json:"ytd"`
}

type Totals struct {
	Earnings      float64 `json:"earnings"`
	Deductions    float64 `json:"deductions"`
	NetPay        float64 `json:"net_pay"`
	EarningsYTD   float64 `json:"earnings_ytd"`
	DeductionsYTD float64 `json:"deductions_ytd"`
	NetPayYTD     float64 `json:"net_pay_ytd"`
}

type Other struct {
	LOPDays                int     `json:"lop_days,omitempty"`
	PayDays                int     `json:"pay_days,omitempty"`
	TotalDays              int     `json:"total_days,omitempty"`
	EmployerPFContribution float64 `json:"employer_pf_contribution,omitempty"`
	ReimbursementClaim     float64 `json:"reimbursement_claim,omitempty"`
}

type Extraction struct {
	Company     string      `json:"company"`
	PayPeriod   string      `json:"pay_period"`
	Designation string      `json:"designation"`
	EmployeeID  string      `json:"employee_id"`
	Earnings    []Component `json:"earnings"`
	Deductions  []Component `json:"deductions"`
	Totals      Totals      `json:"totals"`
	Other       Other       `json:"other"`
}

// CanonicalRef is the llm package's view of a canonical — just the fields the
// classifier needs. Decouples llm from store: the caller converts []store.Canonical
// to []CanonicalRef at the seam, so llm never imports store.
type CanonicalRef struct {
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	Category string `json:"category"` // "earning" or "deduction"
}

// classifyInput is the JSON payload sent to the classifier model. Canonicals
// are grouped by category so the model picks from the right list per array —
// no category ambiguity for same-display-name canonicals.
type classifyInput struct {
	Earnings   []Component     `json:"earnings"`
	Deductions []Component     `json:"deductions"`
	Canonicals struct {
		Earnings   []CanonicalRef `json:"earnings"`
		Deductions []CanonicalRef `json:"deductions"`
	} `json:"canonicals"`
}

// Classification is the classifier's output: parallel slug arrays, same order
// and length as the Extraction's earnings/deductions. Go validates both invariants.
type Classification struct {
	Earnings   []string `json:"earnings"`
	Deductions []string `json:"deductions"`
}
