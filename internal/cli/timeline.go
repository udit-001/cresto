package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var timelineCmd = &cobra.Command{
	Use:   "timeline",
	Short: "All confirmed payslips chronologically, with components",
	Long: `Return every confirmed payslip with its full component breakdown,
ordered oldest-period first. This is the primary data source for
income trend analysis — one call gives the agent the complete
confirmed series to compute deltas, YTD totals, and YoY from.

PII fields are redacted. Employer is shown as a stable hash.

Examples:
  cresto timeline --json
  cresto timeline`,
	Args: cobra.NoArgs,
	RunE:  runTimeline,
}

func runTimeline(cmd *cobra.Command, args []string) error {
	s := mustStore(cmd)

	payslips, err := s.GetConfirmedTimeline(cmd.Context())
	if err != nil {
		return formatError("failed to load confirmed timeline", err)
	}

	if jsonOut {
		printJSON(redactPayslips(payslips))
		return nil
	}

	fmt.Println()
	if len(payslips) == 0 {
		fmt.Println("  No confirmed payslips yet.")
		fmt.Println()
		return nil
	}

	rows := make([][]string, 0, len(payslips))
	for _, p := range payslips {
		rows = append(rows, []string{
			fmt.Sprintf("%d", p.ID),
			employerHash(p.EmployerName),
			periodLabel(p.PayPeriodMonth, p.PayPeriodYear),
			fmt.Sprintf("%d", len(p.Components)),
			moneyPlain(p.GrossSalary),
			moneyPlain(p.NetPay),
		})
	}
	fmt.Println(formatTable([]string{"ID", "Employer", "Period", "Components", "Gross", "Net"}, rows))
	fmt.Printf("  %d confirmed payslips\n", len(payslips))
	fmt.Println()
	return nil
}

func init() {
	rootCmd.AddCommand(timelineCmd)
}
