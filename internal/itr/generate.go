package itr

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"cresto/internal/ais"
	"cresto/internal/store"
	"cresto/internal/tax"
)

const (
	schemaVersion = "V1.1"
	formVersion   = "V1.1"
	assessmentYr  = "2026"
)

// Input bundles everything the ITR-2 JSON needs: taxpayer identity, bank
// account, AIS-parsed income/TDS, Kite Console capital gains, and the
// computed tax breakdown.
type Input struct {
	Profile      store.TaxpayerProfile
	BankAccounts []store.BankAccount
	AIS          ais.ParsedAIS
	CGTrades     []store.CapitalGainsTrade
	TaxBreakdown tax.Breakdown
	FYStartYear  int
}

// Generate produces a schema-conformant ITR-2 JSON for AY 2026-27 (new
// regime). All amounts are integer rupees (rounded). Populated schedules:
// PartA_GEN1, ScheduleS, ScheduleOS, ScheduleCGFor23, Schedule112A (if
// LTCG), ScheduleVIA (zeros), ScheduleTDS1, ScheduleTDS2, ScheduleIT,
// PartB-TI, PartB-TTI, bank account, Verification. Required-but-zero:
// CreationInfo, Form_ITR2, ScheduleCYLA, ScheduleBFLA.
func Generate(in Input) ([]byte, error) {
	if in.Profile.PAN == "" {
		return nil, fmt.Errorf("itr: taxpayer profile PAN is required")
	}

	b := in.TaxBreakdown
	primaryBank := primaryAccount(in.BankAccounts)

	doc := map[string]any{
		"ITR": map[string]any{
			"ITR2": map[string]any{
				"CreationInfo": map[string]any{
					"SWVersionNo":      "1.2.2",
					"SWCreatedBy":      "Cresto",
					"JSONCreatedBy":    "Cresto",
					"JSONCreationDate": time.Now().Format("02/01/2006 15:04:05"),
					"IntermediaryCity": "",
					"Digest":           "",
				},
				"Form_ITR2": map[string]any{
					"FormName":       "ITR-2",
					"Description":    "For Individuals and HUFs not having income from profits and gains of business or profession",
					"AssessmentYear": assessmentYr,
					"SchemaVer":      schemaVersion,
					"FormVer":        formVersion,
				},
				"PartA_GEN1":      buildPartA(in),
				"ScheduleS":       buildScheduleS(in),
				"ScheduleOS":      buildScheduleOS(in),
				"ScheduleCGFor23": buildScheduleCG(in),
				"ScheduleVIA":     buildScheduleVIA(),
				"ScheduleCYLA":    buildScheduleCYLA(),
				"ScheduleBFLA":    buildScheduleBFLA(),
				"ScheduleTDS1":    buildScheduleTDS1(in),
				"ScheduleTDS2":    buildScheduleTDS2(in),
				"ScheduleIT":      buildScheduleIT(in),
				"PartB-TI":        buildPartBTI(b),
				"PartB_TTI":       buildPartBTTI(b, primaryBank),
				"Verification":    buildVerification(in),
			},
		},
	}

	if hasLTCG(in.CGTrades) {
		doc["ITR"].(map[string]any)["ITR2"].(map[string]any)["Schedule112A"] = buildSchedule112A(in)
	}

	return json.MarshalIndent(doc, "", "  ")
}

func buildPartA(in Input) map[string]any {
	nameParts := strings.Fields(in.Profile.DeclarantName)
	first, middle, sur := splitName(nameParts)
	return map[string]any{
		"PersonalInfo": map[string]any{
			"AssesseeName": map[string]any{
				"FirstName":        first,
				"MiddleName":       middle,
				"SurNameOrOrgName": sur,
			},
			"PAN":     in.Profile.PAN,
			"DOB":     formatDOB(in.Profile.DOB),
			"Status":  "I",
			"Address": map[string]any{},
		},
		"FilingStatus": map[string]any{
			"ReturnFileSec":            139,
			"OptOutNewTaxRegime":       "N",
			"SeventhProvisio139":       "No",
			"ResidentialStatus":        "RES",
			"FiiFpiFlag":               "N",
			"HeldUnlistedEqShrPrYrFlg": "N",
			"ItrFilingDueDate":         "31/07/2026",
		},
	}
}

