package ais

import "testing"

const parseFixture = `{
  "header": {"columnData": ["2025-26"]},
  "partB": {
    "sections": [
      {
        "sectionKey": "tdsTcs",
        "title": "TDS/TCS",
        "elements": [
          {
            "title": "Salary",
            "infoSrcId": "TAN001",
            "l2": {
              "columnLabel": ["Information Category","Information Code","Information Description","Information Source","Count","Amount","Information Category Code","Derived Amount","Qualifies For"],
              "columnData": [["Salary","TDS-192","Salary received (Section 192)","ACME CORP PVT LTD (TAN001)","2","5,00,000.00","SAL",null,""]]
            },
            "l1": {
              "columnLabel": [{"field":"tsnId","name":"TSN"},{"field":"quarter","name":"Quarter"},{"field":"transactionDate","name":"Date of Payment/Credit"},{"field":"amtPaid","name":"Amount Paid/Credited"},{"field":"amountDeducted","name":"TDS Deducted"},{"field":"amountDeposited","name":"TDS Deposited"},{"field":"status","name":"Status"},{"field":"transFeedback","name":"Feedback"}],
              "columnData": [
                ["1","Q1","30/06/2025","2,50,000.00","25,000","25,000","Active","Optional"],
                ["2","Q2","30/09/2025","2,50,000.00","25,000","25,000","Active","Optional"]
              ]
            }
          },
          {
            "title": "Salary",
            "infoSrcId": "TAN002",
            "l2": {
              "columnLabel": ["Information Category","Information Code","Information Description","Information Source","Count","Amount","Information Category Code","Derived Amount","Qualifies For"],
              "columnData": [["Salary","TDS-192","Salary received (Section 192)","BETA INC (TAN002)","1","3,00,000.00","SAL",null,""]]
            },
            "l1": {
              "columnLabel": [{"field":"tsnId","name":"TSN"},{"field":"quarter","name":"Quarter"},{"field":"transactionDate","name":"Date of Payment/Credit"},{"field":"amtPaid","name":"Amount Paid/Credited"},{"field":"amountDeducted","name":"TDS Deducted"},{"field":"amountDeposited","name":"TDS Deposited"},{"field":"status","name":"Status"},{"field":"transFeedback","name":"Feedback"}],
              "columnData": [["3","Q3","31/12/2025","3,00,000.00","0","0","Active","Optional"]]
            }
          },
          {
            "title": "Dividend",
            "infoSrcId": "MUMC001",
            "l2": {
              "columnLabel": ["Information Category","Information Code","Information Description","Information Source","Count","Amount","Information Category Code","Derived Amount","Qualifies For"],
              "columnData": [["Dividend","TDS-194","Dividend (Section 194)","CASTROL INDIA LTD (MUMC001)","1","875.00","DIV",null,""]]
            },
            "l1": {
              "columnLabel": [{"field":"tsnId","name":"TSN"},{"field":"quarter","name":"Quarter"},{"field":"transactionDate","name":"Date of Payment/Credit"},{"field":"amtPaid","name":"Amount Paid/Credited"},{"field":"amountDeducted","name":"TDS Deducted"},{"field":"amountDeposited","name":"TDS Deposited"},{"field":"status","name":"Status"},{"field":"transFeedback","name":"Feedback"}],
              "columnData": [["4","Q4","30/03/2026","875.00","0","0","Active","Optional"]]
            }
          },
          {
            "title": "Interest from deposit",
            "infoSrcId": "DELU001",
            "l2": {
              "columnLabel": ["Information Category","Information Code","Information Description","Information Source","Count","Amount","Information Category Code","Derived Amount","Qualifies For"],
              "columnData": [["Interest from deposit","TDS-194A","Interest on FD (Section 194A)","UJJIVAN SFB (DELU001)","1","10,669.00","INT",null,""]]
            },
            "l1": {
              "columnLabel": [{"field":"tsnId","name":"TSN"},{"field":"quarter","name":"Quarter"},{"field":"transactionDate","name":"Date of Payment/Credit"},{"field":"amtPaid","name":"Amount Paid/Credited"},{"field":"amountDeducted","name":"TDS Deducted"},{"field":"amountDeposited","name":"TDS Deposited"},{"field":"status","name":"Status"},{"field":"transFeedback","name":"Feedback"}],
              "columnData": [["5","Q4","31/03/2026","10,669.00","0","0","Active","Optional"]]
            }
          }
        ]
      },
      {
        "sectionKey": "sft",
        "title": "SFT",
        "elements": [
          {
            "title": "Dividend",
            "infoSrcId": "AAAC001",
            "l2": {
              "columnLabel": ["Information Category","Information Code","Information Description","Information Source","Count","Amount","Information Category Code","Derived Amount","Qualifies For"],
              "columnData": [["Dividend","SFT-015","Dividend","CASTROL INDIA LTD (AAAC001)","1","875.00","DIV",null,""]]
            },
            "l1": {
              "columnLabel": [{"field":"tsnId","name":"TSN"},{"field":"reportedOn","name":"Reported On"},{"field":"amtPaid","name":"Dividend Amount"},{"field":"status","name":"Status"},{"field":"transFeedback","name":"Feedback"}],
              "columnData": [["6","29/05/2026","875.00","Active","Optional"]]
            }
          },
          {
            "title": "Interest from savings bank",
            "infoSrcId": "AAAC002",
            "l2": {
              "columnLabel": ["Information Category","Information Code","Information Description","Information Source","Count","Amount","Information Category Code","Derived Amount","Qualifies For"],
              "columnData": [["Interest from savings bank","SFT-016(SB)","Savings interest","HDFC BANK LTD (AAAC002)","1","24,772.00","INT",null,""]]
            },
            "l1": {
              "columnLabel": [{"field":"tsnId","name":"TSN"},{"field":"reportedOn","name":"Reported On"},{"field":"accountNo","name":"Account Number"},{"field":"accountType","name":"Account Type"},{"field":"amtPaid","name":"Interest amount"},{"field":"status","name":"Status"},{"field":"transFeedback","name":"Feedback"}],
              "columnData": [["7","07/05/2026","123456789","Saving","24,772.00","Active","Optional"]]
            }
          },
          {
            "title": "Interest from deposit",
            "infoSrcId": "AAAC002",
            "l2": {
              "columnLabel": ["Information Category","Information Code","Information Description","Information Source","Count","Amount","Information Category Code","Derived Amount","Qualifies For"],
              "columnData": [["Interest from deposit","SFT-016(TD)","FD interest","HDFC BANK LTD (AAAC002)","1","8,622.00","INT",null,""]]
            },
            "l1": {
              "columnLabel": [{"field":"tsnId","name":"TSN"},{"field":"reportedOn","name":"Reported On"},{"field":"accountNo","name":"Account Number"},{"field":"accountType","name":"Account Type"},{"field":"amtPaid","name":"Interest amount"},{"field":"status","name":"Status"},{"field":"transFeedback","name":"Feedback"}],
              "columnData": [["8","07/05/2026","123456789","Term Deposit","8,622.00","Active","Optional"]]
            }
          },
          {
            "title": "Sale of securities",
            "infoSrcId": "AAAC003",
            "l2": {
              "columnLabel": ["Information Category","Information Code","Information Description","Information Source","Count","Amount","Information Category Code","Derived Amount","Qualifies For"],
              "columnData": [["Sale of securities","SFT-17-LES(M)","Securities sell","CDSL (AAAC003)","2","73,264.00","SFT",null,""]]
            },
            "l1": {
              "columnLabel": [{"field":"tsnId","name":"TSN"},{"field":"transferDate","name":"Date of Sale/Transfer"},{"field":"securityName","name":"Security Name"},{"field":"securityClass","name":"Security Class"},{"field":"debitType","name":"Debit Type"},{"field":"creditType","name":"Credit Type"},{"field":"assetType","name":"Asset Type"},{"field":"quantity","name":"Quantity"},{"field":"sellPricePerUnit","name":"Sale Price Per unit"},{"field":"salesConsideration","name":"Sales Consideration"},{"field":"costOfAcquisition","name":"Cost of Acquisition"},{"field":"fmvValueUnit","name":"Unit FMV"},{"field":"fmvValue","name":"Fair Market Value"},{"field":"indexCostOfAcquisition","name":"Indexed Cost of Acquisition"},{"field":"status","name":"Status"},{"field":"transFeedback","name":"Feedback"}],
              "columnData": [
                ["9","08/10/2025","PRICOL LIMITED # EQUITY SHARES(INE726)","Listed Equity Share","Market","Market","Short term","27.00","509.70","13,761.90","15,226.65","106.50","2,875.50","0","Active","Optional"],
                ["10","23/09/2025","NETWEB TECHNOLOGIES(INE0NT)","Listed Equity Share","Market","Market","Short term","4.00","3,465.60","13,862.40","11,141.00","0","0","0","Active","Optional"]
              ]
            }
          }
        ]
      },
      {
        "sectionKey": "paymentOfTaxes",
        "title": "Payment of Taxes",
        "elements": [
          {
            "title": "",
            "columnLabel": ["Financial Year","Major Head","Minor Head","Tax (A)","Surcharge (B)","Education Cess (C)","Others (D)","Total (A+B+C+D)","BSR Code","Date Of Deposit","Challan Serial Number","Challan Identification Number"],
            "columnData": [["2024-25","Income Tax (Other than Companies)","Self Assessment","24,150","0","0","0","24,150","0510016","16/09/2025","62647","25091600669378HDFC"]]
          }
        ]
      }
    ]
  }
}`

