package tax

import (
	"math"
	"testing"
)

func approxEqual(a, b float64) bool {
	return math.Abs(a-b) < 1.0
}

func TestCompute_ZeroIncome(t *testing.T) {
	b := Compute(Input{})
	if b.TotalTaxLiability != 0 {
		t.Errorf("zero income: tax = %.2f, want 0", b.TotalTaxLiability)
	}
	if b.TaxableSalary != 0 {
		t.Errorf("zero income: taxable salary = %.2f, want 0", b.TaxableSalary)
	}
}

func TestCompute_BelowRebateThreshold_TaxFree(t *testing.T) {
	b := Compute(Input{GrossSalary: 1275000})
	if b.TaxableSalary != 1200000 {
		t.Errorf("taxable salary = %.2f, want 1200000", b.TaxableSalary)
	}
	if b.NormalRateTax != 60000 {
		t.Errorf("normal rate tax = %.2f, want 60000", b.NormalRateTax)
	}
	if b.Rebate87A != 60000 {
		t.Errorf("rebate = %.2f, want 60000", b.Rebate87A)
	}
	if b.TotalTaxLiability != 0 {
		t.Errorf("total tax = %.2f, want 0 (rebate covers all)", b.TotalTaxLiability)
	}
}

func TestCompute_JustAboveRebate_MarginalRelief(t *testing.T) {
	b := Compute(Input{GrossSalary: 1275001})
	if b.TaxableSalary != 1200001 {
		t.Errorf("taxable salary = %.2f, want 1200001", b.TaxableSalary)
	}
	excess := b.TotalTaxableIncome - rebateThreshold
	if !approxEqual(b.TaxAfterRelief, excess) {
		t.Errorf("marginal relief: tax = %.2f, want %.2f (excess over 12L)", b.TaxAfterRelief, excess)
	}
	if b.MarginalRelief <= 0 {
		t.Error("marginal relief should be positive just above 12L")
	}
}

func TestCompute_MarginalRelief_OneRupeeOver(t *testing.T) {
	b := Compute(Input{GrossSalary: 1275001})
	excess := b.TotalTaxableIncome - rebateThreshold
	if !approxEqual(b.TaxAfterRelief, excess) {
		t.Errorf("₹1 over 12L: tax = %.2f, want %.2f", b.TaxAfterRelief, excess)
	}
}

func TestCompute_AboveMarginalReliefBreakEven(t *testing.T) {
	b := Compute(Input{GrossSalary: 1500000})
	if b.MarginalRelief != 0 {
		t.Errorf("above break-even: marginal relief = %.2f, want 0", b.MarginalRelief)
	}
	expectedSlabTax := slabTax(1425000)
	if !approxEqual(b.NormalRateTax, expectedSlabTax) {
		t.Errorf("normal rate tax = %.2f, want %.2f", b.NormalRateTax, expectedSlabTax)
	}
}

func TestCompute_NoRebate_NoSurcharge(t *testing.T) {
	b := Compute(Input{GrossSalary: 2142141, SavingsInterest: 49250, FDInterest: 29057, Dividends: 1679, STCG: 6130})

	if !approxEqual(b.NormalRateTax, 236782) {
		t.Errorf("normal rate tax = %.2f, want ~236782", b.NormalRateTax)
	}
	if !approxEqual(b.SpecialRateTax, 1226) {
		t.Errorf("special rate tax = %.2f, want 1226", b.SpecialRateTax)
	}
	if b.Rebate87A != 0 {
		t.Errorf("rebate = %.2f, want 0 (income > 12L)", b.Rebate87A)
	}
	if b.Surcharge != 0 {
		t.Errorf("surcharge = %.2f, want 0 (income < 50L)", b.Surcharge)
	}
	if !approxEqual(b.TotalTaxLiability, 247528) {
		t.Errorf("total tax = %.2f, want ~247528", b.TotalTaxLiability)
	}
}

func TestCompute_Surcharge10Percent(t *testing.T) {
	b := Compute(Input{GrossSalary: 5100000})
	if b.SurchargeRate != 0.10 {
		t.Errorf("surcharge rate = %.2f, want 0.10 (income > 50L)", b.SurchargeRate)
	}
	if b.Surcharge <= 0 {
		t.Error("surcharge should be positive above 50L")
	}
}

func TestCompute_Surcharge15Percent(t *testing.T) {
	b := Compute(Input{GrossSalary: 10100000})
	if b.SurchargeRate != 0.15 {
		t.Errorf("surcharge rate = %.2f, want 0.15 (income > 1cr)", b.SurchargeRate)
	}
}