func buildScheduleS(in Input) map[string]any {
	salaries := []map[string]any{}
	var totalGross float64
	for _, sal := range in.AIS.Salaries {
		totalGross += sal.GrossSalary
		salaries = append(salaries, map[string]any{
			"NameOfEmployer":     sal.Employer,
			"NatureOfEmployment": "PE",
			"TANofEmployer":      sal.TAN,
			"AddressDetail":      map[string]any{"AddrDetail": "", "CityOrTownOrDistrict": "", "StateCode": "99", "PinCode": 0, "ZipCode": ""},
			"Salarys": map[string]any{
				"GrossSalary":            toInt(sal.GrossSalary),
				"Salary":                 0,
				"ValueOfPerquisites":     0,
				"ProfitsinLieuOfSalary":  0,
				"IncomeNotified89A":      0,
				"IncomeNotifiedOther89A": 0,
			},
		})
	}
	standardDed := 75000
	netSalary := toInt(totalGross)
	totInc := netSalary - standardDed
	if totInc < 0 {
		totInc = 0
	}
	return map[string]any{
		"Salaries":                  salaries,
		"TotalGrossSalary":          netSalary,
		"AllwncExtentExemptUs10":    0,
		"NetSalary":                 netSalary,
		"DeductionUS16":             standardDed,
		"DeductionUnderSection16ia": standardDed,
		"EntertainmntalwncUs16ii":   0,
		"ProfessionalTaxUs16iii":    0,
		"TotIncUnderHeadSalaries":   totInc,
	}
}

func buildScheduleOS(in Input) map[string]any {
	var savings, fd, dividends float64
	for _, si := range in.AIS.SavingsInterest {
		savings += si.Amount
	}
	for _, fi := range in.AIS.FDInterest {
		fd += fi.Amount
	}
	for _, d := range in.AIS.Dividends {
		dividends += d.Amount
	}
	totalInterest := toInt(savings + fd)
	incChargeable := totalInterest + toInt(dividends)
	return map[string]any{
		"IncOthThanOwnRaceHorse": map[string]any{
			"GrossIncChrgblTaxAtAppRate": 0,
			"DividendGross":              toInt(dividends),
			"InterestGross":              totalInterest,
			"IntrstFrmSavingBank":        toInt(savings),
			"IntrstFrmTermDeposit":       toInt(fd),
			"IntrstFrmIncmTaxRefund":     0,
			"IntrstFrmOthers":            0,
			"NatofPassThrghIncome":       0,
			"RentFromMachPlantBldgs":     0,
			"Tot562x":                    0,
			"Aggrtvaluewithoutcons562x":  0,
			"Immovpropwithoutcons562x":   0,
			"Immovpropinadeqcons562x":    0,
			"OtherIncChargeable":         0,
			"TotIncOthThanOwnRaceHorse":  incChargeable,
		},
		"IncFrmLottery":            map[string]any{"FromDate": "", "ToDate": ""},
		"DividendIncUs115BBDA":     map[string]any{"FromDate": "", "ToDate": ""},
		"DividendIncUs115BBDAaiii": map[string]any{"FromDate": "", "ToDate": ""},
		"DividendIncUs115A1ai":     map[string]any{"FromDate": "", "ToDate": ""},
		"DividendIncUs115AC":       map[string]any{"FromDate": "", "ToDate": ""},
		"DividendIncUs115ACA":      map[string]any{"FromDate": "", "ToDate": ""},
		"DividendIncUs115AD1i":     map[string]any{"FromDate": "", "ToDate": ""},
		"DividendDTAA":             map[string]any{"FromDate": "", "ToDate": ""},
		"NOT89A":                   map[string]any{"FromDate": "", "ToDate": ""},
		"IncChargeable":            incChargeable,
	}
}

