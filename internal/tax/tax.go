package tax

import "math"

const (
	standardDeduction = 75000
	rebate87ACap      = 60000
	rebateThreshold   = 1200000
	marginalReliefCap = 1270588
	ltcgExemption     = 125000

	stcgRate = 0.20
	ltcgRate = 0.125

	cessRate = 0.04
)

type slab struct {
	width float64
	rate  float64
}

var slabs = []slab{
	{400000, 0.00},
	{400000, 0.05},
	{400000, 0.10},
	{400000, 0.15},
	{400000, 0.20},
	{400000, 0.25},
	{math.Inf(1), 0.30},
}

type surchargeTier struct {
	threshold float64
	rate      float64
}

var surchargeTiers = []surchargeTier{
	{5000000, 0.10},
	{10000000, 0.15},
	{20000000, 0.25},
}

// Input is the income data needed to compute the new-regime tax liability.
type Input struct {
	GrossSalary     float64
	SavingsInterest float64
	FDInterest      float64
	Dividends       float64
	STCG            float64
	LTCG            float64
}

// Breakdown is the full tax computation result: income split, slab tax,
// rebate, marginal relief, surcharge, cess, and total liability.
type Breakdown struct {
	GrossSalary        float64
	StandardDeduction  float64
	TaxableSalary      float64
	SavingsInterest    float64
	FDInterest         float64
	Dividends          float64
	NormalRateIncome   float64
	STCG               float64
	LTCG               float64
	LTCGExemption      float64
	TaxableSTCG        float64
	TaxableLTCG        float64
	SpecialRateIncome  float64
	TotalTaxableIncome float64

	NormalRateTax     float64
	SpecialRateTax    float64
	Rebate87A         float64
	MarginalRelief    float64
	TaxAfterRelief    float64
	SurchargeRate     float64
	Surcharge         float64
	Cess              float64
	TotalTaxLiability float64
}

// Compute applies the new-regime (Section 115BAC) tax computation for
// FY 2025-26. Income is split into normal-rate (salary + interest +
// dividends) and special-rate (STCG @20% + LTCG @12.5%) portions. Rebate
// 87A applies to normal-rate tax only when total income ≤ ₹12L. Marginal
// relief smooths the ₹12L–₹12.70L band. Surcharge applies above ₹50L with
// CG surcharge capped at 15%. Cess is 4% on (tax + surcharge).
func Compute(in Input) Breakdown {
	b := Breakdown{
		GrossSalary:       in.GrossSalary,
		StandardDeduction: standardDeduction,
		SavingsInterest:   in.SavingsInterest,
		FDInterest:        in.FDInterest,
		Dividends:         in.Dividends,
		STCG:              in.STCG,
		LTCG:              in.LTCG,
	}

	b.TaxableSalary = math.Max(0, in.GrossSalary-standardDeduction)
	b.NormalRateIncome = b.TaxableSalary + in.SavingsInterest + in.FDInterest + in.Dividends

	b.TaxableSTCG = math.Max(0, in.STCG)
	b.LTCGExemption = math.Min(in.LTCG, ltcgExemption)
	b.TaxableLTCG = math.Max(0, in.LTCG-ltcgExemption)
	b.SpecialRateIncome = b.TaxableSTCG + b.TaxableLTCG

	b.TotalTaxableIncome = b.NormalRateIncome + b.SpecialRateIncome

	b.NormalRateTax = slabTax(b.NormalRateIncome)
	b.SpecialRateTax = stcgRate*b.TaxableSTCG + ltcgRate*b.TaxableLTCG

	if b.TotalTaxableIncome <= rebateThreshold {
		b.Rebate87A = math.Min(b.NormalRateTax, rebate87ACap)
	}

	normalAfterRebate := b.NormalRateTax - b.Rebate87A

	if b.TotalTaxableIncome > rebateThreshold && b.TotalTaxableIncome <= marginalReliefCap {
		excess := b.TotalTaxableIncome - rebateThreshold
		if normalAfterRebate > excess {
			b.MarginalRelief = normalAfterRebate - excess
		}
	}

	b.TaxAfterRelief = normalAfterRebate - b.MarginalRelief + b.SpecialRateTax

	surchargeRate := surchargeFor(b.TotalTaxableIncome)
	b.SurchargeRate = surchargeRate
	if surchargeRate > 0 {
		normalSurcharge := surchargeRate * (normalAfterRebate - b.MarginalRelief)
		specialSurcharge := math.Min(surchargeRate, 0.15) * b.SpecialRateTax
		b.Surcharge = normalSurcharge + specialSurcharge
	}

	b.Cess = cessRate * (b.TaxAfterRelief + b.Surcharge)
	b.TotalTaxLiability = b.TaxAfterRelief + b.Surcharge + b.Cess

	return b
}

func slabTax(income float64) float64 {
	var tax float64
	remaining := income
	for _, s := range slabs {
		if remaining <= 0 {
			break
		}
		taxable := math.Min(remaining, s.width)
		tax += taxable * s.rate
		remaining -= taxable
	}
	return tax
}

func surchargeFor(income float64) float64 {
	var rate float64
	for _, t := range surchargeTiers {
		if income > t.threshold {
			rate = t.rate
		}
	}
	return rate
}
