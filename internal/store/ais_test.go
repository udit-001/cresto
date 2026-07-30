package store

import (
	"context"
	"testing"
)

func TestAISImport_EmptyDB_ReturnsNotFound(t *testing.T) {
	st := newTestStore(t)
	_, err := st.GetAISImport(context.Background(), 2025)
	if err != ErrNotFound {
		t.Errorf("GetAISImport on empty DB: err = %v, want ErrNotFound", err)
	}
}

func TestAISImport_SaveThenGet_Roundtrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	if err := st.SaveAISImport(ctx, 2025, "/data/ais/fy2025.json"); err != nil {
		t.Fatalf("SaveAISImport: %v", err)
	}

	got, err := st.GetAISImport(ctx, 2025)
	if err != nil {
		t.Fatalf("GetAISImport: %v", err)
	}
	if got.FYStartYear != 2025 {
		t.Errorf("FYStartYear = %d, want 2025", got.FYStartYear)
	}
	if got.RawJSONPath != "/data/ais/fy2025.json" {
		t.Errorf("RawJSONPath = %q, want %q", got.RawJSONPath, "/data/ais/fy2025.json")
	}
	if got.ImportedAt == "" {
		t.Error("ImportedAt should not be empty")
	}
}

func TestAISImport_SaveTwice_Overwrites(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	_ = st.SaveAISImport(ctx, 2025, "/old/path.json")
	_ = st.SaveAISImport(ctx, 2025, "/new/path.json")

	got, err := st.GetAISImport(ctx, 2025)
	if err != nil {
		t.Fatalf("GetAISImport: %v", err)
	}
	if got.RawJSONPath != "/new/path.json" {
		t.Errorf("RawJSONPath = %q, want %q (should be overwritten)", got.RawJSONPath, "/new/path.json")
	}
}

func TestAISImport_ListMultiple(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	_ = st.SaveAISImport(ctx, 2024, "/data/ais/fy2024.json")
	_ = st.SaveAISImport(ctx, 2025, "/data/ais/fy2025.json")

	imports, err := st.ListAISImports(ctx)
	if err != nil {
		t.Fatalf("ListAISImports: %v", err)
	}
	if len(imports) != 2 {
		t.Fatalf("ListAISImports: got %d, want 2", len(imports))
	}
	if imports[0].FYStartYear != 2025 {
		t.Errorf("ListAISImports[0].FYStartYear = %d, want 2025 (descending)", imports[0].FYStartYear)
	}
	if imports[1].FYStartYear != 2024 {
		t.Errorf("ListAISImports[1].FYStartYear = %d, want 2024", imports[1].FYStartYear)
	}
}

func TestGetFYEmployerTDS_Empty(t *testing.T) {
	st := newTestStore(t)
	result, err := st.GetFYEmployerTDS(context.Background(), 2025)
	if err != nil {
		t.Fatalf("GetFYEmployerTDS: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("GetFYEmployerTDS on empty DB: got %d, want 0", len(result))
	}
}

func TestGetFYEmployerTDS_PerEmployer(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	p1 := samplePayslip(t, st, 5, 2025, "ACME Corp")
	p1.GrossSalary = 200000
	p1.Components[1].Amount = -25000
	if err := st.SavePayslip(ctx, &p1); err != nil {
		t.Fatalf("SavePayslip p1: %v", err)
	}
	if err := st.ConfirmPayslip(ctx, p1.ID); err != nil {
		t.Fatalf("ConfirmPayslip p1: %v", err)
	}

	p2 := samplePayslip(t, st, 8, 2025, "ACME Corp")
	p2.GrossSalary = 200000
	p2.Components[1].Amount = -25000
	if err := st.SavePayslip(ctx, &p2); err != nil {
		t.Fatalf("SavePayslip p2: %v", err)
	}
	if err := st.ConfirmPayslip(ctx, p2.ID); err != nil {
		t.Fatalf("ConfirmPayslip p2: %v", err)
	}

	p3 := samplePayslip(t, st, 3, 2026, "Beta Inc")
	p3.GrossSalary = 100000
	p3.Components[1].Amount = -10000
	if err := st.SavePayslip(ctx, &p3); err != nil {
		t.Fatalf("SavePayslip p3: %v", err)
	}
	if err := st.ConfirmPayslip(ctx, p3.ID); err != nil {
		t.Fatalf("ConfirmPayslip p3: %v", err)
	}

	result, err := st.GetFYEmployerTDS(ctx, 2025)
	if err != nil {
		t.Fatalf("GetFYEmployerTDS: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("GetFYEmployerTDS: got %d employers, want 2", len(result))
	}

	var acme, beta *EmployerTDS
	for i := range result {
		if result[i].EmployerName == "ACME Corp" {
			acme = &result[i]
		}
		if result[i].EmployerName == "Beta Inc" {
			beta = &result[i]
		}
	}

	if acme == nil {
		t.Fatal("ACME Corp not found in results")
	}
	if acme.GrossSalary != 400000 {
		t.Errorf("ACME GrossSalary = %v, want 400000", acme.GrossSalary)
	}
	if acme.TDS != 50000 {
		t.Errorf("ACME TDS = %v, want 50000", acme.TDS)
	}
	if acme.PayslipCount != 2 {
		t.Errorf("ACME PayslipCount = %d, want 2", acme.PayslipCount)
	}

	if beta == nil {
		t.Fatal("Beta Inc not found in results")
	}
	if beta.GrossSalary != 100000 {
		t.Errorf("Beta GrossSalary = %v, want 100000", beta.GrossSalary)
	}
	if beta.TDS != 10000 {
		t.Errorf("Beta TDS = %v, want 10000", beta.TDS)
	}
	if beta.PayslipCount != 1 {
		t.Errorf("Beta PayslipCount = %d, want 1", beta.PayslipCount)
	}
}

func TestGetFYEmployerTDS_ExcludesOtherFY(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	p1 := samplePayslip(t, st, 5, 2024, "ACME Corp")
	if err := st.SavePayslip(ctx, &p1); err != nil {
		t.Fatalf("SavePayslip: %v", err)
	}
	if err := st.ConfirmPayslip(ctx, p1.ID); err != nil {
		t.Fatalf("ConfirmPayslip: %v", err)
	}

	result, err := st.GetFYEmployerTDS(ctx, 2025)
	if err != nil {
		t.Fatalf("GetFYEmployerTDS: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("GetFYEmployerTDS for FY2025 with only FY2024 payslips: got %d, want 0", len(result))
	}
}

func TestGetFYEmployerTDS_ExcludesUnconfirmed(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	p1 := samplePayslip(t, st, 5, 2025, "ACME Corp")
	if err := st.SavePayslip(ctx, &p1); err != nil {
		t.Fatalf("SavePayslip: %v", err)
	}

	result, err := st.GetFYEmployerTDS(ctx, 2025)
	if err != nil {
		t.Fatalf("GetFYEmployerTDS: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("GetFYEmployerTDS with only unconfirmed payslips: got %d, want 0", len(result))
	}
}
