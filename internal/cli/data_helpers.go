package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/dustin/go-humanize"
)

// moneyPlain formats a float as a grouped decimal, dropping the fractional
// part when it's .00. Mirrors the web layer's formatMoneyPlain so CLI and
// web agree on number rendering.
func moneyPlain(v float64) string {
	if v == float64(int64(v)) {
		return humanize.Comma(int64(v))
	}
	return humanize.Commaf(v)
}

// periodLabel renders (month, year) as "July 2026". Returns "—" when either
// is missing (e.g. pending payslips where the LLM couldn't parse the period).
func periodLabel(m, y int) string {
	if m == 0 || y == 0 {
		return "—"
	}
	if m < 1 || m > 12 {
		return fmt.Sprintf("%d/%d", m, y)
	}
	return time.Month(m).String() + " " + fmt.Sprintf("%d", y)
}

// formatTable renders a column-aligned table with a header and separator line.
// Cell content is truncated to 40 chars per column. Returns "" for empty rows.
func formatTable(header []string, rows [][]string) string {
	if len(rows) == 0 {
		return ""
	}
	colWidths := make([]int, len(header))
	for i, h := range header {
		colWidths[i] = len(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if len(cell) > colWidths[i] {
				colWidths[i] = len(cell)
			}
		}
	}
	for i := range colWidths {
		if colWidths[i] > 40 {
			colWidths[i] = 40
		}
	}

	var b strings.Builder
	for i, h := range header {
		if i > 0 {
			b.WriteString("  ")
		}
		fmt.Fprintf(&b, "%-*s", colWidths[i], h)
	}
	b.WriteString("\n")

	sepCount := 0
	for _, w := range colWidths {
		sepCount += w
	}
	b.WriteString(strings.Repeat("─", sepCount+2*(len(header)-1)))
	b.WriteString("\n")

	for _, row := range rows {
		for i, cell := range row {
			if i > 0 {
				b.WriteString("  ")
			}
			display := cell
			if len(display) > colWidths[i] {
				display = display[:colWidths[i]-3] + "..."
			}
			fmt.Fprintf(&b, "%-*s", colWidths[i], display)
		}
		b.WriteString("\n")
	}

	return b.String()
}
