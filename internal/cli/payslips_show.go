package cli

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"
	"cresto/internal/store"
)

var payslipsShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show one payslip with full component breakdown",
	Long: `Print a single payslip's financial data with all earnings and
deduction line items. PII fields are redacted.

Examples:
  cresto payslips show 42
  cresto payslips show 42 --json`,
	Args: cobra.ExactArgs(1),
	RunE:  runPayslipsShow,
}

func runPayslipsShow(cmd *cobra.Command, args []string) error {
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return fmt.Errorf("invalid payslip id %q: expected a number", args[0])
	}

	s := mustStore(cmd)
	p, err := s.GetPayslip(cmd.Context(), id)
	if err != nil {
		if err == store.ErrNotFound {
			return fmt.Errorf("payslip %d not found", id)
		}
		return formatError("failed to get payslip", err)
	}

	if jsonOut {
		printJSON(redactPayslip(p))
		return nil
	}

	fmt.Println()
	fmt.Printf("  Payslip #%d\n", p.ID)
	fmt.Printf("  Employer:  %s\n", employerHash(p.EmployerName))
	fmt.Printf("  Period:    %s\n", periodLabel(p.PayPeriodMonth, p.PayPeriodYear))
	fmt.Printf("  Status:    %s\n", p.Status)
	fmt.Printf("  Pay Days:  %d / %d\n", p.PayDays, p.TotalDays)
	fmt.Println()
	fmt.Printf("  Gross:         %s\n", moneyPlain(p.GrossSalary))
	fmt.Printf("  Deductions:    %s\n", moneyPlain(p.TotalDeductions))
	fmt.Printf("  Net Pay:       %s\n", moneyPlain(p.NetPay))
	fmt.Println()

	if len(p.Components) > 0 {
		rows := make([][]string, 0, len(p.Components))
		for _, c := range p.Components {
			rows = append(rows, []string{
				c.RawLabel,
				string(c.Category),
				moneyPlain(c.Amount),
				moneyPlain(c.YTDAmt),
			})
		}
		fmt.Println(formatTable([]string{"Component", "Category", "Amount", "YTD"}, rows))
		fmt.Println()
	}

	return nil
}

func init() {
	payslipsCmd.AddCommand(payslipsShowCmd)
}
