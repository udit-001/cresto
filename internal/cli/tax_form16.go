package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"cresto/internal/greythr"
	"cresto/internal/store"
)

// taxForm16Cmd lists Form 16 documents on file. It's the read path — Form 16
// is a tax artifact, so it lives under 'tax' (not under 'greythr', which is
// the payslip/connection surface).
var taxForm16Cmd = &cobra.Command{
	Use:   "form16",
	Short: "List Form 16 documents on file",
	Long: `List archived Form 16 TDS certificate PDFs (Part A + Part B).

Form 16 is stored for record keeping, not parsed. Use --fy to filter
to a financial year; otherwise lists all on file.

Use 'cresto tax form16 fetch' to pull from the connected greytHR tenant.

Examples:
  cresto tax form16 --json
  cresto tax form16 --fy 2025`,
	Args: cobra.NoArgs,
	RunE:  runTaxForm16List,
}

// taxForm16FetchCmd syncs Form 16 documents from the connected greytHR tenant.
// Source-bound operation: only the connected employer's documents are
// fetchable; other employers' need manual upload (source: manual).
var taxForm16FetchCmd = &cobra.Command{
	Use:   "fetch",
	Short: "Fetch Form 16 PDFs from the connected greytHR tenant",
	Args:  cobra.NoArgs,
	RunE:  runTaxForm16Fetch,
}

func init() {
	taxForm16Cmd.AddCommand(taxForm16FetchCmd)
	taxCmd.AddCommand(taxForm16Cmd)
}

// form16JSON is the agent-safe representation of a stored Form 16 document.
// FilePath is a local path (dropped from CLI JSON — it's machine-local, not
// something an agent acts on).
type form16JSON struct {
	EmployerName string `json:"employer_name"`
	FYStartYear  int    `json:"fy_start_year"`
	Part         string `json:"part"`
	Source       string `json:"source"`
	FetchedAt    string `json:"fetched_at"`
}

func runTaxForm16List(cmd *cobra.Command, args []string) error {
	st := mustStore(cmd)

	fyFilter, hasFY := resolveFY(st, cmd)

	var docs []store.Form16Document
	var err error
	if hasFY {
		docs, err = st.ListForm16DocumentsForFY(cmd.Context(), fyFilter)
	} else {
		docs, err = st.ListForm16Documents(cmd.Context())
	}
	if err != nil {
		return formatError("failed to list form16 documents", err)
	}

	out := make([]form16JSON, 0, len(docs))
	for _, d := range docs {
		out = append(out, form16JSON{
			EmployerName: d.EmployerName,
			FYStartYear:  d.FYStartYear,
			Part:         d.Part,
			Source:       d.Source,
			FetchedAt:    d.FetchedAt,
		})
	}

	if jsonOut {
		printJSON(out)
		return nil
	}

	fmt.Println()
	if len(out) == 0 {
		fmt.Println("  No Form 16 documents on file.")
		fmt.Println()
		return nil
	}

	rows := make([][]string, 0, len(out))
	for _, d := range out {
		rows = append(rows, []string{
			d.EmployerName,
			fmt.Sprintf("FY %d-%d", d.FYStartYear, (d.FYStartYear+1)%100),
			"Part " + d.Part,
			d.Source,
			d.FetchedAt,
		})
	}
	fmt.Println(formatTable([]string{"Employer", "FY", "Part", "Source", "Fetched"}, rows))
	fmt.Println()
	return nil
}

func runTaxForm16Fetch(cmd *cobra.Command, args []string) error {
	cfg := resolveConfig()
	st := mustStore(cmd)
	client := greythr.New(cfg.GreytHRSessionPath)

	if !client.Connected() {
		return fmt.Errorf("greytHR not connected. Connect via the web UI at /greythr")
	}

	ctx := cmd.Context()

	docs, err := client.ListForm16(ctx)
	if err != nil {
		if errors.Is(err, greythr.ErrNotConnected) {
			return fmt.Errorf("greytHR session expired. Reconnect via the web UI at /greythr")
		}
		return fmt.Errorf("list form16: %w", err)
	}

	if len(docs) == 0 {
		fmt.Print("\n  No Form 16 documents found on greytHR.\n\n")
		return nil
	}

	form16Dir := filepath.Join(cfg.DataDir, "form16")
	if err := os.MkdirAll(form16Dir, 0o700); err != nil {
		return fmt.Errorf("create form16 dir: %w", err)
	}

	sess, _ := client.LoadSession()
	employerName := greythr.DeriveEmployerName(sess.Host)

	fetched, failed := 0, 0
	for _, doc := range docs {
		part := doc.Part
		fy := doc.TaxYear
		if fy == 0 {
			fmt.Printf("  ✗ %s: no tax year\n", doc.Title)
			failed++
			continue
		}

		pdfBody, _, err := client.DownloadForm16(ctx, doc.ID)
		if err != nil {
			fmt.Printf("  ✗ %s: %v\n", doc.Title, err)
			failed++
			continue
		}

		filename := fmt.Sprintf("%s_form16_part%s_fy%d.pdf", employerName, part, fy)
		diskPath := filepath.Join(form16Dir, filename)

		if err := saveStream(diskPath, pdfBody); err != nil {
			fmt.Printf("  ✗ %s: save: %v\n", doc.Title, err)
			failed++
			continue
		}

		if err := st.SaveForm16Document(ctx, store.Form16Document{
			EmployerName: employerName,
			FYStartYear:  fy,
			Part:         part,
			Source:       "greythr",
			FilePath:     diskPath,
		}); err != nil {
			fmt.Printf("  ✗ %s: record: %v\n", doc.Title, err)
			failed++
			continue
		}

		fmt.Printf("  ✓ %s (FY %d-%d)\n", doc.Title, fy, (fy+1)%100)
		fetched++
	}

	fmt.Printf("\n  Fetched: %d  Failed: %d\n", fetched, failed)
	return nil
}

func saveStream(path string, r io.ReadCloser) error {
	defer r.Close()
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, r)
	return err
}