func buildScheduleCG(in Input) map[string]any {
	stcgEntries := []map[string]any{}
	var totalSTCG float64
	for _, tr := range in.CGTrades {
		if isSTCG(tr) {
			totalSTCG += tr.TaxableProfit
			stcgEntries = append(stcgEntries, map[string]any{
				"FullConsideration": toInt(tr.SellValue),
				"DeductSec48": map[string]any{
					"AquisitCost": toInt(tr.BuyValue),
					"ExpOnTrans":  0,
				},
				"BalanceCG": toInt(tr.TaxableProfit),
			})
		}
	}
	stcgTotal := toInt(totalSTCG)
	return map[string]any{
		"ShortTermCapGainFor23": map[string]any{
			"EquityMFonSTT":      stcgEntries,
			"NRITransacSec48Dtl": map[string]any{},
			"NRISecur115AD": map[string]any{
				"FullValueConsdRecvUnqshr": map[string]any{},
				"FairMrktValueUnqshr":      map[string]any{},
				"FullValueConsdSec50CA":    map[string]any{},
				"FullValueConsdOthUnqshr":  map[string]any{},
				"FullConsideration":        map[string]any{},
				"DeductSec48":              map[string]any{},
				"BalanceCG":                map[string]any{},
				"LossSec94of7Or94of8":      0,
				"CapgainonAssets":          map[string]any{},
			},
			"SaleOnOtherAssets": map[string]any{
				"FullValueConsdRecvUnqshr": map[string]any{},
				"FairMrktValueUnqshr":      map[string]any{},
				"FullValueConsdSec50CA":    map[string]any{},
				"FullValueConsdOthUnqshr":  map[string]any{},
				"FullConsideration":        map[string]any{},
				"DeductSec48":              map[string]any{},
				"BalanceCG":                map[string]any{},
				"LossSec94of7Or94of8":      0,
				"CapgainonAssets":          map[string]any{},
			},
			"AmtDeemedStcg":            0,
			"TotalAmtDeemedStcg":       0,
			"PassThrIncNatureSTCG":     0,
			"TotalAmtNotTaxUsDTAAStcg": 0,
			"TotalAmtTaxUsDTAAStcg":    0,
			"TotalSTCG":                stcgTotal,
		},
		"LongTermCapGain23": map[string]any{
			"SaleOfEquityShareUs112A": map[string]any{},
			"TotalLTCG":               0,
		},
		"SumOfCGIncm":        stcgTotal,
		"IncmFromVDATrnsf":   0,
		"TotScheduleCGFor23": stcgTotal,
		"CurrYrLosses":       buildCurrYrLosses(stcgTotal),
		"AccruOrRecOfCG":     map[string]any{},
	}
}

func buildSchedule112A(in Input) map[string]any {
	dtls := []map[string]any{}
	for _, tr := range in.CGTrades {
		if !isLTCG(tr) {
			continue
		}
		shareOnOrBefore := "AE"
		if isBeforeJan2018(tr.EntryDate) {
			shareOnOrBefore = "BE"
		}
		dtls = append(dtls, map[string]any{
			"ShareOnOrBefore":          shareOnOrBefore,
			"ISINCode":                 tr.ISIN,
			"ShareUnitName":            tr.Symbol,
			"NumSharesUnits":           tr.Quantity,
			"SalePricePerShareUnit":    safeDiv(tr.SellValue, tr.Quantity),
			"TotSaleValue":             toInt(tr.SellValue),
			"CostAcqWithoutIndx":       toInt(tr.BuyValue),
			"AcquisitionCost":          safeDiv(tr.BuyValue, tr.Quantity),
			"LTCGBeforelowerB1B2":      toInt(tr.TaxableProfit),
			"FairMktValuePerShareunit": safeDiv(tr.FMV, tr.Quantity),
			"TotFairMktValueCapAst":    toInt(tr.FMV),
			"ExpExclCnctTransfer":      0,
			"TotalDeductions":          toInt(tr.BuyValue),
			"Balance":                  toInt(tr.TaxableProfit),
		})
	}
	return map[string]any{
		"Schedule112ADtls":           dtls,
		"TotLTCGOnSaleOfEquityShare": toInt(ltcgTotal(in.CGTrades)),
	}
}

