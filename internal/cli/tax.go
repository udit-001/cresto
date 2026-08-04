package cli

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"cresto/internal/ais"
	"cresto/internal/store"
	"cresto/internal/tax"
)

var taxCmd = &cobra.Command{
	Use:   "tax",
	Short: "Tax filing data: liability, AIS income, capital gains, TDS reconciliation",
	Long: `Read-only access to tax filing data computed from AIS imports,
Kite Console capital gains, and confirmed payslips.

PII is redacted from all output. Employer/deductor names (Section 192)
are shown as a stable hash (e.g. "employer_a3f2") — the same hash used
by 'cresto payslips', so AIS, TDS, and payslip data correlate.

Commands:
  cresto tax                Tax liability summary + refund/dues + readiness
  cresto tax income         AIS salary, interest, dividends, securities sales
  cresto tax capital-gains  Kite Console FIFO-matched trades (STCG/LTCG)
  cresto tax tds            AIS-vs-Cresto TDS reconciliation
  cresto tax form16         List Form 16 documents on file (fetch to sync)

All commands default to the latest AIS import's FY. Use --fy to override.
All commands support --json for machine-readable output.`,
	Args: cobra.NoArgs,
	RunE:  runTaxSummary,
}

var taxIncomeCmd = &cobra.Command{
	Use:   "income",
	Short: "AIS income entries: salary, interest, dividends, securities",
	Long: `Salary, savings/FD interest, dividends, and securities sales
from the imported AIS JSON. Employer names are hashed; bank and
company names are kept (public institutions).`,
	Args: cobra.NoArgs,
	RunE:  runTaxIncome,
}

var taxCapitalGainsCmd = &cobra.Command{
	Use:   "capital-gains",
	Short: "Kite Console capital gains trades (STCG/LTCG)",
	Long: `FIFO-matched trades from the Kite Console Tax P&L export.
Includes buy/sell values, taxable profit, FMV, and STT.`,
	Args: cobra.NoArgs,
	RunE:  runTaxCapitalGains,
}

var taxTdsCmd = &cobra.Command{
	Use:   "tds",
	Short: "TDS reconciliation: AIS vs Cresto payslips",
	Long: `Each AIS TDS entry reconciled against Cresto's payslip-derived
TDS for the same employer. Status: match, gap, or no_payslips.
Section 192 deductors are hashed; others are kept (public institutions).`,
	Args: cobra.NoArgs,
	RunE:  runTaxTds,
}

func init() {
	taxCmd.PersistentFlags().Int("fy", 0, "Financial year start (e.g. 2025 for FY 2025-26). Defaults to latest AIS import.")
	taxCmd.AddCommand(taxIncomeCmd)
	taxCmd.AddCommand(taxCapitalGainsCmd)
	taxCmd.AddCommand(taxTdsCmd)
	rootCmd.AddCommand(taxCmd)
}

// resolveFY returns the FY start year from --fy or the latest AIS import.
// Returns false if no FY could be determined (no flag, no imports).
func resolveFY(s *store.Store, cmd *cobra.Command) (int, bool) {
	if fy, _ := cmd.Flags().GetInt("fy"); fy > 0 {
		return fy, true
	}
	imports, _ := s.ListAISImports(cmd.Context())
	if len(imports) > 0 {
		return imports[0].FYStartYear, true
	}
	return 0, false
}

// newEmployerResolver returns a function that maps AIS employer names to
// their canonical payslip name via fuzzy matching. This ensures the hash
// for an employer is the same whether it came from AIS or payslips — the
// payslip name is the canonical spelling Cresto confirmed. When no payslip
// match exists, the AIS name passes through unchanged.
func newEmployerResolver(s *store.Store, ctx context.Context, fyStart int) func(string) string {
	employers, _ := s.GetFYEmployerTDS(ctx, fyStart)
	byName := make(map[string]store.EmployerTDS, len(employers))
	for _, e := range employers {
		byName[e.EmployerName] = e
	}
	return func(aisName string) string {
		if emp, ok := store.MatchEmployerTDS(aisName, byName); ok {
			return emp.EmployerName
		}
		return aisName
	}
}

