package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var canonicalsCmd = &cobra.Command{
	Use:   "canonicals",
	Short: "List the salary component vocabulary",
	Long: `List all canonical component names (basic, hra, epf, tds, etc.)
with their category (earning or deduction) and display name.
Use this to interpret the canonical_id values in payslip and
timeline output.

Examples:
  cresto canonicals --json
  cresto canonicals`,
	Args: cobra.NoArgs,
	RunE:  runCanonicals,
}

func runCanonicals(cmd *cobra.Command, args []string) error {
	s := mustStore(cmd)

	canonicals, err := s.ListCanonicals(cmd.Context())
	if err != nil {
		return formatError("failed to list canonicals", err)
	}

	if jsonOut {
		out := make([]canonicalJSON, 0, len(canonicals))
		for _, c := range canonicals {
			out = append(out, canonicalJSON{
				ID:            c.ID,
				Name:          c.Name,
				DisplayName:   c.DisplayName(),
				Category:      string(c.Category),
				IsUserCreated: c.IsUserCreated,
			})
		}
		printJSON(out)
		return nil
	}

	fmt.Println()
	if len(canonicals) == 0 {
		fmt.Println("  No canonical components found.")
		fmt.Println()
		return nil
	}

	rows := make([][]string, 0, len(canonicals))
	for _, c := range canonicals {
		userCreated := ""
		if c.IsUserCreated {
			userCreated = "yes"
		}
		rows = append(rows, []string{
			fmt.Sprintf("%d", c.ID),
			c.DisplayName(),
			c.Name,
			string(c.Category),
			userCreated,
		})
	}
	fmt.Println(formatTable([]string{"ID", "Display Name", "Slug", "Category", "User"}, rows))
	fmt.Println()
	return nil
}

func init() {
	rootCmd.AddCommand(canonicalsCmd)
}
