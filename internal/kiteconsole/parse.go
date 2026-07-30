package kiteconsole

import (
	"fmt"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
)

// Trade is one FIFO-matched exit from Kite Console's Tax P&L Sheet 0.
// Zerodha pre-computes the FIFO matching, holding period classification,
// and grandfathering — Cresto just stores and displays.
type Trade struct {
	Section       string
	Symbol        string
	ISIN          string
	EntryDate     string
	ExitDate      string
	Quantity      float64
	BuyValue      float64
	SellValue     float64
	Profit        float64
	TaxableProfit float64
	FMV           float64
	STT           float64
}

// IsSTCG returns true for short-term equity/MF trades (section 111A, 20%).
func (t Trade) IsSTCG() bool {
	return strings.Contains(t.Section, "Short Term")
}

// IsLTCG returns true for long-term equity/MF trades (section 112A, 12.5%).
func (t Trade) IsLTCG() bool {
	return strings.Contains(t.Section, "Long Term")
}

// Summary is the aggregated capital gains from all trades.
type Summary struct {
	Trades    []Trade
	TotalSTCG float64
	TotalLTCG float64
	TotalBuy  float64
	TotalSell float64
	TotalSTT  float64
	STCGCount int
	LTCGCount int
}

// Parse reads a Kite Console Tax P&L XLSX file and extracts trades from
// Sheet 0 ("Tradewise Exits"). Section headers (col B) partition the sheet
// into Equity Intraday / Short Term / Long Term / Buyback / Mutual Funds /
// F&O / Currency / Commodity. Only Short Term and Long Term sections are
// captured as capital gains — intraday (speculative) and F&O (business
// income) are skipped.
func Parse(data []byte) (Summary, error) {
	f, err := excelize.OpenReader(bytesReader(data))
	if err != nil {
		return Summary{}, fmt.Errorf("kiteconsole: open xlsx: %w", err)
	}
	defer f.Close()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return Summary{}, fmt.Errorf("kiteconsole: no sheets in workbook")
	}

	rows, err := f.GetRows(sheets[0])
	if err != nil {
		return Summary{}, fmt.Errorf("kiteconsole: read sheet 0: %w", err)
	}

	return parseRows(rows), nil
}

func parseRows(rows [][]string) Summary {
	var s Summary
	currentSection := ""

	for _, row := range rows {
		if len(row) < 2 {
			continue
		}

		b := strings.TrimSpace(row[1])
		c := ""
		if len(row) > 2 {
			c = strings.TrimSpace(row[2])
		}

		if b == "" {
			continue
		}

		if isSectionHeader(b, c) {
			currentSection = b
			continue
		}

		if b == "Symbol" && c == "ISIN" {
			continue
		}

		if currentSection == "" {
			continue
		}

		if !isCGSection(currentSection) {
			continue
		}

		if c == "" || c == "ISIN" {
			continue
		}

		t, ok := parseTradeRow(row, currentSection)
		if !ok {
			continue
		}

		s.Trades = append(s.Trades, t)
		if t.IsSTCG() {
			s.TotalSTCG += t.TaxableProfit
			s.STCGCount++
		} else if t.IsLTCG() {
			s.TotalLTCG += t.TaxableProfit
			s.LTCGCount++
		}
		s.TotalBuy += t.BuyValue
		s.TotalSell += t.SellValue
		s.TotalSTT += t.STT
	}

	return s
}

func parseTradeRow(row []string, section string) (Trade, bool) {
	if len(row) < 12 {
		return Trade{}, false
	}

	symbol := strings.TrimSpace(row[1])
	isin := strings.TrimSpace(row[2])
	if symbol == "" || isin == "" {
		return Trade{}, false
	}

	t := Trade{
		Section:   section,
		Symbol:    symbol,
		ISIN:      isin,
		EntryDate: strings.TrimSpace(row[3]),
		ExitDate:  strings.TrimSpace(row[4]),
	}

	t.Quantity = parseNum(row[5])
	t.BuyValue = parseNum(row[6])
	t.SellValue = parseNum(row[7])
	t.Profit = parseNum(row[8])
	t.FMV = parseNum(row[10])
	t.TaxableProfit = parseNum(row[11])

	if len(row) > 21 {
		t.STT = parseNum(row[21])
	}

	return t, true
}

func isSectionHeader(b, c string) bool {
	if c != "" {
		return false
	}
	sections := []string{"Equity - Intraday", "Equity - Short Term", "Equity - Long Term",
		"Equity - Buyback", "Mutual Funds", "F&O", "Currency", "Commodity"}
	for _, s := range sections {
		if b == s || strings.HasPrefix(b, s) {
			return true
		}
	}
	return false
}

func isCGSection(section string) bool {
	return strings.Contains(section, "Short Term") || strings.Contains(section, "Long Term")
}

func parseNum(s string) float64 {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, ",", "")
	var v float64
	fmt.Sscanf(s, "%f", &v)
	return v
}

func bytesReader(data []byte) *strings.Reader {
	return strings.NewReader(string(data))
}

// FYStartYearFromRange extracts the FY start year from the sheet title
// "Tradewise Exits from 2025-04-01 to 2026-03-31" → 2025.
func FYStartYearFromRange(title string) int {
	if idx := strings.Index(title, "from "); idx >= 0 {
		rest := title[idx+5:]
		if len(rest) >= 4 {
			var year int
			fmt.Sscanf(rest[:4], "%d", &year)
			return year
		}
	}
	return 0
}

// FormatDate normalizes an excelize date cell to YYYY-MM-DD. excelize returns
// dates as floats; this converts via the Excel epoch if needed.
func FormatDate(cell string) string {
	cell = strings.TrimSpace(cell)
	if cell == "" {
		return ""
	}
	if len(cell) >= 10 && cell[4] == '-' {
		return cell[:10]
	}
	if _, err := time.Parse("2006-01-02 15:04:05", cell); err == nil {
		t, _ := time.Parse("2006-01-02 15:04:05", cell)
		return t.Format("2006-01-02")
	}
	return cell
}