// loadAIS reads and parses the AIS JSON for the given FY from disk.
// Returns the parsed AIS and the import record, or an error.
func loadAIS(s *store.Store, cmd *cobra.Command, fyStart int) (ais.ParsedAIS, error) {
	im, err := s.GetAISImport(cmd.Context(), fyStart)
	if err != nil {
		return ais.ParsedAIS{}, fmt.Errorf("no AIS import for FY %d: %w", fyStart, err)
	}
	raw, err := os.ReadFile(im.RawJSONPath)
	if err != nil {
		return ais.ParsedAIS{}, fmt.Errorf("could not read AIS JSON at %s: %w", im.RawJSONPath, err)
	}
	parsed, err := ais.Parse(raw)
	if err != nil {
		return ais.ParsedAIS{}, fmt.Errorf("could not parse AIS JSON: %w", err)
	}
	return parsed, nil
}

// --- cresto tax ---

func runTaxSummary(cmd *cobra.Command, args []string) error {
	s := mustStore(cmd)
	ctx := cmd.Context()

	fyStart, hasFY := resolveFY(s, cmd)

	out := taxSummaryJSON{}

	if _, err := s.GetTaxpayerProfile(ctx); err == nil {
		out.ProfileSet = true
	}
	if accounts, err := s.ListBankAccounts(ctx); err == nil {
		for _, a := range accounts {
			if a.IsPrimary {
				out.PrimaryBankSet = true
				break
			}
		}
	}

	if !hasFY {
		if jsonOut {
			printJSON(out)
			return nil
		}
		fmt.Print("\n  No AIS imported. Import via the web UI (/tax) to get started.\n\n")
		return nil
	}

	out.FYStartYear = fyStart
	out.FY = fmt.Sprintf("%d-%d", fyStart, (fyStart+1)%100)

	parsed, err := loadAIS(s, cmd, fyStart)
	if err == nil {
		out.AISImported = true
		for _, sal := range parsed.Salaries {
			out.TotalSalary += sal.GrossSalary
		}
		for _, si := range parsed.SavingsInterest {
			out.TotalSavings += si.Amount
		}
		for _, fd := range parsed.FDInterest {
			out.TotalFD += fd.Amount
		}
		for _, d := range parsed.Dividends {
			out.TotalDividends += d.Amount
		}
		for _, tds := range parsed.TDS {
			out.TotalAISTDS += tds.TDS
		}
		for _, at := range parsed.AdvanceTax {
			if ais.FYStartYear(at.FY) == fyStart {
				out.TotalAdvanceTax += at.Total
			} else {
				out.ExcludedAdvanceTax = append(out.ExcludedAdvanceTax, redactAdvanceTaxEntry(at))
			}
		}
	}

	if trades, err := s.ListCapitalGainsTrades(ctx, fyStart); err == nil && len(trades) > 0 {
		out.CGImported = true
		for _, tr := range trades {
			if strings.Contains(tr.Section, "Short Term") {
				out.TotalSTCG += tr.TaxableProfit
			} else if strings.Contains(tr.Section, "Long Term") {
				out.TotalLTCG += tr.TaxableProfit
			}
		}
	}

	if f16, err := s.ListForm16DocumentsForFY(ctx, fyStart); err == nil && len(f16) > 0 {
		out.Form16OnFile = true
	}

	out.Breakdown = tax.Compute(tax.Input{
		GrossSalary:     out.TotalSalary,
		SavingsInterest: out.TotalSavings,
		FDInterest:      out.TotalFD,
		Dividends:       out.TotalDividends,
		STCG:            out.TotalSTCG,
		LTCG:            out.TotalLTCG,
	})
	out.RefundDue = (out.TotalAISTDS + out.TotalAdvanceTax) - out.Breakdown.TotalTaxLiability
	out.HasRefund = out.RefundDue > 0
	out.ExportReady = out.AISImported && out.ProfileSet && out.PrimaryBankSet

	if jsonOut {
		printJSON(out)
		return nil
	}

	printTaxSummaryText(out)
	return nil
}

