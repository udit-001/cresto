package itr

import (
	"encoding/json"
	"strings"
	"testing"

	"cresto/internal/ais"
	"cresto/internal/store"
	"cresto/internal/tax"
)

func testInput() Input {
	return Input{
		Profile: store.TaxpayerProfile{
			PAN:               "ABCDE1234F",
			DOB:               "15061990",
			DeclarantName:     "Test User",
			VerificationPlace: "Bangalore",
		},
		BankAccounts: []store.BankAccount{
			{IFSC: "HDFC0001234", AccountNumber: "1234567890", AccountType: "savings", IsPrimary: true},
		},
		AIS: ais.ParsedAIS{
			FY: "2025-26",
			Salaries: []ais.SalaryEntry{
				{Employer: "Test Corp", TAN: "TAN001", GrossSalary: 500000, TDS: 50000},
			},
			SavingsInterest: []ais.InterestEntry{
				{Bank: "HDFC Bank", Amount: 24772},
			},
			FDInterest: []ais.InterestEntry{
				{Bank: "Axis Bank", Amount: 8600},
			},
			Dividends: []ais.DividendEntry{
				{Company: "Castrol", Amount: 875},
			},
			TDS: []ais.TDSEntry{
				{Deductor: "Test Corp", TAN: "TAN001", Section: "192", Income: 500000, TDS: 50000},
				{Deductor: "Ujjivan SFB", TAN: "DELU001", Section: "194A", Income: 10669, TDS: 0},
			},
			AdvanceTax: []ais.AdvanceTaxEntry{
				{FY: "2024-25", Total: 24150, BSRCode: "0510016", Date: "16/09/2025", Challan: "62647"},
			},
		},
		CGTrades: []store.CapitalGainsTrade{
			{Section: "Equity - Short Term", Symbol: "NETWEB", ISIN: "INE0NT", BuyValue: 2785, SellValue: 3465, TaxableProfit: 680, STT: 3.46},
		},
		TaxBreakdown: tax.Compute(tax.Input{
			GrossSalary:     500000,
			SavingsInterest: 24772,
			FDInterest:      8600,
			Dividends:       875,
			STCG:            680,
		}),
		FYStartYear: 2025,
	}
}

func getITR2(t *testing.T, in Input) map[string]any {
	t.Helper()
	data, err := Generate(in)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	itr, ok := doc["ITR"].(map[string]any)
	if !ok {
		t.Fatal("missing ITR key")
	}
	itr2, ok := itr["ITR2"].(map[string]any)
	if !ok {
		t.Fatal("missing ITR2 key")
	}
	return itr2
}

func TestGenerate_HasRequiredSchedules(t *testing.T) {
	itr2 := getITR2(t, testInput())
	required := []string{"CreationInfo", "Form_ITR2", "PartA_GEN1", "ScheduleS", "ScheduleOS",
		"ScheduleCGFor23", "ScheduleVIA", "ScheduleCYLA", "ScheduleBFLA",
		"ScheduleTDS1", "ScheduleTDS2", "ScheduleIT", "PartB-TI", "PartB_TTI", "Verification"}
	for _, s := range required {
		if _, ok := itr2[s]; !ok {
			t.Errorf("missing required schedule: %s", s)
		}
	}
}

func TestGenerate_OmitsOptionalSchedules(t *testing.T) {
	itr2 := getITR2(t, testInput())
	omitted := []string{"ScheduleHP", "ScheduleVDA", "Schedule80C", "Schedule80D",
		"ScheduleAMT", "ScheduleSPI", "ScheduleSI", "ScheduleFSI", "ScheduleFA",
		"ScheduleAL", "ScheduleTDS3", "ScheduleTCS", "ScheduleESOP"}
	for _, s := range omitted {
		if _, ok := itr2[s]; ok {
			t.Errorf("optional schedule should be omitted: %s", s)
		}
	}
}

func TestGenerate_NoPAN_ReturnsError(t *testing.T) {
	in := testInput()
	in.Profile.PAN = ""
	if _, err := Generate(in); err == nil {
		t.Fatal("Generate without PAN: want error, got nil")
	}
}

