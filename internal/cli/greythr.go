package cli

import (
	"context"
	"errors"
	"fmt"
	"log"

	"cresto/internal/greythr"
	"cresto/internal/pdfstore"
	"cresto/internal/store"

	"github.com/spf13/cobra"
)

var greythrCmd = &cobra.Command{
	Use:   "greythr",
	Short: "Fetch payslips from greytHR ESS portal",
	Long: `Fetch payslips from greytHR's Employee Self Service portal.

Uses greytHR's JSON API for structured payslip data — no LLM vision
extraction needed. PDFs are also downloaded for archival.

Connection is managed via the web UI at /greythr. This command reads
the saved session to fetch payslips.

Examples:
  cresto greythr fetch`,
}

var greythrFetchCmd = &cobra.Command{
	Use:   "fetch",
	Short: "Fetch all unpublished payslips from greytHR",
	Args:  cobra.NoArgs,
	RunE:  runGreytHRFetch,
}

func init() {
	greythrCmd.AddCommand(greythrFetchCmd)
	rootCmd.AddCommand(greythrCmd)
}

func runGreytHRFetch(cmd *cobra.Command, args []string) error {
	cfg := resolveConfig()
	st := mustStore(cmd)
	pdfs := pdfstore.New(cfg.PDFStoragePath)
	client := greythr.New(cfg.GreytHRSessionPath)

	if !client.Connected() {
		return fmt.Errorf("greytHR not connected. Start the server (cresto start) and connect via the web UI at /greythr")
	}

	ctx := cmd.Context()

	months, err := client.ListPayslipMonths(ctx)
	if err != nil {
		if errors.Is(err, greythr.ErrNotConnected) {
			return fmt.Errorf("greytHR session expired. Reconnect via the web UI at /greythr")
		}
		return fmt.Errorf("list months: %w", err)
	}

	canonicals, err := st.ListCanonicals(ctx)
	if err != nil {
		return fmt.Errorf("load canonicals: %w", err)
	}

	sess, _ := client.LoadSession()

	// Fetch employee info once for all payslips (best-effort).
	info, empErr := client.FetchEmployeeInfo(ctx)
	if empErr != nil {
		fmt.Printf("  ⚠ Could not fetch employee info (continuing without): %v\n", empErr)
	}

	// YTD cache: FY year → summary. Lazy-fetched per FY (one API call covers
	// all months in the financial year).
	ytdCache := make(map[int]*greythr.YTDSummary)

	fetched, skipped, failed := 0, 0, 0
	for _, m := range months.Months {
		if !m.Released {
			continue
		}

		// Dedup: skip if a payslip for this period already exists.
		pm, py := greythr.ParseFromDate(m.FromDate)
		if pm == 0 || py == 0 {
			log.Printf("skip %s: could not parse fromDate %q", m.Month, m.FromDate)
			skipped++
			continue
		}
		existing, err := st.ListPayslips(ctx, store.Filter{
			MonthFrom: pm, MonthTo: pm, YearFrom: py, YearTo: py, Limit: 1,
		})
		if err != nil {
			log.Printf("dedup check %s: %v", m.Month, err)
		}
		if len(existing) > 0 {
			skipped++
			continue
		}

		fyYear := greythr.FYYearFor(pm, py)
		if _, ok := ytdCache[fyYear]; !ok {
			ytd, err := client.FetchYTDSummary(ctx, fyYear)
			if err != nil {
				log.Printf("fetch YTD for FY %d (continuing without): %v", fyYear, err)
			} else {
				ytdCache[fyYear] = ytd
			}
		}

		p, err := fetchAndMapGreytHR(ctx, client, pdfs, m, sess.Host, info, ytdCache, canonicals)
		if err != nil {
			fmt.Printf("  ✗ %s: %v\n", m.Month, err)
			failed++
			continue
		}

		p.Status = store.StatusPendingReview
		if len(p.Components) == 0 {
			fmt.Printf("  ~ %s (empty, skipped)\n", m.Month)
			skipped++
			continue
		}
		if err := st.SavePayslip(ctx, &p); err != nil {
			fmt.Printf("  ✗ %s: save: %v\n", m.Month, err)
			failed++
			continue
		}

		fmt.Printf("  ✓ %s\n", m.Month)
		fetched++
	}

	fmt.Printf("\n  Fetched: %d  Skipped: %d  Failed: %d\n", fetched, skipped, failed)
	return nil
}

func fetchAndMapGreytHR(ctx context.Context, client *greythr.Client, pdfs *pdfstore.Store, m greythr.PayslipMonth, host string, info greythr.EmployeeInfo, ytdCache map[int]*greythr.YTDSummary, canonicals []store.Canonical) (store.Payslip, error) {
	data, err := client.FetchPayslipData(ctx, m.ID)
	if err != nil {
		return store.Payslip{}, fmt.Errorf("fetch data: %w", err)
	}

	payMonth, payYear := greythr.ParseFromDate(m.FromDate)
	fyYear := greythr.FYYearFor(payMonth, payYear)
	var ytd map[string]float64
	if summary := ytdCache[fyYear]; summary != nil {
		ytd = summary.YTDForMonth(payMonth)
	}

	p, err := greythr.MapToPayslip(data, m, host, canonicals, ytd)
	if err != nil {
		return store.Payslip{}, fmt.Errorf("map: %w", err)
	}
	p.Designation = info.Designation
	p.EmployeeID = info.EmployeeNo

	// Download PDF for archival (best-effort).
	pdfBody, filename, err := client.DownloadPayslipPDF(ctx, m.ID)
	if err != nil {
		log.Printf("download PDF for %s (continuing without): %v", m.Month, err)
		return p, nil
	}
	defer pdfBody.Close()

	relPath, err := pdfs.Save(filename, pdfBody)
	if err != nil {
		log.Printf("save PDF for %s (continuing without): %v", m.Month, err)
		return p, nil
	}
	p.RawPDFPath = relPath
	return p, nil
}
