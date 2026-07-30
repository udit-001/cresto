package ais

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// ParsedAIS is the structured representation of a decrypted AIS JSON.
type ParsedAIS struct {
	FY              string
	Salaries        []SalaryEntry
	SavingsInterest []InterestEntry
	FDInterest      []InterestEntry
	Dividends       []DividendEntry
	TDS             []TDSEntry
	Securities      []SecuritySale
	AdvanceTax      []AdvanceTaxEntry
}

type SalaryEntry struct {
	Employer    string
	TAN         string
	GrossSalary float64
	TDS         float64
}

type InterestEntry struct {
	Bank   string
	Code   string
	Amount float64
}

type DividendEntry struct {
	Company string
	Code    string
	Amount  float64
	TDS     float64
}

type TDSEntry struct {
	Deductor string
	TAN      string
	Section  string
	Income   float64
	TDS      float64
}

type SecuritySale struct {
	SecurityName       string
	SalesConsideration float64
	CostOfAcquisition  float64
	Type               string
}

type AdvanceTaxEntry struct {
	FY        string
	MajorHead string
	MinorHead string
	Tax       float64
	Surcharge float64
	Cess      float64
	Total     float64
	BSRCode   string
	Date      string
	Challan   string
	CIN       string
}

// FYStartYear converts an AIS FY string like "2025-26" to the start year 2025.
func FYStartYear(fy string) int {
	parts := strings.SplitN(fy, "-", 2)
	y, _ := strconv.Atoi(parts[0])
	return y
}

// Parse converts a decrypted AIS JSON into structured domain types.
func Parse(data []byte) (ParsedAIS, error) {
	var raw aisDocument
	if err := json.Unmarshal(data, &raw); err != nil {
		return ParsedAIS{}, fmt.Errorf("ais: unmarshal: %w", err)
	}

	var p ParsedAIS
	if len(raw.Header.ColumnData) > 0 {
		p.FY = raw.Header.ColumnData[0]
	}

	for _, sec := range raw.PartB.Sections {
		switch sec.SectionKey {
		case "tdsTcs":
			for _, elem := range sec.Elements {
				parseTDSElement(&p, elem)
			}
		case "sft":
			for _, elem := range sec.Elements {
				parseSFTElement(&p, elem)
			}
		case "paymentOfTaxes":
			for _, elem := range sec.Elements {
				parsePaymentOfTaxes(&p, elem)
			}
		}
	}

	return p, nil
}

func parseTDSElement(p *ParsedAIS, elem *aisElement) {
	infoCode := l2String(elem.L2, 1)
	source := l2String(elem.L2, 3)
	amount := l2Amount(elem.L2, 5)

	name, tan := splitSource(source)
	if tan == "" {
		tan = elem.InfoSrcID
	}

	tds := sumL1Field(elem.L1, "amountDeducted")

	entry := TDSEntry{
		Deductor: name,
		TAN:      tan,
		Section:  stripTDSPrefix(infoCode),
		Income:   amount,
		TDS:      tds,
	}
	p.TDS = append(p.TDS, entry)

	switch infoCode {
	case "TDS-192":
		p.Salaries = append(p.Salaries, SalaryEntry{
			Employer:    name,
			TAN:         tan,
			GrossSalary: amount,
			TDS:         tds,
		})
	case "TDS-194":
		for i := range p.Dividends {
			if p.Dividends[i].Code == tan || strings.Contains(strings.ToUpper(p.Dividends[i].Company), strings.ToUpper(name)) {
				p.Dividends[i].TDS = tds
			}
		}
	}
}

func parseSFTElement(p *ParsedAIS, elem *aisElement) {
	infoCode := l2String(elem.L2, 1)
	source := l2String(elem.L2, 3)
	amount := l2Amount(elem.L2, 5)

	name, code := splitSource(source)
	if code == "" {
		code = elem.InfoSrcID
	}

	switch {
	case infoCode == "SFT-015" || infoCode == "SFT-17-LES(D)":
		p.Dividends = append(p.Dividends, DividendEntry{
			Company: name,
			Code:    code,
			Amount:  amount,
		})
	case infoCode == "SFT-016(SB)":
		p.SavingsInterest = append(p.SavingsInterest, InterestEntry{
			Bank:   name,
			Code:   code,
			Amount: amount,
		})
	case infoCode == "SFT-016(TD)":
		p.FDInterest = append(p.FDInterest, InterestEntry{
			Bank:   name,
			Code:   code,
			Amount: amount,
		})
	case infoCode == "SFT-17-LES(M)":
		if elem.L1 != nil {
			idx := l1FieldIndex(elem.L1.ColumnLabel)
			for _, row := range elem.L1.ColumnData {
				p.Securities = append(p.Securities, SecuritySale{
					SecurityName:       rowString(row, idx["securityName"]),
					SalesConsideration: rowAmount(row, idx["salesConsideration"]),
					CostOfAcquisition:  rowAmount(row, idx["costOfAcquisition"]),
					Type:               rowString(row, idx["assetType"]),
				})
			}
		}
	}
}