func TestParse_FY(t *testing.T) {
	p, err := Parse([]byte(parseFixture))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.FY != "2025-26" {
		t.Errorf("FY = %q, want %q", p.FY, "2025-26")
	}
}

func TestParse_Salaries(t *testing.T) {
	p, _ := Parse([]byte(parseFixture))
	if len(p.Salaries) != 2 {
		t.Fatalf("Salaries: got %d entries, want 2", len(p.Salaries))
	}
	acme := p.Salaries[0]
	if acme.Employer != "ACME CORP PVT LTD" {
		t.Errorf("Salaries[0].Employer = %q, want %q", acme.Employer, "ACME CORP PVT LTD")
	}
	if acme.TAN != "TAN001" {
		t.Errorf("Salaries[0].TAN = %q, want %q", acme.TAN, "TAN001")
	}
	if acme.GrossSalary != 500000 {
		t.Errorf("Salaries[0].GrossSalary = %v, want 500000", acme.GrossSalary)
	}
	if acme.TDS != 50000 {
		t.Errorf("Salaries[0].TDS = %v, want 50000", acme.TDS)
	}
	beta := p.Salaries[1]
	if beta.TDS != 0 {
		t.Errorf("Salaries[1].TDS = %v, want 0", beta.TDS)
	}
}