func printTaxSummaryText(v taxSummaryJSON) {
	fmt.Printf("\n  FY %s\n\n", v.FY)

	fmt.Println("  Income")
	fmt.Printf("    Salary            %s\n", moneyPlain(v.TotalSalary))
	fmt.Printf("    Savings Interest  %s\n", moneyPlain(v.TotalSavings))
	fmt.Printf("    FD Interest       %s\n", moneyPlain(v.TotalFD))
	fmt.Printf("    Dividends         %s\n", moneyPlain(v.TotalDividends))
	fmt.Printf("    STCG (taxable)    %s\n", moneyPlain(v.TotalSTCG))
	fmt.Printf("    LTCG (taxable)    %s\n", moneyPlain(v.TotalLTCG))
	fmt.Println()

	b := v.Breakdown
	fmt.Println("  Tax Liability")
	fmt.Printf("    Taxable Income    %s\n", moneyPlain(b.TotalTaxableIncome))
	fmt.Printf("    Tax (slabs)       %s\n", moneyPlain(b.NormalRateTax))
	fmt.Printf("    Tax (CG rates)    %s\n", moneyPlain(b.SpecialRateTax))
	if b.Rebate87A > 0 {
		fmt.Printf("    Rebate 87A       -%s\n", moneyPlain(b.Rebate87A))
	}
	if b.MarginalRelief > 0 {
		fmt.Printf("    Marginal Relief  -%s\n", moneyPlain(b.MarginalRelief))
	}
	if b.Surcharge > 0 {
		fmt.Printf("    Surcharge (%.0f%%)  %s\n", b.SurchargeRate*100, moneyPlain(b.Surcharge))
	}
	fmt.Printf("    Cess (4%%)         %s\n", moneyPlain(b.Cess))
	fmt.Printf("    ─────────────────────────────\n")
	fmt.Printf("    Total Liability   %s\n", moneyPlain(b.TotalTaxLiability))
	fmt.Println()

	totalPaid := v.TotalAISTDS + v.TotalAdvanceTax
	fmt.Printf("  TDS + Advance Tax   %s\n", moneyPlain(totalPaid))
	if v.HasRefund {
		fmt.Printf("  Refund Due          %s\n", moneyPlain(math.Abs(v.RefundDue)))
	} else if v.RefundDue < 0 {
		fmt.Printf("  Balance Due         %s\n", moneyPlain(math.Abs(v.RefundDue)))
	} else {
		fmt.Println("  No refund or balance due.")
	}

	if len(v.ExcludedAdvanceTax) > 0 {
		fmt.Println()
		fmt.Printf("  ⚠ %d tax payment(s) for other FYs excluded:\n", len(v.ExcludedAdvanceTax))
		for _, at := range v.ExcludedAdvanceTax {
			fmt.Printf("    %s  %s  %s  %s\n", at.FY, at.MinorHead, moneyPlain(at.Total), at.Date)
		}
	}

	fmt.Print("\n  Readiness: ")
	flags := []string{}
	flags = append(flags, readiness("AIS", v.AISImported))
	flags = append(flags, readiness("Capital Gains", v.CGImported))
	flags = append(flags, readiness("Form 16", v.Form16OnFile))
	flags = append(flags, readiness("Profile", v.ProfileSet))
	flags = append(flags, readiness("Bank", v.PrimaryBankSet))
	if v.ExportReady {
		flags = append(flags, "Export Ready")
	}
	fmt.Println(strings.Join(flags, "  "))
	fmt.Println()
}

func readiness(name string, ok bool) string {
	if ok {
		return "✓ " + name
	}
	return "✗ " + name
}

// --- cresto tax income ---