func parsePaymentOfTaxes(p *ParsedAIS, elem *aisElement) {
	idx := stringIndex(elem.ColumnLabel)
	for _, row := range elem.ColumnData {
		p.AdvanceTax = append(p.AdvanceTax, AdvanceTaxEntry{
			FY:        rowString(row, idx["Financial Year"]),
			MajorHead: rowString(row, idx["Major Head"]),
			MinorHead: rowString(row, idx["Minor Head"]),
			Tax:       rowAmount(row, idx["Tax (A)"]),
			Surcharge: rowAmount(row, idx["Surcharge (B)"]),
			Cess:      rowAmount(row, idx["Education Cess (C)"]),
			Total:     rowAmount(row, idx["Total (A+B+C+D)"]),
			BSRCode:   rowString(row, idx["BSR Code"]),
			Date:      rowString(row, idx["Date Of Deposit"]),
			Challan:   rowString(row, idx["Challan Serial Number"]),
			CIN:       rowString(row, idx["Challan Identification Number"]),
		})
	}
}

// --- raw JSON types ---

type aisDocument struct {
	Header struct {
		ColumnData []string `json:"columnData"`
	} `json:"header"`
	PartB struct {
		Sections []aisSection `json:"sections"`
	} `json:"partB"`
}

type aisSection struct {
	SectionKey string        `json:"sectionKey"`
	Elements   []*aisElement `json:"elements"`
}

type aisElement struct {
	Title       string      `json:"title"`
	InfoSrcID   string      `json:"infoSrcId"`
	L2          *aisL2      `json:"l2"`
	L1          *aisL1      `json:"l1"`
	ColumnLabel []string    `json:"columnLabel"`
	ColumnData  [][]*string `json:"columnData"`
}

type aisL2 struct {
	ColumnLabel []string    `json:"columnLabel"`
	ColumnData  [][]*string `json:"columnData"`
}

type aisL1 struct {
	ColumnLabel []l1Column  `json:"columnLabel"`
	ColumnData  [][]*string `json:"columnData"`
}

type l1Column struct {
	Field string `json:"field"`
	Name  string `json:"name"`
}

// --- helpers ---

func l2String(l2 *aisL2, col int) string {
	if l2 == nil || len(l2.ColumnData) == 0 || len(l2.ColumnData[0]) <= col {
		return ""
	}
	return ptrString(l2.ColumnData[0][col])
}

func l2Amount(l2 *aisL2, col int) float64 {
	return parseAmount(l2String(l2, col))
}

func l1FieldIndex(cols []l1Column) map[string]int {
	m := make(map[string]int, len(cols))
	for i, c := range cols {
		m[c.Field] = i
	}
	return m
}

func stringIndex(cols []string) map[string]int {
	m := make(map[string]int, len(cols))
	for i, c := range cols {
		m[c] = i
	}
	return m
}

func sumL1Field(l1 *aisL1, field string) float64 {
	if l1 == nil {
		return 0
	}
	idx := l1FieldIndex(l1.ColumnLabel)
	col, ok := idx[field]
	if !ok {
		return 0
	}
	var sum float64
	for _, row := range l1.ColumnData {
		sum += rowAmount(row, col)
	}
	return sum
}

func rowString(row []*string, col int) string {
	if col < 0 || col >= len(row) || row[col] == nil {
		return ""
	}
	return *row[col]
}

func rowAmount(row []*string, col int) float64 {
	return parseAmount(rowString(row, col))
}

func ptrString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func parseAmount(s string) float64 {
	s = strings.ReplaceAll(s, ",", "")
	s = strings.TrimSpace(s)
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func stripTDSPrefix(code string) string {
	return strings.TrimPrefix(code, "TDS-")
}

func splitSource(src string) (name, code string) {
	src = strings.TrimSpace(src)
	idx := strings.LastIndex(src, "(")
	if idx < 0 {
		return src, ""
	}
	name = strings.TrimSpace(src[:idx])
	code = strings.TrimSpace(src[idx+1:])
	code = strings.TrimSuffix(code, ")")
	return name, code
}