func TestGenerate_ScheduleS_SalaryPerEmployer(t *testing.T) {
	itr2 := getITR2(t, testInput())
	schS, ok := itr2["ScheduleS"].(map[string]any)
	if !ok {
		t.Fatal("missing ScheduleS")
	}
	salaries, ok := schS["Salaries"].([]any)
	if !ok {
		t.Fatal("ScheduleS.Salaries not an array")
	}
	if len(salaries) != 1 {
		t.Errorf("Salaries: got %d entries, want 1", len(salaries))
	}
	first := salaries[0].(map[string]any)
	if first["NameOfEmployer"] != "Test Corp" {
		t.Errorf("NameOfEmployer = %v, want Test Corp", first["NameOfEmployer"])
	}
	if first["TANofEmployer"] != "TAN001" {
		t.Errorf("TANofEmployer = %v, want TAN001", first["TANofEmployer"])
	}
	salarys := first["Salarys"].(map[string]any)
	if salarys["GrossSalary"] != float64(500000) {
		t.Errorf("GrossSalary = %v, want 500000", salarys["GrossSalary"])
	}
}

func TestGenerate_ScheduleS_StandardDeduction(t *testing.T) {
	itr2 := getITR2(t, testInput())
	schS := itr2["ScheduleS"].(map[string]any)
	if schS["DeductionUnderSection16ia"] != float64(75000) {
		t.Errorf("Standard deduction = %v, want 75000", schS["DeductionUnderSection16ia"])
	}
	if schS["TotIncUnderHeadSalaries"] != float64(425000) {
		t.Errorf("TotIncUnderHeadSalaries = %v, want 425000 (500000-75000)", schS["TotIncUnderHeadSalaries"])
	}
}

func TestGenerate_ScheduleOS_InterestSplit(t *testing.T) {
	itr2 := getITR2(t, testInput())
	schOS := itr2["ScheduleOS"].(map[string]any)
	iorh := schOS["IncOthThanOwnRaceHorse"].(map[string]any)
	if iorh["IntrstFrmSavingBank"] != float64(24772) {
		t.Errorf("Savings interest = %v, want 24772", iorh["IntrstFrmSavingBank"])
	}
	if iorh["IntrstFrmTermDeposit"] != float64(8600) {
		t.Errorf("FD interest = %v, want 8600", iorh["IntrstFrmTermDeposit"])
	}
	if iorh["DividendGross"] != float64(875) {
		t.Errorf("Dividends = %v, want 875", iorh["DividendGross"])
	}
}

func TestGenerate_ScheduleTDS1_SalaryTDS(t *testing.T) {
	itr2 := getITR2(t, testInput())
	schTDS1 := itr2["ScheduleTDS1"].(map[string]any)
	entries := schTDS1["TDSonSalary"].([]any)
	if len(entries) != 1 {
		t.Errorf("TDSonSalary: got %d, want 1", len(entries))
	}
	first := entries[0].(map[string]any)
	detl := first["EmployerOrDeductorOrCollectDetl"].(map[string]any)
	if detl["TAN"] != "TAN001" {
		t.Errorf("TAN = %v, want TAN001", detl["TAN"])
	}
	if first["TotalTDSSal"] != float64(50000) {
		t.Errorf("TotalTDSSal = %v, want 50000", first["TotalTDSSal"])
	}
}

func TestGenerate_ScheduleTDS2_NonSalaryTDS(t *testing.T) {
	itr2 := getITR2(t, testInput())
	schTDS2 := itr2["ScheduleTDS2"].(map[string]any)
	entries := schTDS2["TDSOthThanSalaryDtls"].([]any)
	if len(entries) != 1 {
		t.Errorf("TDSOthThanSalaryDtls: got %d, want 1 (only 194A, not 192)", len(entries))
	}
}

func TestGenerate_ScheduleIT_AdvanceTax(t *testing.T) {
	itr2 := getITR2(t, testInput())
	schIT := itr2["ScheduleIT"].(map[string]any)
	payments := schIT["TaxPayment"].([]any)
	if len(payments) != 1 {
		t.Errorf("TaxPayment: got %d, want 1", len(payments))
	}
	first := payments[0].(map[string]any)
	if first["BSRCode"] != "0510016" {
		t.Errorf("BSRCode = %v, want 0510016", first["BSRCode"])
	}
}