func buildScheduleVIA() map[string]any {
	return map[string]any{
		"UsrDeductUndChapVIA": map[string]any{
			"Section80C":               0,
			"Section80CCC":             0,
			"Section80CCDEmployeeOrSE": 0,
			"Section80CCD1B":           0,
			"Section80CCDEmployer":     0,
			"Section80D":               0,
			"Section80DD":              0,
			"Section80DDB":             0,
			"Section80E":               0,
			"Section80EE":              0,
			"Section80G":               0,
			"Section80GG":              0,
			"Section80GGA":             0,
			"Section80GGC":             0,
			"Section80U":               0,
			"Section80TTA":             0,
			"Section80TTB":             0,
		},
		"DeductUndChapVIA": map[string]any{
			"Section80C":            0,
			"Section80D":            0,
			"TotalDeductUndChapVIA": 0,
		},
	}
}

func buildScheduleCYLA() map[string]any {
	return map[string]any{
		"Salary":       map[string]any{"CurYrLoss": 0, "SetOffCurYr": 0, "BalAfterSetOff": 0},
		"HP":           map[string]any{"CurYrLoss": 0, "SetOffCurYr": 0, "BalAfterSetOff": 0},
		"STCG20Per":    map[string]any{"CurYrLoss": 0, "SetOffCurYr": 0, "BalAfterSetOff": 0},
		"STCG30Per":    map[string]any{"CurYrLoss": 0, "SetOffCurYr": 0, "BalAfterSetOff": 0},
		"STCGAppRate":  map[string]any{"CurYrLoss": 0, "SetOffCurYr": 0, "BalAfterSetOff": 0},
		"LTCG12_5Per":  map[string]any{"CurYrLoss": 0, "SetOffCurYr": 0, "BalAfterSetOff": 0},
		"LTCGDTAARate": map[string]any{"CurYrLoss": 0, "SetOffCurYr": 0, "BalAfterSetOff": 0},
	}
}

func buildScheduleBFLA() map[string]any {
	return map[string]any{
		"Salary":       map[string]any{"BroughtFwdLoss": 0, "SetOffBroughtFwd": 0, "BalAfterSetOff": 0},
		"HP":           map[string]any{"BroughtFwdLoss": 0, "SetOffBroughtFwd": 0, "BalAfterSetOff": 0},
		"STCG20Per":    map[string]any{"BroughtFwdLoss": 0, "SetOffBroughtFwd": 0, "BalAfterSetOff": 0},
		"STCG30Per":    map[string]any{"BroughtFwdLoss": 0, "SetOffBroughtFwd": 0, "BalAfterSetOff": 0},
		"STCGAppRate":  map[string]any{"BroughtFwdLoss": 0, "SetOffBroughtFwd": 0, "BalAfterSetOff": 0},
		"LTCG12_5Per":  map[string]any{"BroughtFwdLoss": 0, "SetOffBroughtFwd": 0, "BalAfterSetOff": 0},
		"LTCGDTAARate": map[string]any{"BroughtFwdLoss": 0, "SetOffBroughtFwd": 0, "BalAfterSetOff": 0},
	}
}

func buildScheduleTDS1(in Input) map[string]any {
	entries := []map[string]any{}
	var totalTDS int
	for _, sal := range in.AIS.Salaries {
		tdsAmt := toInt(sal.TDS)
		totalTDS += tdsAmt
		entries = append(entries, map[string]any{
			"EmployerOrDeductorOrCollectDetl": map[string]any{
				"TAN":                               sal.TAN,
				"EmployerOrDeductorOrCollecterName": sal.Employer,
			},
			"IncChrgSal":  toInt(sal.GrossSalary),
			"TotalTDSSal": tdsAmt,
		})
	}
	return map[string]any{
		"TDSonSalary":        entries,
		"TotalTDSonSalaries": totalTDS,
	}
}