func TestParse_TDSEntries(t *testing.T) {
	p, _ := Parse([]byte(parseFixture))
	if len(p.TDS) != 4 {
		t.Fatalf("TDS: got %d entries, want 4", len(p.TDS))
	}
	want192 := 0
	want194 := 0
	want194A := 0
	for _, e := range p.TDS {
		switch e.Section {
		case "192":
			want192++
		case "194":
			want194++
		case "194A":
			want194A++
		}
	}
	if want192 != 2 {
		t.Errorf("TDS section 192: got %d, want 2", want192)
	}
	if want194 != 1 {
		t.Errorf("TDS section 194: got %d, want 1", want194)
	}
	if want194A != 1 {
		t.Errorf("TDS section 194A: got %d, want 1", want194A)
	}
}

func TestParse_SavingsInterest(t *testing.T) {
	p, _ := Parse([]byte(parseFixture))
	if len(p.SavingsInterest) != 1 {
		t.Fatalf("SavingsInterest: got %d, want 1", len(p.SavingsInterest))
	}
	si := p.SavingsInterest[0]
	if si.Bank != "HDFC BANK LTD" {
		t.Errorf("SavingsInterest[0].Bank = %q, want %q", si.Bank, "HDFC BANK LTD")
	}
	if si.Amount != 24772 {
		t.Errorf("SavingsInterest[0].Amount = %v, want 24772", si.Amount)
	}
}

