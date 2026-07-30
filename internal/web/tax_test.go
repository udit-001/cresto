package web

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

const taxAISFixture = `{
  "header": {"columnData": ["2025-26"]},
  "partB": {
    "sections": [
      {
        "sectionKey": "tdsTcs",
        "elements": [
          {
            "title": "Salary",
            "infoSrcId": "TAN001",
            "l2": {
              "columnLabel": ["Information Category","Information Code","Information Description","Information Source","Count","Amount","Information Category Code","Derived Amount","Qualifies For"],
              "columnData": [["Salary","TDS-192","Salary received (Section 192)","TEST CORP PVT LTD (TAN001)","2","5,00,000.00","SAL",null,""]]
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
            "title": "Interest from deposit",
            "infoSrcId": "DELU001",
            "l2": {
              "columnLabel": ["Information Category","Information Code","Information Description","Information Source","Count","Amount","Information Category Code","Derived Amount","Qualifies For"],
              "columnData": [["Interest from deposit","TDS-194A","Interest on FD (Section 194A)","UJJIVAN SFB (DELU001)","1","10,669.00","INT",null,""]]
            },
            "l1": {
              "columnLabel": [{"field":"tsnId","name":"TSN"},{"field":"quarter","name":"Quarter"},{"field":"transactionDate","name":"Date of Payment/Credit"},{"field":"amtPaid","name":"Amount Paid/Credited"},{"field":"amountDeducted","name":"TDS Deducted"},{"field":"amountDeposited","name":"TDS Deposited"},{"field":"status","name":"Status"},{"field":"transFeedback","name":"Feedback"}],
              "columnData": [["3","Q4","31/03/2026","10,669.00","0","0","Active","Optional"]]
            }
          }
        ]
      },
      {
        "sectionKey": "sft",
        "elements": [
          {
            "title": "Interest from savings bank",
            "infoSrcId": "AAAC002",
            "l2": {
              "columnLabel": ["Information Category","Information Code","Information Description","Information Source","Count","Amount","Information Category Code","Derived Amount","Qualifies For"],
              "columnData": [["Interest from savings bank","SFT-016(SB)","Savings interest","HDFC BANK LTD (AAAC002)","1","24,772.00","INT",null,""]]
            },
            "l1": {
              "columnLabel": [{"field":"tsnId","name":"TSN"},{"field":"reportedOn","name":"Reported On"},{"field":"amtPaid","name":"Interest amount"},{"field":"status","name":"Status"},{"field":"transFeedback","name":"Feedback"}],
              "columnData": [["4","07/05/2026","24,772.00","Active","Optional"]]
            }
          },
          {
            "title": "Dividend",
            "infoSrcId": "AAAC001",
            "l2": {
              "columnLabel": ["Information Category","Information Code","Information Description","Information Source","Count","Amount","Information Category Code","Derived Amount","Qualifies For"],
              "columnData": [["Dividend","SFT-015","Dividend","CASTROL INDIA LTD (AAAC001)","1","875.00","DIV",null,""]]
            },
            "l1": {
              "columnLabel": [{"field":"tsnId","name":"TSN"},{"field":"reportedOn","name":"Reported On"},{"field":"amtPaid","name":"Dividend Amount"},{"field":"status","name":"Status"},{"field":"transFeedback","name":"Feedback"}],
              "columnData": [["5","29/05/2026","875.00","Active","Optional"]]
            }
          }
        ]
      },
      {
        "sectionKey": "paymentOfTaxes",
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

func TestTax_EmptyState_ShowsWizard(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	rec, _ := doGet(srv, "/tax")
	if rec.Code != 200 {
		t.Fatalf("GET /tax: status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Download your AIS JSON") {
		t.Error("empty /tax should show import wizard")
	}
	if !strings.Contains(body, `name="ais"`) {
		t.Error("empty /tax should have AIS upload form")
	}
	if !strings.Contains(body, "incometax.gov.in") {
		t.Error("empty /tax should link to AIS download instructions")
	}
}

func TestTax_EmptyState_NoProfile_ShowsWarning(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	rec, _ := doGet(srv, "/tax")
	body := rec.Body.String()
	if !strings.Contains(body, "Set up your Tax Profile first") {
		t.Error("empty /tax without profile should show warning")
	}
}

func TestTax_EmptyState_WithProfile_NoWarning(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	doPostForm(srv, "/settings/tax-profile",
		"pan=ABCDE1234F&dob=15061990&declarant_name=Test+User&verification_place=Bangalore")

	rec, _ := doGet(srv, "/tax")
	body := rec.Body.String()
	if strings.Contains(body, "Set up your Tax Profile first") {
		t.Error("/tax with profile should NOT show the warning")
	}
}

func TestTax_Upload_NoProfile_ReturnsError(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	rec := uploadAIS(srv, []byte(taxAISFixture))
	if rec.Code != 400 {
		t.Errorf("upload without profile: status = %d, want 400", rec.Code)
	}
}

func TestTax_Upload_ThenDisplay(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	doPostForm(srv, "/settings/tax-profile",
		"pan=ABCDE1234F&dob=15061990&declarant_name=Test+User&verification_place=Bangalore")

	rec := uploadAIS(srv, []byte(taxAISFixture))
	if rec.Code != 303 {
		t.Fatalf("upload AIS: status = %d, want 303, body: %s", rec.Code, rec.Body.String())
	}

	rec, _ = doGet(srv, "/tax")
	if rec.Code != 200 {
		t.Fatalf("GET /tax after upload: status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "FY 2025-26") {
		t.Error("/tax should show FY 2025-26")
	}
	if !strings.Contains(body, "TEST CORP PVT LTD") {
		t.Error("/tax should show employer name from AIS")
	}
	if !strings.Contains(body, "500,000") {
		t.Error("/tax should show gross salary 500,000")
	}
	if !strings.Contains(body, "HDFC BANK LTD") {
		t.Error("/tax should show savings interest bank")
	}
	if !strings.Contains(body, "24,772") {
		t.Error("/tax should show savings interest amount")
	}
	if !strings.Contains(body, "CASTROL INDIA LTD") {
		t.Error("/tax should show dividend company")
	}
	if !strings.Contains(body, "Self Assessment") {
		t.Error("/tax should show advance tax minor head")
	}
	if !strings.Contains(body, "TDS Reconciliation") {
		t.Error("/tax should show TDS reconciliation table")
	}
}

func TestTax_Upload_TDSReconciliation_WithPayslips(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	doPostForm(srv, "/settings/tax-profile",
		"pan=ABCDE1234F&dob=15061990&declarant_name=Test+User&verification_place=Bangalore")

	p := seedPayslip(t, srv)
	p.EmployerName = "Test Corp Pvt Ltd"
	p.PayPeriodMonth = 5
	p.PayPeriodYear = 2025
	p.GrossSalary = 500000
	p.Components[1].Amount = -50000
	if err := srv.store.SavePayslip(context.Background(), &p); err != nil {
		t.Fatalf("SavePayslip: %v", err)
	}
	if err := srv.store.ConfirmPayslip(context.Background(), p.ID); err != nil {
		t.Fatalf("ConfirmPayslip: %v", err)
	}

	uploadAIS(srv, []byte(taxAISFixture))

	rec, _ := doGet(srv, "/tax")
	body := rec.Body.String()
	if !strings.Contains(body, "Match") {
		t.Errorf("/tax TDS recon should show 'Match' when AIS TDS equals Cresto TDS\nbody snippet: %s", tdsReconSnippet(body))
	}
}

func TestTax_Upload_TDSReconciliation_Gap(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	doPostForm(srv, "/settings/tax-profile",
		"pan=ABCDE1234F&dob=15061990&declarant_name=Test+User&verification_place=Bangalore")

	p := seedPayslip(t, srv)
	p.EmployerName = "Test Corp Pvt Ltd"
	p.PayPeriodMonth = 5
	p.PayPeriodYear = 2025
	p.GrossSalary = 200000
	p.Components[1].Amount = -25000
	if err := srv.store.SavePayslip(context.Background(), &p); err != nil {
		t.Fatalf("SavePayslip: %v", err)
	}
	if err := srv.store.ConfirmPayslip(context.Background(), p.ID); err != nil {
		t.Fatalf("ConfirmPayslip: %v", err)
	}

	uploadAIS(srv, []byte(taxAISFixture))

	rec, _ := doGet(srv, "/tax")
	body := rec.Body.String()
	if !strings.Contains(body, "Gap") {
		t.Errorf("/tax TDS recon should show 'Gap' when AIS TDS != Cresto TDS\nbody snippet: %s", tdsReconSnippet(body))
	}
}

func TestTax_ComputationCard_ShowsBreakdown(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	doPostForm(srv, "/settings/tax-profile",
		"pan=ABCDE1234F&dob=15061990&declarant_name=Test+User&verification_place=Bangalore")
	uploadAIS(srv, []byte(taxAISFixture))

	rec, _ := doGet(srv, "/tax")
	body := rec.Body.String()
	if !strings.Contains(body, "Tax Liability") {
		t.Error("/tax should show Tax Liability card")
	}
	if !strings.Contains(body, "Standard Deduction") {
		t.Error("/tax should show standard deduction line")
	}
	if !strings.Contains(body, "Tax Computation") {
		t.Error("/tax should show Tax Computation card")
	}
	if !strings.Contains(body, "Total Tax Liability") {
		t.Error("/tax should show total tax liability")
	}
}

func TestTax_RefundDues_ShowsBalanceDue(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	doPostForm(srv, "/settings/tax-profile",
		"pan=ABCDE1234F&dob=15061990&declarant_name=Test+User&verification_place=Bangalore")
	uploadAIS(srv, []byte(taxAISFixture))

	rec, _ := doGet(srv, "/tax")
	body := rec.Body.String()
	if !strings.Contains(body, "Balance Due") && !strings.Contains(body, "Refund") {
		t.Error("/tax should show refund or balance due section")
	}
	if !strings.Contains(body, "TDS Paid") {
		t.Error("/tax should show TDS paid amount")
	}
}

func uploadAIS(srv *Server, jsonData []byte) *httptest.ResponseRecorder {
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	part, _ := w.CreateFormFile("ais", "ais.json")
	part.Write(jsonData)
	w.Close()

	req := httptest.NewRequest(http.MethodPost, "/tax/ais-upload", body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	return rec
}

func tdsReconSnippet(body string) string {
	idx := strings.Index(body, "TDS Reconciliation")
	if idx < 0 {
		return "(TDS Reconciliation section not found)"
	}
	end := idx + 800
	if end > len(body) {
		end = len(body)
	}
	return body[idx:end]
}

func buildKiteFixtureXLSX(t *testing.T) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	orig := f.GetSheetName(0)
	f.SetSheetName(orig, "Tradewise Exits from 2025-04-01")
	sheetName := "Tradewise Exits from 2025-04-01"
	f.SetCellValue(sheetName, "A1", " ")

	set := func(row, col int, val string) {
		axis, _ := excelize.CoordinatesToCellName(col, row)
		f.SetCellValue(sheetName, axis, val)
	}

	set(24, 2, "Equity - Short Term")
	headers := []string{"", "Symbol", "ISIN", "Entry Date", "Exit Date", "Quantity",
		"Buy Value", "Sell Value", "Profit", "Period of Holding",
		"Fair Market Value", "Taxable Profit", "Turnover", "Brokerage",
		"Exchange Transaction Charges", "IPFT", "SEBI Charges",
		"CGST", "SGST", "IGST", "Stamp Duty", "STT"}
	for i, h := range headers {
		set(26, i+1, h)
	}
	set(27, 2, "TESTSTOCK")
	set(27, 3, "INE999")
	set(27, 4, "2024-11-06")
	set(27, 5, "2025-09-22")
	set(27, 6, "1")
	set(27, 7, "1000")
	set(27, 8, "1500")
	set(27, 9, "500")
	set(27, 10, "321 days")
	set(27, 11, "0")
	set(27, 12, "500")
	for i := 13; i <= 20; i++ {
		set(27, i, "0")
	}
	set(27, 22, "1.50")

	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("f.Write: %v", err)
	}
	return buf.Bytes()
}

func uploadKite(srv *Server, xlsxData []byte) *httptest.ResponseRecorder {
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	part, _ := w.CreateFormFile("kite", "taxpnl.xlsx")
	part.Write(xlsxData)
	w.Close()

	req := httptest.NewRequest(http.MethodPost, "/tax/kite-upload", body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	return rec
}

func TestTax_KiteUpload_NoAIS_ReturnsError(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	rec := uploadKite(srv, buildKiteFixtureXLSX(t))
	if rec.Code != 400 {
		t.Errorf("kite upload without AIS: status = %d, want 400", rec.Code)
	}
}

func TestTax_KiteUpload_ThenDisplay(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	doPostForm(srv, "/settings/tax-profile",
		"pan=ABCDE1234F&dob=15061990&declarant_name=Test+User&verification_place=Bangalore")
	uploadAIS(srv, []byte(taxAISFixture))

	rec := uploadKite(srv, buildKiteFixtureXLSX(t))
	if rec.Code != 303 {
		t.Fatalf("kite upload: status = %d, want 303, body: %s", rec.Code, rec.Body.String())
	}

	rec, _ = doGet(srv, "/tax")
	body := rec.Body.String()
	if !strings.Contains(body, "Capital Gains") {
		t.Error("/tax should show Capital Gains card after Kite upload")
	}
	if !strings.Contains(body, "TESTSTOCK") {
		t.Error("/tax should show trade symbol TESTSTOCK")
	}
	if !strings.Contains(body, "1,500") {
		t.Error("/tax should show sell value 1,500")
	}
	if !strings.Contains(body, "STCG @ 20%") {
		t.Error("/tax should show STCG type label")
	}
	if !strings.Contains(body, "Kite Console imported") {
		t.Error("/tax should show Kite Console imported status")
	}
}

func TestTax_KiteUpload_ComputationIncludesCG(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	doPostForm(srv, "/settings/tax-profile",
		"pan=ABCDE1234F&dob=15061990&declarant_name=Test+User&verification_place=Bangalore")
	uploadAIS(srv, []byte(taxAISFixture))
	uploadKite(srv, buildKiteFixtureXLSX(t))

	rec, _ := doGet(srv, "/tax")
	body := rec.Body.String()
	if !strings.Contains(body, "special rates") {
		t.Error("/tax computation should show special-rate tax line (STCG)")
	}
}