func buildScheduleTDS2(in Input) map[string]any {
	entries := []map[string]any{}
	var totalTDS int
	for _, tds := range in.AIS.TDS {
		if tds.Section == "192" {
			continue
		}
		tdsAmt := toInt(tds.TDS)
		totalTDS += tdsAmt
		head := "OS"
		if tds.Section == "194A" || tds.Section == "194K" {
			head = "OS"
		}
		entries = append(entries, map[string]any{
			"TDSCreditName": tds.Deductor,
			"TANOfDeductor": tds.TAN,
			"TDSSection":    tds.Section,
			"TaxDeductCreditDtls": map[string]any{
				"TaxDeductedTDS":     tdsAmt,
				"TaxClaimedTDS":      tdsAmt,
				"TaxClaimedOwnHands": 0,
			},
			"GrossAmount":   toInt(tds.Income),
			"HeadOfIncome":  head,
			"AmtCarriedFwd": map[string]any{},
		})
	}
	return map[string]any{
		"TDSOthThanSalaryDtls":  entries,
		"TotalTDSonOthThanSals": totalTDS,
	}
}

func buildScheduleIT(in Input) map[string]any {
	payments := []map[string]any{}
	var total int
	for _, at := range in.AIS.AdvanceTax {
		amt := toInt(at.Total)
		total += amt
		payments = append(payments, map[string]any{
			"BSRCode":      at.BSRCode,
			"DateDep":      formatDDMMYYYY(at.Date),
			"SrlNoOfChaln": at.Challan,
			"Amt":          amt,
		})
	}
	return map[string]any{
		"TaxPayment":       payments,
		"TotalTaxPayments": total,
	}
}

func buildPartBTI(b tax.Breakdown) map[string]any {
	specialRateInc := toInt(b.TaxableSTCG + b.TaxableLTCG)
	totalIncome := toInt(b.TotalTaxableIncome)
	return map[string]any{
		"Salaries":     toInt(b.TaxableSalary),
		"IncomeFromHP": 0,
		"CapGain": map[string]any{
			"ShortTerm": map[string]any{
				"ShortTerm20Per":       specialRateInc,
				"ShortTerm30Per":       0,
				"ShortTermAppRate":     0,
				"ShortTermSplRateDTAA": 0,
				"TotalShortTerm":       specialRateInc,
			},
			"LongTerm": map[string]any{
				"LongTerm12_5Per":     toInt(b.TaxableLTCG),
				"LongTermSplRateDTAA": 0,
				"TotalLongTerm":       toInt(b.TaxableLTCG),
			},
			"ShortTermLongTermTotal": specialRateInc + toInt(b.TaxableLTCG),
			"CapGains30Per115BBH":    0,
			"TotalCapGains":          specialRateInc + toInt(b.TaxableLTCG),
		},
		"IncFromOS": map[string]any{
			"OtherSrcThanOwnRaceHorse": toInt(b.SavingsInterest + b.FDInterest + b.Dividends),
			"IncChargblSplRate":        0,
			"FromOwnRaceHorse":         0,
			"TotIncFromOS":             toInt(b.SavingsInterest + b.FDInterest + b.Dividends),
		},
		"TotalTI":                                  totalIncome,
		"CurrentYearLoss":                          0,
		"BalanceAfterSetoffLosses":                 totalIncome,
		"BroughtFwdLossesSetoff":                   0,
		"GrossTotalIncome":                         totalIncome,
		"IncChargeTaxSplRate111A112":               specialRateInc,
		"DeductionsUnderScheduleVIA":               0,
		"TotalIncome":                              totalIncome,
		"IncChargeableTaxSplRates":                 specialRateInc,
		"NetAgricultureIncomeOrOtherIncomeForRate": 0,
		"AggregateIncome":                          totalIncome,
		"LossesOfCurrentYearCarriedFwd":            0,
		"DeemedIncomeUs115JC":                      0,
	}
}

