package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"cresto/internal/store"
)

func TestEmployerHash(t *testing.T) {
	t.Parallel()
	h := employerHash("Acme Corp")
	if !strings.HasPrefix(h, "employer_") {
		t.Fatalf("hash %q missing employer_ prefix", h)
	}
	if len(h) != len("employer_")+4 {
		t.Fatalf("hash %q expected 4 hex chars after prefix, got %d", h, len(h)-len("employer_"))
	}
}

func TestEmployerHashStable(t *testing.T) {
	t.Parallel()
	a := employerHash("Acme Corp")
	b := employerHash("Acme Corp")
	if a != b {
		t.Fatalf("employer hash not stable: %q vs %q", a, b)
	}
}

func TestEmployerHashDifferent(t *testing.T) {
	t.Parallel()
	if employerHash("Acme Corp") == employerHash("Globex Inc") {
		t.Fatal("different employer names produced the same hash")
	}
}

func TestResolveEmployerHash(t *testing.T) {
	t.Parallel()
	names := []string{"Acme Corp", "Globex Inc"}
	hash := employerHash("Acme Corp")
	got, ok := resolveEmployerHash(hash, names)
	if !ok {
		t.Fatalf("hash %q not resolved", hash)
	}
	if got != "Acme Corp" {
		t.Fatalf("resolved to %q, want %q", got, "Acme Corp")
	}
}

func TestResolveEmployerHashUnknown(t *testing.T) {
	t.Parallel()
	_, ok := resolveEmployerHash("employer_zzzz", []string{"Acme Corp"})
	if ok {
		t.Fatal("expected unknown hash to not resolve")
	}
}

func TestRedactPayslipDropsPII(t *testing.T) {
	t.Parallel()
	p := store.Payslip{
		ID:              42,
		EmployerName:    "Acme Corp",
		EmployeeID:      "EMP12345",
		Designation:     "Senior Engineer",
		PayPeriodMonth:  7,
		PayPeriodYear:   2026,
		GrossSalary:     200000,
		NetPay:          150000,
		RawPDFPath:      "/home/user/.cresto/payslips/file.pdf",
		BatchID:         "batch-uuid-123",
		Status:          store.StatusConfirmed,
	}

	out := redactPayslip(p)
	b, _ := json.Marshal(out)
	s := string(b)

	dropped := []string{
		"EMP12345",
		"Senior Engineer",
		"/home/user",
		"file.pdf",
		"batch-uuid-123",
		"Acme Corp",
	}
	for _, pii := range dropped {
		if strings.Contains(s, pii) {
			t.Errorf("PII %q leaked into redacted JSON: %s", pii, s)
		}
	}
}

func TestRedactPayslipKeepsFinancialData(t *testing.T) {
	t.Parallel()
	p := store.Payslip{
		ID:             42,
		EmployerName:   "Acme Corp",
		PayPeriodMonth: 7,
		PayPeriodYear:  2026,
		GrossSalary:    200000,
		NetPay:         150000,
		Status:         store.StatusConfirmed,
	}

	out := redactPayslip(p)

	if out.ID != 42 {
		t.Errorf("ID = %d, want 42", out.ID)
	}
	if out.Employer != employerHash("Acme Corp") {
		t.Errorf("Employer = %q, want %q", out.Employer, employerHash("Acme Corp"))
	}
	if out.GrossSalary != 200000 {
		t.Errorf("GrossSalary = %v, want 200000", out.GrossSalary)
	}
	if out.NetPay != 150000 {
		t.Errorf("NetPay = %v, want 150000", out.NetPay)
	}
	if out.PayPeriodMonth != 7 || out.PayPeriodYear != 2026 {
		t.Errorf("Period = %d/%d, want 7/2026", out.PayPeriodMonth, out.PayPeriodYear)
	}
}

func TestRedactPayslipKeepsComponentRawLabels(t *testing.T) {
	t.Parallel()
	p := store.Payslip{
		ID: 1,
		Components: []store.Component{
			{CanonicalID: 1, RawLabel: "Basic Salary", Amount: 100000, Category: store.CategoryEarning},
			{CanonicalID: 16, RawLabel: "Provident Fund", Amount: 12000, Category: store.CategoryDeduction},
		},
	}

	out := redactPayslip(p)
	if len(out.Components) != 2 {
		t.Fatalf("Components len = %d, want 2", len(out.Components))
	}
	if out.Components[0].RawLabel != "Basic Salary" {
		t.Errorf("component 0 RawLabel = %q, want %q", out.Components[0].RawLabel, "Basic Salary")
	}
	if out.Components[1].RawLabel != "Provident Fund" {
		t.Errorf("component 1 RawLabel = %q, want %q", out.Components[1].RawLabel, "Provident Fund")
	}
}

func TestRedactPayslipsEmpty(t *testing.T) {
	t.Parallel()
	out := redactPayslips(nil)
	if len(out) != 0 {
		t.Fatalf("expected empty slice, got %d", len(out))
	}
}
