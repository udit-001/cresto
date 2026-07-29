package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"cresto/internal/pdfstore"
	"cresto/internal/store"

	"github.com/spf13/cobra"
)

var payslipsDeleteCmd = &cobra.Command{
	Use:   "delete <id> [<id>...]",
	Short: "Delete payslips by ID, employer, or status",
	Long: `Delete payslip records and their associated PDF files.

Accepts one or more payslip IDs, or a filter flag to batch-delete:

  --employer   Delete all payslips for an employer hash
  --status     Delete all payslips with a status (failed, pending_review)

Examples:
  cresto payslips delete 42
  cresto payslips delete 42 43 44
  cresto payslips delete --employer employer_a3f2
  cresto payslips delete --status failed`,
	Args: cobra.ArbitraryArgs,
	RunE: runPayslipsDelete,
}

func runPayslipsDelete(cmd *cobra.Command, args []string) error {
	s := mustStore(cmd)
	ctx := cmd.Context()
	cfg := resolveConfig()
	pdfs := pdfstore.New(cfg.PDFStoragePath)

	employerFlag, _ := cmd.Flags().GetString("employer")
	statusFlag, _ := cmd.Flags().GetString("status")

	var deleted []store.Payslip
	var err error

	switch {
	case employerFlag != "":
		names, err := s.ListEmployers(ctx)
		if err != nil {
			return formatError("failed to list employers", err)
		}
		name, ok := resolveEmployerHash(employerFlag, names)
		if !ok {
			return fmt.Errorf("unknown employer hash %q — run 'cresto employers' to see valid hashes", employerFlag)
		}
		deleted, err = s.DeletePayslipsByEmployer(ctx, name)
		if err != nil {
			return formatError("failed to delete by employer", err)
		}

	case statusFlag != "":
		deleted, err = s.DeletePayslipsByStatus(ctx, store.Status(statusFlag))
		if err != nil {
			return formatError("failed to delete by status", err)
		}

	case len(args) > 0:
		for _, arg := range args {
			id, err := strconv.ParseInt(arg, 10, 64)
			if err != nil {
				return fmt.Errorf("invalid payslip id %q: expected a number", arg)
			}
			p, err := s.GetPayslip(ctx, id)
			if err != nil {
				if err == store.ErrNotFound {
					fmt.Printf("  ✗ payslip %d not found (skipped)\n", id)
					continue
				}
				return formatError(fmt.Sprintf("failed to load payslip %d", id), err)
			}
			if err := s.DeletePayslip(ctx, id); err != nil {
				fmt.Printf("  ✗ payslip %d: %v\n", id, err)
				continue
			}
			deleted = append(deleted, p)
		}

	default:
		return fmt.Errorf("specify payslip IDs, --employer <hash>, or --status <status>")
	}

	for _, p := range deleted {
		if p.RawPDFPath != "" && pdfs.Exists(p.RawPDFPath) {
			_ = os.Remove(pdfs.Abs(p.RawPDFPath))
		}
	}

	if len(deleted) == 0 {
		fmt.Println("  No payslips matched.")
		return nil
	}

	fmt.Printf("  Deleted %d payslip(s)", len(deleted))
	ids := make([]string, len(deleted))
	for i, p := range deleted {
		ids[i] = strconv.FormatInt(p.ID, 10)
	}
	fmt.Printf(" (ID: %s)\n", strings.Join(ids, ", "))
	return nil
}

func init() {
	payslipsCmd.AddCommand(payslipsDeleteCmd)
	payslipsDeleteCmd.Flags().String("employer", "", "Delete all payslips for an employer hash")
	payslipsDeleteCmd.Flags().String("status", "", "Delete all payslips with a status (failed, pending_review)")
}