func TestParse_FDInterest(t *testing.T) {
	p, _ := Parse([]byte(parseFixture))
	if len(p.FDInterest) != 1 {
		t.Fatalf("FDInterest: got %d, want 1", len(p.FDInterest))
	}
	if p.FDInterest[0].Amount != 8622 {
		t.Errorf("FDInterest[0].Amount = %v, want 8622", p.FDInterest[0].Amount)
	}
}

func TestParse_Dividends(t *testing.T) {
	p, _ := Parse([]byte(parseFixture))
	if len(p.Dividends) != 1 {
		t.Fatalf("Dividends: got %d, want 1", len(p.Dividends))
	}
	if p.Dividends[0].Amount != 875 {
		t.Errorf("Dividends[0].Amount = %v, want 875", p.Dividends[0].Amount)
	}
}

func TestParse_Securities(t *testing.T) {
	p, _ := Parse([]byte(parseFixture))
	if len(p.Securities) != 2 {
		t.Fatalf("Securities: got %d, want 2", len(p.Securities))
	}
	s0 := p.Securities[0]
	if s0.SecurityName != "PRICOL LIMITED # EQUITY SHARES(INE726)" {
		t.Errorf("Securities[0].SecurityName = %q", s0.SecurityName)
	}
	if s0.SalesConsideration != 13761.90 {
		t.Errorf("Securities[0].SalesConsideration = %v, want 13761.90", s0.SalesConsideration)
	}
	if s0.CostOfAcquisition != 15226.65 {
		t.Errorf("Securities[0].CostOfAcquisition = %v, want 15226.65", s0.CostOfAcquisition)
	}
	if s0.Type != "Short term" {
		t.Errorf("Securities[0].Type = %q, want %q", s0.Type, "Short term")
	}
}

func TestParse_AdvanceTax(t *testing.T) {
	p, _ := Parse([]byte(parseFixture))
	if len(p.AdvanceTax) != 1 {
		t.Fatalf("AdvanceTax: got %d, want 1", len(p.AdvanceTax))
	}
	at := p.AdvanceTax[0]
	if at.FY != "2024-25" {
		t.Errorf("AdvanceTax[0].FY = %q, want %q", at.FY, "2024-25")
	}
	if at.Tax != 24150 {
		t.Errorf("AdvanceTax[0].Tax = %v, want 24150", at.Tax)
	}
	if at.Total != 24150 {
		t.Errorf("AdvanceTax[0].Total = %v, want 24150", at.Total)
	}
	if at.BSRCode != "0510016" {
		t.Errorf("AdvanceTax[0].BSRCode = %q, want %q", at.BSRCode, "0510016")
	}
	if at.Date != "16/09/2025" {
		t.Errorf("AdvanceTax[0].Date = %q, want %q", at.Date, "16/09/2025")
	}
}

func TestParse_Garbage(t *testing.T) {
	if _, err := Parse([]byte("not json")); err == nil {
		t.Fatal("Parse garbage: want error, got nil")
	}
}

func TestParse_EmptyJSON(t *testing.T) {
	p, err := Parse([]byte(`{}`))
	if err != nil {
		t.Fatalf("Parse empty: %v", err)
	}
	if p.FY != "" {
		t.Errorf("FY = %q, want empty", p.FY)
	}
	if len(p.Salaries) != 0 {
		t.Errorf("Salaries: got %d, want 0", len(p.Salaries))
	}
}

func TestFYStartYear(t *testing.T) {
	if y := FYStartYear("2025-26"); y != 2025 {
		t.Errorf("FYStartYear(\"2025-26\") = %d, want 2025", y)
	}
	if y := FYStartYear("2024-25"); y != 2024 {
		t.Errorf("FYStartYear(\"2024-25\") = %d, want 2024", y)
	}
}