func TestGenerate_BankAccount(t *testing.T) {
	itr2 := getITR2(t, testInput())
	tti := itr2["PartB_TTI"].(map[string]any)
	refund := tti["Refund"].(map[string]any)
	bank := refund["BankAccountDetail"].(map[string]any)
	if bank["IFSCCode"] != "HDFC0001234" {
		t.Errorf("IFSC = %v, want HDFC0001234", bank["IFSCCode"])
	}
	if bank["BankAccountNumber"] != "1234567890" {
		t.Errorf("Account number = %v, want 1234567890", bank["BankAccountNumber"])
	}
}

func TestGenerate_Verification(t *testing.T) {
	itr2 := getITR2(t, testInput())
	verif := itr2["Verification"].(map[string]any)
	decl := verif["Declaration"].(map[string]any)
	if decl["AssesseeName"] != "Test User" {
		t.Errorf("Declaration.AssesseeName = %v, want Test User", decl["AssesseeName"])
	}
	if verif["Place"] != "Bangalore" {
		t.Errorf("Verification.Place = %v, want Bangalore", verif["Place"])
	}
}

func TestGenerate_NewRegimeFlag(t *testing.T) {
	itr2 := getITR2(t, testInput())
	pa := itr2["PartA_GEN1"].(map[string]any)
	fs := pa["FilingStatus"].(map[string]any)
	if fs["OptOutNewTaxRegime"] != "N" {
		t.Errorf("OptOutNewTaxRegime = %v, want N (new regime)", fs["OptOutNewTaxRegime"])
	}
}

func TestGenerate_CreationInfo(t *testing.T) {
	itr2 := getITR2(t, testInput())
	ci := itr2["CreationInfo"].(map[string]any)
	if ci["SWCreatedBy"] != "Cresto" {
		t.Errorf("SWCreatedBy = %v, want Cresto", ci["SWCreatedBy"])
	}
}

func TestGenerate_FormName(t *testing.T) {
	itr2 := getITR2(t, testInput())
	fi := itr2["Form_ITR2"].(map[string]any)
	if fi["FormName"] != "ITR-2" {
		t.Errorf("FormName = %v, want ITR-2", fi["FormName"])
	}
	if fi["AssessmentYear"] != "2026" {
		t.Errorf("AssessmentYear = %v, want 2026", fi["AssessmentYear"])
	}
}

func TestGenerate_Schedule112A_OmittedWithoutLTCG(t *testing.T) {
	itr2 := getITR2(t, testInput())
	if _, ok := itr2["Schedule112A"]; ok {
		t.Error("Schedule112A should be omitted when no LTCG trades")
	}
}

func TestGenerate_Schedule112A_PresentWithLTCG(t *testing.T) {
	in := testInput()
	in.CGTrades = append(in.CGTrades, store.CapitalGainsTrade{
		Section: "Equity - Long Term", Symbol: "RELIANCE", ISIN: "INE002",
		EntryDate: "2023-01-15", ExitDate: "2025-12-10",
		BuyValue: 20000, SellValue: 25000, TaxableProfit: 3000, FMV: 22000,
	})
	itr2 := getITR2(t, in)
	sch112A, ok := itr2["Schedule112A"].(map[string]any)
	if !ok {
		t.Fatal("Schedule112A should be present with LTCG trades")
	}
	dtls := sch112A["Schedule112ADtls"].([]any)
	if len(dtls) != 1 {
		t.Errorf("Schedule112ADtls: got %d, want 1", len(dtls))
	}
}

func TestGenerate_AmountsAreIntegers(t *testing.T) {
	data, _ := Generate(testInput())
	str := string(data)
	if strings.Contains(str, "500000.0") || strings.Contains(str, "500000.5") {
		t.Error("JSON should contain integer amounts, found float")
	}
}

func TestGenerate_TaxComputation(t *testing.T) {
	itr2 := getITR2(t, testInput())
	tti := itr2["PartB_TTI"].(map[string]any)
	comp := tti["ComputationOfTaxLiability"].(map[string]any)
	taxPayable := comp["TaxPayableOnTI"].(map[string]any)
	if taxPayable["TaxAtSpecialRates"] == nil {
		t.Error("TaxAtSpecialRates should be present")
	}
	if _, ok := comp["Rebate87A"]; !ok {
		t.Error("Rebate87A should be present")
	}
}

func TestGenerate_ValidJSON(t *testing.T) {
	data, err := Generate(testInput())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("generated JSON is not valid: %v", err)
	}
}