func runTaxIncome(cmd *cobra.Command, args []string) error {
	s := mustStore(cmd)
	ctx := cmd.Context()

	fyStart, hasFY := resolveFY(s, cmd)
	if !hasFY {
		return fmt.Errorf("no AIS imported — import via the web UI (/tax) first, or use --fy")
	}

	parsed, err := loadAIS(s, cmd, fyStart)
	if err != nil {
		return err
	}

	resolve := newEmployerResolver(s, ctx, fyStart)

	out := taxIncomeJSON{}
	for _, sal := range parsed.Salaries {
		out.Salaries = append(out.Salaries, redactSalaryEntry(sal, resolve(sal.Employer)))
	}
	for _, si := range parsed.SavingsInterest {
		out.SavingsInterest = append(out.SavingsInterest, redactInterestEntry(si))
	}
	for _, fd := range parsed.FDInterest {
		out.FDInterest = append(out.FDInterest, redactInterestEntry(fd))
	}
	for _, d := range parsed.Dividends {
		out.Dividends = append(out.Dividends, redactDividendEntry(d))
	}
	for _, ss := range parsed.Securities {
		out.Securities = append(out.Securities, redactSecuritySale(ss))
	}
	for _, at := range parsed.AdvanceTax {
		out.AdvanceTax = append(out.AdvanceTax, redactAdvanceTaxEntry(at))
	}

	if jsonOut {
		printJSON(out)
		return nil
	}

	printTaxIncomeText(out)
	return nil
}

func printTaxIncomeText(v taxIncomeJSON) {
	fmt.Println()
	if len(v.Salaries) > 0 {
		rows := make([][]string, 0, len(v.Salaries))
		for _, s := range v.Salaries {
			rows = append(rows, []string{s.Employer, moneyPlain(s.GrossSalary), moneyPlain(s.TDS)})
		}
		fmt.Println("  Salary")
		fmt.Println(formatTable([]string{"Employer", "Gross", "TDS"}, rows))
	}
	if len(v.SavingsInterest) > 0 {
		rows := make([][]string, 0, len(v.SavingsInterest))
		for _, e := range v.SavingsInterest {
			rows = append(rows, []string{e.Bank, moneyPlain(e.Amount)})
		}
		fmt.Println("  Savings Interest")
		fmt.Println(formatTable([]string{"Bank", "Amount"}, rows))
	}
	if len(v.FDInterest) > 0 {
		rows := make([][]string, 0, len(v.FDInterest))
		for _, e := range v.FDInterest {
			rows = append(rows, []string{e.Bank, moneyPlain(e.Amount)})
		}
		fmt.Println("  FD Interest")
		fmt.Println(formatTable([]string{"Bank", "Amount"}, rows))
	}
	if len(v.Dividends) > 0 {
		rows := make([][]string, 0, len(v.Dividends))
		for _, d := range v.Dividends {
			rows = append(rows, []string{d.Company, moneyPlain(d.Amount), moneyPlain(d.TDS)})
		}
		fmt.Println("  Dividends")
		fmt.Println(formatTable([]string{"Company", "Amount", "TDS"}, rows))
	}
	if len(v.Securities) > 0 {
		rows := make([][]string, 0, len(v.Securities))
		for _, ss := range v.Securities {
			rows = append(rows, []string{ss.SecurityName, moneyPlain(ss.SalesConsideration), moneyPlain(ss.CostOfAcquisition), ss.Type})
		}
		fmt.Println("  Securities Sales (AIS)")
		fmt.Println(formatTable([]string{"Security", "Sales", "Cost", "Type"}, rows))
	}
	if len(v.AdvanceTax) > 0 {
		rows := make([][]string, 0, len(v.AdvanceTax))
		for _, at := range v.AdvanceTax {
			rows = append(rows, []string{at.FY, at.MinorHead, moneyPlain(at.Total), at.Date})
		}
		fmt.Println("  Advance / Self-Assessment Tax")
		fmt.Println(formatTable([]string{"FY", "Head", "Total", "Date"}, rows))
	}
	if empty := len(v.Salaries) + len(v.SavingsInterest) + len(v.FDInterest) + len(v.Dividends) + len(v.Securities) + len(v.AdvanceTax); empty == 0 {
		fmt.Println("  No income entries found in AIS.")
	}
	fmt.Println()
}

// --- cresto tax capital-gains ---

