package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"cresto/internal/store"
)

var employersCmd = &cobra.Command{
	Use:   "employers",
	Short: "List employer hashes with payslip counts",
	Long: `List all employers as stable anonymized hashes (e.g.
"employer_a3f2") with their confirmed payslip counts. Use the
hash value with 'payslips list --employer <hash>' to filter.

The real employer name is never shown.

Examples:
  cresto employers --json
  cresto employers`,
	Args: cobra.NoArgs,
	RunE:  runEmployers,
}

func runEmployers(cmd *cobra.Command, args []string) error {
	s := mustStore(cmd)

	names, err := s.ListEmployers(cmd.Context())
	if err != nil {
		return formatError("failed to list employers", err)
	}

	type entry struct {
		ID           string `json:"id"`
		PayslipCount int    `json:"payslip_count"`
	}

	entries := make([]entry, 0, len(names))
	for _, name := range names {
		count, err := s.CountPayslips(cmd.Context(), store.Filter{Employer: name})
		if err != nil {
			return formatError(fmt.Sprintf("failed to count payslips for employer hash %s", employerHash(name)), err)
		}
		entries = append(entries, entry{
			ID:           employerHash(name),
			PayslipCount: count,
		})
	}

	if jsonOut {
		printJSON(entries)
		return nil
	}

	fmt.Println()
	if len(entries) == 0 {
		fmt.Println("  No employers found.")
		fmt.Println()
		return nil
	}

	rows := make([][]string, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, []string{
			e.ID,
			fmt.Sprintf("%d", e.PayslipCount),
		})
	}
	fmt.Println(formatTable([]string{"Employer Hash", "Payslips"}, rows))
	fmt.Println()
	return nil
}

func init() {
	rootCmd.AddCommand(employersCmd)
}