func buildPartBTTI(b tax.Breakdown, bank *store.BankAccount) map[string]any {
	surcharge := toInt(b.Surcharge)
	cess := toInt(b.Cess)
	totalTax := toInt(b.TotalTaxLiability)
	bankSection := map[string]any{
		"BankAccountDetail": map[string]any{},
	}
	if bank != nil {
		bankSection = map[string]any{
			"BankAccountDetail": map[string]any{
				"IFSCCode":          bank.IFSC,
				"BankAccountNumber": bank.AccountNumber,
				"AccountType":       strings.Title(bank.AccountType),
			},
		}
	}
	return map[string]any{
		"TaxPayDeemedTotIncUs115JC":  0,
		"Surcharge":                  surcharge,
		"HealthEduCess":              cess,
		"TotalTaxPayablDeemedTotInc": 0,
		"ComputationOfTaxLiability": map[string]any{
			"TaxPayableOnTI": map[string]any{
				"TaxAtNormalRatesOnAggrInc": toInt(b.NormalRateTax),
				"TaxAtSpecialRates":         toInt(b.SpecialRateTax),
				"RebateOnAgriInc":           0,
				"TaxPayableOnTotInc":        toInt(b.NormalRateTax - b.Rebate87A),
			},
			"Rebate87A":                           toInt(b.Rebate87A),
			"TaxPayableOnRebate":                  toInt(b.NormalRateTax - b.Rebate87A),
			"Surcharge25ofSI":                     0,
			"SurchargeOnAboveCrore":               surcharge,
			"Surcharge25ofSIBeforeMarginal":       0,
			"SurchargeOnAboveCroreBeforeMarginal": 0,
			"TotalSurcharge":                      surcharge,
			"EducationCess":                       cess,
			"GrossTaxLiability":                   totalTax,
			"GrossTaxPayable":                     totalTax,
			"NetTaxLiability":                     totalTax,
			"IntrstPay": map[string]any{
				"IntrstPayUs234A":   0,
				"IntrstPayUs234B":   0,
				"IntrstPayUs234C":   0,
				"LateFilingFee234F": 0,
				"TotalIntrstPay":    0,
			},
			"AggregateTaxInterestLiability": totalTax,
		},
		"TaxPaid": map[string]any{
			"TotalTDS":        toInt(b.NormalRateTax + b.SpecialRateTax),
			"TotalTDSClaim":   toInt(b.NormalRateTax + b.SpecialRateTax),
			"TotalAdvTax":     0,
			"TotalSelfAssTax": 0,
			"TotalTAXPaid":    toInt(b.NormalRateTax + b.SpecialRateTax),
		},
		"Refund":            bankSection,
		"AssetOutIndiaFlag": "N",
	}
}

func buildVerification(in Input) map[string]any {
	place := in.Profile.VerificationPlace
	if place == "" {
		place = "India"
	}
	return map[string]any{
		"Declaration": map[string]any{
			"AssesseeName": in.Profile.DeclarantName,
			"PAN":          in.Profile.PAN,
		},
		"Capacity": "Individual",
		"Date":     time.Now().Format("02/01/2006"),
		"Place":    place,
	}
}

