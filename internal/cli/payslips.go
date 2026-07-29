package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"cresto/internal/store"
)

var payslipsCmd = &cobra.Command{
	Use:   "payslips",
	Short: "List and inspect parsed payslip data",
	Long: `Read-only access to parsed payslip financial data.

PII fields (employer name, employee ID, designation, PDF paths)
are redacted from all output. Employer is shown as a stable hash
(e.g. "employer_a3f2"); use 'cresto employers' to list
available employer hashes.

Examples:
  cresto payslips list --json
  cresto payslips list --status confirmed --year 2025
  cresto payslips show 42 --json`,
	Args: cobra.NoArgs,
	RunE:  runShowHelp,
}

var payslipsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List payslips (without component breakdown)",
	Long: `List payslips newest-period first, without component details.
Use 'payslips show <id>' for the full component breakdown.

Filters:
  --status    Filter by status: confirmed, pending_review, processing, failed
  --employer  Filter by employer hash (see 'cresto employers')
  --year      Filter by pay period year (e.g. 2025)
  --limit     Maximum number of payslips to return (0 = all)

Examples:
  cresto payslips list
  cresto payslips list --status confirmed --json
  cresto payslips list --employer employer_a3f2 --year 2025`,
	Args: cobra.NoArgs,
	RunE:  runPayslipsList,
}

func runPayslipsList(cmd *cobra.Command, args []string) error {
	s := mustStore(cmd)

	f := store.Filter{}
	if status, _ := cmd.Flags().GetString("status"); status != "" {
		f.Status = store.Status(status)
	}
	if empHash, _ := cmd.Flags().GetString("employer"); empHash != "" {
		names, err := s.ListEmployers(cmd.Context())
		if err != nil {
			return formatError("failed to list employers for hash lookup", err)
		}
		name, ok := resolveEmployerHash(empHash, names)
		if !ok {
			return fmt.Errorf("unknown employer hash %q — run 'cresto employers' to see valid hashes", empHash)
		}
		f.Employer = name
	}
	if year, _ := cmd.Flags().GetInt("year"); year > 0 {
		f.YearFrom = year
		f.YearTo = year
	}
	if limit, _ := cmd.Flags().GetInt("limit"); limit > 0 {
		f.Limit = limit
	}

	payslips, err := s.ListPayslips(cmd.Context(), f)
	if err != nil {
		return formatError("failed to list payslips", err)
	}

	if jsonOut {
		printJSON(redactPayslips(payslips))
		return nil
	}

	fmt.Println()
	if len(payslips) == 0 {
		fmt.Println("  No payslips found.")
		fmt.Println()
		return nil
	}

	rows := make([][]string, 0, len(payslips))
	for _, p := range payslips {
		rows = append(rows, []string{
			fmt.Sprintf("%d", p.ID),
			employerHash(p.EmployerName),
			periodLabel(p.PayPeriodMonth, p.PayPeriodYear),
			string(p.Status),
			moneyPlain(p.GrossSalary),
			moneyPlain(p.NetPay),
		})
	}
	fmt.Println(formatTable([]string{"ID", "Employer", "Period", "Status", "Gross", "Net"}, rows))
	fmt.Println()
	return nil
}

func init() {
	rootCmd.AddCommand(payslipsCmd)
	payslipsCmd.AddCommand(payslipsListCmd)
	payslipsListCmd.Flags().String("status", "", "Filter by status (confirmed, pending_review, processing, failed)")
	payslipsListCmd.Flags().String("employer", "", "Filter by employer hash")
	payslipsListCmd.Flags().Int("year", 0, "Filter by pay period year")
	payslipsListCmd.Flags().Int("limit", 0, "Maximum payslips to return (0 = all)")
}
