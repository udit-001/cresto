package kiteconsole

import (
	"bytes"
	"math"
	"os"
	"testing"

	"github.com/xuri/excelize/v2"
)

func approxEqual(a, b float64) bool {
	return math.Abs(a-b) < 0.01
}

func buildFixtureXLSXDirect(t *testing.T) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()

	originalSheet := f.GetSheetName(0)
	f.SetSheetName(originalSheet, "Tradewise Exits from 2025-04-01")
	sheetName := "Tradewise Exits from 2025-04-01"
	f.SetCellValue(sheetName, "A1", " ")

	set := func(row, col int, val interface{}) {
		axis, _ := excelize.CoordinatesToCellName(col, row)
		f.SetCellValue(sheetName, axis, val)
	}

	set(7, 2, "Client ID")
	set(7, 3, "TEST001")
	set(9, 2, "PAN")
	set(9, 3, "ABCDE1234F")
	set(11, 2, "Tradewise Exits from 2025-04-01 to 2026-03-31")

	headers := []string{"", "Symbol", "ISIN", "Entry Date", "Exit Date", "Quantity",
		"Buy Value", "Sell Value", "Profit", "Period of Holding",
		"Fair Market Value", "Taxable Profit", "Turnover", "Brokerage",
		"Exchange Transaction Charges", "IPFT", "SEBI Charges",
		"CGST", "SGST", "IGST", "Stamp Duty", "STT"}

	set(18, 2, "Equity - Intraday")
	for i, h := range headers {
		set(20, i+1, h)
	}

	set(24, 2, "Equity - Short Term")
	for i, h := range headers {
		set(26, i+1, h)
	}
	stcgTrades := [][]string{
		{"NETWEB", "INE0NT901020", "2024-11-06", "2025-09-22", "1", "2785.25", "3465.60", "680.35", "321 days", "0", "680.35", "3465.60", "0", "0", "0", "0", "0", "0", "0", "0", "3.46"},
		{"SJS", "INE284S01014", "2024-12-02", "2025-08-20", "12", "15453.00", "16284.00", "831.00", "261 days", "0", "831.00", "16284.00", "0", "0", "0", "0", "0", "0", "0", "0", "16.39"},
		{"PRICOLLTD", "INE726V01018", "2024-12-18", "2025-10-07", "27", "15226.65", "13761.90", "-1464.75", "293 days", "0", "-1464.75", "13761.90", "0", "0", "0", "0", "0", "0", "0", "0", "13.83"},
	}
	for i, tr := range stcgTrades {
		for j, val := range tr {
			set(27+i, j+2, val)
		}
	}

	set(38, 2, "Equity - Long Term")
	for i, h := range headers {
		set(40, i+1, h)
	}
	ltcgTrades := [][]string{
		{"RELIANCE", "INE002A01018", "2023-01-15", "2025-12-10", "10", "20000", "25000", "5000", "695 days", "22000", "3000", "25000", "0", "0", "0", "0", "0", "0", "0", "0", "25.00"},
	}
	for i, tr := range ltcgTrades {
		for j, val := range tr {
			set(41+i, j+2, val)
		}
	}

	set(44, 2, "Equity - Buyback")
	set(50, 2, "Mutual Funds")
	set(56, 2, "F&O")

	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("f.Write: %v", err)
	}
	return buf.Bytes()
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

func TestParse_STCGTrades(t *testing.T) {
	data := buildFixtureXLSXDirect(t)
	s, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if s.STCGCount != 3 {
		t.Errorf("STCG count = %d, want 3", s.STCGCount)
	}
	if s.LTCGCount != 1 {
		t.Errorf("LTCG count = %d, want 1", s.LTCGCount)
	}
}

func TestParse_Totals(t *testing.T) {
	data := buildFixtureXLSXDirect(t)
	s, _ := Parse(data)
	wantSTCG := 680.35 + 831.00 + (-1464.75)
	if !approxEqual(s.TotalSTCG, wantSTCG) {
		t.Errorf("TotalSTCG = %.2f, want %.2f", s.TotalSTCG, wantSTCG)
	}

	wantLTCG := 3000.0
	if !approxEqual(s.TotalLTCG, wantLTCG) {
		t.Errorf("TotalLTCG = %.2f, want %.2f", s.TotalLTCG, wantLTCG)
	}

	wantSell := 3465.60 + 16284.00 + 13761.90 + 25000
	if !approxEqual(s.TotalSell, wantSell) {
		t.Errorf("TotalSell = %.2f, want %.2f", s.TotalSell, wantSell)
	}
}

func TestParse_TradeFields(t *testing.T) {
	data := buildFixtureXLSXDirect(t)
	s, _ := Parse(data)
	if len(s.Trades) != 4 {
		t.Fatalf("Trades: got %d, want 4", len(s.Trades))
	}
	first := s.Trades[0]
	if first.Symbol != "NETWEB" {
		t.Errorf("Trades[0].Symbol = %q, want %q", first.Symbol, "NETWEB")
	}
	if first.ISIN != "INE0NT901020" {
		t.Errorf("Trades[0].ISIN = %q, want %q", first.ISIN, "INE0NT901020")
	}
	if first.Quantity != 1 {
		t.Errorf("Trades[0].Quantity = %v, want 1", first.Quantity)
	}
	if first.BuyValue != 2785.25 {
		t.Errorf("Trades[0].BuyValue = %v, want 2785.25", first.BuyValue)
	}
	if first.STT != 3.46 {
		t.Errorf("Trades[0].STT = %v, want 3.46", first.STT)
	}
	if !first.IsSTCG() {
		t.Error("Trades[0] should be STCG")
	}

	ltcg := s.Trades[3]
	if ltcg.Symbol != "RELIANCE" {
		t.Errorf("Trades[3].Symbol = %q, want %q", ltcg.Symbol, "RELIANCE")
	}
	if !ltcg.IsLTCG() {
		t.Error("Trades[3] should be LTCG")
	}
	if ltcg.FMV != 22000 {
		t.Errorf("Trades[3].FMV = %v, want 22000", ltcg.FMV)
	}
}

func TestParse_SkipsIntradayAndFO(t *testing.T) {
	data := buildFixtureXLSXDirect(t)
	s, _ := Parse(data)
	for _, tr := range s.Trades {
		if tr.Section == "Equity - Intraday" {
			t.Error("intraday trades should be skipped")
		}
		if tr.Section == "F&O" {
			t.Error("F&O trades should be skipped")
		}
	}
}

func TestParse_EmptyXLSX(t *testing.T) {
	f := excelize.NewFile()
	defer f.Close()
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("f.Write: %v", err)
	}
	s, err := Parse(buf.Bytes())
	if err != nil {
		t.Fatalf("Parse empty: %v", err)
	}
	if len(s.Trades) != 0 {
		t.Errorf("empty XLSX: got %d trades, want 0", len(s.Trades))
	}
}

func TestParse_Garbage(t *testing.T) {
	if _, err := Parse([]byte("not an xlsx")); err == nil {
		t.Fatal("Parse garbage: want error, got nil")
	}
}

func TestFYStartYearFromRange(t *testing.T) {
	if y := FYStartYearFromRange("Tradewise Exits from 2025-04-01 to 2026-03-31"); y != 2025 {
		t.Errorf("FYStartYear = %d, want 2025", y)
	}
	if y := FYStartYearFromRange("no date here"); y != 0 {
		t.Errorf("FYStartYear = %d, want 0 for no date", y)
	}
}