func buildCurrYrLosses(stcgTotal int) map[string]any {
	return map[string]any{
		"InLossSetOff": map[string]any{},
		"InStcg20Per": map[string]any{
			"CurrYearIncome":     stcgTotal,
			"StclSetoff30Per":    0,
			"StclSetoffAppRate":  0,
			"StclSetoffDTAARate": 0,
			"CurrYrCapGain":      stcgTotal,
		},
		"InStcg30Per": map[string]any{
			"CurrYearIncome":     0,
			"StclSetoff20Per":    0,
			"StclSetoffAppRate":  0,
			"StclSetoffDTAARate": 0,
			"CurrYrCapGain":      0,
		},
		"InStcgAppRate": map[string]any{
			"CurrYearIncome":     0,
			"StclSetoff20Per":    0,
			"StclSetoff30Per":    0,
			"StclSetoffDTAARate": 0,
			"CurrYrCapGain":      0,
		},
		"InStcgDTAARate": map[string]any{
			"CurrYearIncome":    0,
			"StclSetoff20Per":   0,
			"StclSetoff30Per":   0,
			"StclSetoffAppRate": 0,
			"CurrYrCapGain":     0,
		},
		"InLtcg12_5Per": map[string]any{
			"CurrYearIncome":     0,
			"StclSetoff20Per":    0,
			"StclSetoff30Per":    0,
			"StclSetoffAppRate":  0,
			"StclSetoffDTAARate": 0,
			"LtclSetOffDTAARate": 0,
			"CurrYrCapGain":      0,
		},
		"InLtcgDTAARate": map[string]any{
			"CurrYearIncome":     0,
			"StclSetoff20Per":    0,
			"StclSetoff30Per":    0,
			"StclSetoffAppRate":  0,
			"StclSetoffDTAARate": 0,
			"CurrYrCapGain":      0,
		},
		"TotLossSetOff": map[string]any{
			"TotStclSetoff": 0,
			"TotLtclSetoff": 0,
		},
		"LossRemainSetOff": map[string]any{
			"StclRemainSetoff": 0,
			"LtclRemainSetoff": 0,
		},
	}
}

func primaryAccount(accounts []store.BankAccount) *store.BankAccount {
	for i := range accounts {
		if accounts[i].IsPrimary {
			return &accounts[i]
		}
	}
	if len(accounts) > 0 {
		return &accounts[0]
	}
	return nil
}

func hasLTCG(trades []store.CapitalGainsTrade) bool {
	for _, tr := range trades {
		if isLTCG(tr) {
			return true
		}
	}
	return false
}

func ltcgTotal(trades []store.CapitalGainsTrade) float64 {
	var total float64
	for _, tr := range trades {
		if isLTCG(tr) {
			total += tr.TaxableProfit
		}
	}
	return total
}

func isBeforeJan2018(dateStr string) bool {
	if len(dateStr) >= 10 && dateStr[4] == '-' {
		year := dateStr[:4]
		return year < "2018" || (year == "2018" && dateStr[5:7] <= "01" && dateStr[8:10] <= "31")
	}
	return false
}

func isSTCG(tr store.CapitalGainsTrade) bool {
	return strings.Contains(tr.Section, "Short Term")
}

func isLTCG(tr store.CapitalGainsTrade) bool {
	return strings.Contains(tr.Section, "Long Term")
}

func splitName(parts []string) (first, middle, sur string) {
	if len(parts) == 0 {
		return "", "", ""
	}
	if len(parts) == 1 {
		return parts[0], "", ""
	}
	if len(parts) == 2 {
		return parts[0], "", parts[1]
	}
	return parts[0], strings.Join(parts[1:len(parts)-1], " "), parts[len(parts)-1]
}

func toInt(v float64) int {
	if v >= 0 {
		return int(v + 0.5)
	}
	return int(v - 0.5)
}

func safeDiv(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return a / b
}

func formatDOB(dob string) string {
	if len(dob) == 8 {
		return dob[:2] + "/" + dob[2:4] + "/" + dob[4:]
	}
	return dob
}

func formatDDMMYYYY(dateStr string) string {
	if len(dateStr) == 10 && string(dateStr[2]) == "/" {
		return dateStr
	}
	if len(dateStr) == 10 && string(dateStr[4]) == "-" {
		return dateStr[8:10] + "/" + dateStr[5:7] + "/" + dateStr[:4]
	}
	return dateStr
}