func TestCompute_Surcharge25Percent(t *testing.T) {
	b := Compute(Input{GrossSalary: 20100000})
	if b.SurchargeRate != 0.25 {
		t.Errorf("surcharge rate = %.2f, want 0.25 (income > 2cr)", b.SurchargeRate)
	}
}

func TestCompute_PureCGIncome_RebateExcluded(t *testing.T) {
	b := Compute(Input{LTCG: 500000})
	if b.TaxableLTCG != 375000 {
		t.Errorf("taxable LTCG = %.2f, want 375000 (after 1.25L exemption)", b.TaxableLTCG)
	}
	if b.SpecialRateTax != 46875 {
		t.Errorf("special rate tax = %.2f, want 46875", b.SpecialRateTax)
	}
	if b.Rebate87A != 0 {
		t.Errorf("rebate = %.2f, want 0 (no normal-rate income)", b.Rebate87A)
	}
	if !approxEqual(b.TotalTaxLiability, 46875*1.04) {
		t.Errorf("total tax = %.2f, want %.2f (special tax + cess)", b.TotalTaxLiability, 46875*1.04)
	}
}

func TestCompute_STCG_NoExemption(t *testing.T) {
	b := Compute(Input{STCG: 10000})
	if b.TaxableSTCG != 10000 {
		t.Errorf("taxable STCG = %.2f, want 10000 (no exemption for STCG)", b.TaxableSTCG)
	}
	if b.SpecialRateTax != 2000 {
		t.Errorf("special rate tax = %.2f, want 2000", b.SpecialRateTax)
	}
}

func TestCompute_LTCG_BelowExemption(t *testing.T) {
	b := Compute(Input{LTCG: 100000})
	if b.TaxableLTCG != 0 {
		t.Errorf("taxable LTCG = %.2f, want 0 (below 1.25L exemption)", b.TaxableLTCG)
	}
	if b.SpecialRateTax != 0 {
		t.Errorf("special rate tax = %.2f, want 0", b.SpecialRateTax)
	}
}

func TestCompute_CGWithSalary_RebateOnNormalOnly(t *testing.T) {
	b := Compute(Input{GrossSalary: 1000000, LTCG: 200000})
	if b.TaxableSalary != 925000 {
		t.Errorf("taxable salary = %.2f, want 925000", b.TaxableSalary)
	}
	if b.TaxableLTCG != 75000 {
		t.Errorf("taxable LTCG = %.2f, want 75000 (200000 - 125000 exemption)", b.TaxableLTCG)
	}
	if b.TotalTaxableIncome != 1000000 {
		t.Errorf("total taxable income = %.2f, want 1000000", b.TotalTaxableIncome)
	}
	if b.TotalTaxableIncome > rebateThreshold {
		t.Error("total income should be ≤ 12L for rebate to apply")
	}
	if b.Rebate87A <= 0 {
		t.Error("rebate should apply (total income ≤ 12L)")
	}
	if b.TaxAfterRelief != b.SpecialRateTax {
		t.Errorf("tax after relief = %.2f, want %.2f (only CG tax remains after rebate)", b.TaxAfterRelief, b.SpecialRateTax)
	}
}

func TestCompute_CessIs4Percent(t *testing.T) {
	b := Compute(Input{GrossSalary: 2000000})
	expectedCess := 0.04 * b.TaxAfterRelief
	if !approxEqual(b.Cess, expectedCess) {
		t.Errorf("cess = %.2f, want %.2f", b.Cess, expectedCess)
	}
}

func TestCompute_SlabTax(t *testing.T) {
	tests := []struct {
		income float64
		want   float64
	}{
		{0, 0},
		{400000, 0},
		{800000, 20000},
		{1200000, 60000},
		{1600000, 120000},
		{2000000, 200000},
		{2400000, 300000},
		{2800000, 420000},
	}
	for _, tc := range tests {
		got := slabTax(tc.income)
		if !approxEqual(got, tc.want) {
			t.Errorf("slabTax(%.0f) = %.2f, want %.2f", tc.income, got, tc.want)
		}
	}
}

func TestCompute_SurchargeFor(t *testing.T) {
	tests := []struct {
		income float64
		want   float64
	}{
		{4000000, 0},
		{5000000, 0},
		{5000001, 0.10},
		{10000000, 0.10},
		{10000001, 0.15},
		{20000000, 0.15},
		{20000001, 0.25},
	}
	for _, tc := range tests {
		got := surchargeFor(tc.income)
		if got != tc.want {
			t.Errorf("surchargeFor(%.0f) = %.2f, want %.2f", tc.income, got, tc.want)
		}
	}
}