func runTaxCapitalGains(cmd *cobra.Command, args []string) error {
	s := mustStore(cmd)
	ctx := cmd.Context()

	fyStart, hasFY := resolveFY(s, cmd)
	if !hasFY {
		return fmt.Errorf("no FY determined — import AIS via the web UI (/tax) first, or use --fy")
	}

	trades, err := s.ListCapitalGainsTrades(ctx, fyStart)
	if err != nil {
		return formatError("failed to load capital gains trades", err)
	}

	if jsonOut {
		printJSON(redactCapitalGainsTrades(trades))
		return nil
	}

	fmt.Println()
	if len(trades) == 0 {
		fmt.Println("  No capital gains trades found for this FY.")
		fmt.Println()
		return nil
	}

	rows := make([][]string, 0, len(trades))
	for _, t := range trades {
		rj := redactCapitalGainsTrade(t)
		rows = append(rows, []string{
			rj.Symbol,
			rj.Section,
			rj.EntryDate,
			rj.ExitDate,
			moneyPlain(rj.Profit),
			moneyPlain(rj.TaxableProfit),
			rj.Section,
		})
	}
	// Shorten the Section column for display
	fmt.Println(formatTable([]string{"Symbol", "Type", "Buy Date", "Sell Date", "P&L", "Taxable"}, rows))
	fmt.Println()
	return nil
}

// --- cresto tax tds ---

func runTaxTds(cmd *cobra.Command, args []string) error {
	s := mustStore(cmd)
	ctx := cmd.Context()

	fyStart, hasFY := resolveFY(s, cmd)
	if !hasFY {
		return fmt.Errorf("no AIS imported — import via the web UI (/tax) first, or use --fy")
	}

	parsed, err := loadAIS(s, cmd, fyStart)
	if err != nil {
		return err
	}

	employerTDS, _ := s.GetFYEmployerTDS(ctx, fyStart)
	tdsByEmployer := make(map[string]store.EmployerTDS, len(employerTDS))
	for _, e := range employerTDS {
		tdsByEmployer[e.EmployerName] = e
	}

	recon := make([]tdsReconJSON, 0, len(parsed.TDS))
	for _, tds := range parsed.TDS {
		r := tdsReconJSON{
			Section:   tds.Section,
			AISIncome: tds.Income,
			AISTDS:    tds.TDS,
		}

		if tds.Section == "192" {
			if emp, ok := store.MatchEmployerTDS(tds.Deductor, tdsByEmployer); ok {
				r.Deductor = redactDeductor(tds.Section, tds.Deductor, emp.EmployerName)
				r.CrestoTDS = emp.TDS
				r.HasPayslips = true
				r.GapAmount = tds.TDS - emp.TDS
				if math.Abs(r.GapAmount) < 1 {
					r.Status = "match"
				} else {
					r.Status = "gap"
				}
			} else {
				r.Deductor = redactDeductor(tds.Section, tds.Deductor, "")
				r.Status = "no_payslips"
			}
		} else {
			r.Deductor = redactDeductor(tds.Section, tds.Deductor, "")
			if tds.TDS == 0 {
				r.Status = "match"
			} else {
				r.Status = "no_payslips"
			}
		}
		recon = append(recon, r)
	}

	if jsonOut {
		printJSON(recon)
		return nil
	}

	fmt.Println()
	if len(recon) == 0 {
		fmt.Println("  No TDS entries found in AIS.")
		fmt.Println()
		return nil
	}

	rows := make([][]string, 0, len(recon))
	for _, r := range recon {
		statusDisplay := r.Status
		switch r.Status {
		case "match":
			statusDisplay = "✓ match"
		case "gap":
			statusDisplay = "⚠ gap"
		case "no_payslips":
			statusDisplay = "  no payslips"
		}
		rows = append(rows, []string{
			r.Deductor,
			r.Section,
			moneyPlain(r.AISTDS),
			moneyPlain(r.CrestoTDS),
			statusDisplay,
		})
	}
	fmt.Println(formatTable([]string{"Deductor", "Sec", "AIS TDS", "Cresto TDS", "Status"}, rows))
	fmt.Println()
	return nil
}
