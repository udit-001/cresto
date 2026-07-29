package cli

import (
	"context"
	"errors"
	"fmt"

	"cresto/internal/config"
	"cresto/internal/groww"
	"cresto/internal/kite"

	"github.com/spf13/cobra"
)

var holdingsBrokerFlag string

var holdingsCmd = &cobra.Command{
	Use:   "holdings",
	Short: "List stock and mutual fund holdings from connected brokers",
	Long: `Fetch live holdings from Groww and/or Zerodha (Kite) via their
MCP integrations. No database or running server required — this
command talks to the broker APIs directly.

Connection state is shown as content, not errors: if a broker isn't
connected, its section says so and the command still succeeds.

Examples:
  cresto holdings
  cresto holdings --broker groww
  cresto holdings --broker kite --json`,
	Args: cobra.NoArgs,
	RunE:  runHoldings,
}

func init() {
	rootCmd.AddCommand(holdingsCmd)
	holdingsCmd.Flags().StringVar(&holdingsBrokerFlag, "broker", "", "Filter by broker: groww, kite")
}

// brokerHoldings is the JSON shape for --json output. Each broker
// appears as a key with its holdings or a connection error.
type brokerHoldings struct {
	Groww      *growwSection `json:"groww,omitempty"`
	Kite       *kiteSection  `json:"kite,omitempty"`
}

type growwSection struct {
	Connected  bool             `json:"connected"`
	Error      string           `json:"error,omitempty"`
	Holdings   []growwHoldingOut `json:"holdings,omitempty"`
	TotalValue float64          `json:"total_value,omitempty"`
	TotalPnL   float64          `json:"total_pnl,omitempty"`
}

type growwHoldingOut struct {
	Symbol       string  `json:"symbol"`
	Title        string  `json:"title"`
	Quantity     float64 `json:"quantity"`
	AvgPrice     float64 `json:"avg_price"`
	CurrentPrice float64 `json:"current_price"`
	PnL          float64 `json:"pnl"`
	PnLPercent   float64 `json:"pnl_percent"`
}

type kiteSection struct {
	Connected    bool              `json:"connected"`
	Error        string            `json:"error,omitempty"`
	Holdings     []kiteHoldingOut  `json:"holdings,omitempty"`
	MFHoldings   []kiteMFHoldingOut `json:"mf_holdings,omitempty"`
	TotalValue   float64           `json:"total_value,omitempty"`
	TotalPnL     float64           `json:"total_pnl,omitempty"`
}

type kiteHoldingOut struct {
	Symbol       string  `json:"symbol"`
	Quantity     float64 `json:"quantity"`
	AvgPrice     float64 `json:"avg_price"`
	LastPrice    float64 `json:"last_price"`
	PnL          float64 `json:"pnl"`
	DayChangePct float64 `json:"day_change_pct"`
}

type kiteMFHoldingOut struct {
	SchemeName  string  `json:"scheme_name"`
	Quantity    float64 `json:"quantity"`
	AvgPrice    float64 `json:"avg_price"`
	LastPrice   float64 `json:"last_price"`
	PnL         float64 `json:"pnl"`
	Current     float64 `json:"current_value"`
}

func runHoldings(cmd *cobra.Command, args []string) error {
	cfg := resolveConfig()
	broker := holdingsBrokerFlag

	// Determine which brokers to query.
	fetchGroww := broker == "" || broker == "groww"
	fetchKite := broker == "" || broker == "kite"
	if broker != "" && !fetchGroww && !fetchKite {
		return fmt.Errorf("unknown broker %q — use 'groww' or 'kite'", broker)
	}

	ctx := cmd.Context()

	// Fetch in sequence — these are quick MCP calls, no need for concurrency.
	result := &brokerHoldings{}

	if fetchGroww {
		result.Groww = fetchGrowwHoldings(ctx, cfg)
	}
	if fetchKite {
		result.Kite = fetchKiteHoldings(ctx, cfg)
	}

	if jsonOut {
		printJSON(result)
		return nil
	}

	// Text output
	fmt.Println()

	anyConnected := false

	if result.Groww != nil {
		anyConnected = printGrowwText(result.Groww) || anyConnected
	}
	if result.Kite != nil {
		anyConnected = printKiteText(result.Kite) || anyConnected
	}

	if !anyConnected {
		fmt.Println("  No brokers connected. Start the server (cresto start) and connect via the web UI.")
	}
	fmt.Println()
	return nil
}

func fetchGrowwHoldings(ctx context.Context, cfg config.Config) *growwSection {
	client := groww.New(cfg.GrowwTokenPath)
	section := &growwSection{Connected: client.Connected()}

	if !section.Connected {
		return section
	}

	result, err := client.Holdings(ctx)
	if err != nil {
		section.Error = err.Error()
		if isGrowwNotConnected(err) {
			section.Connected = false
		}
		return section
	}

	for _, h := range result.Holdings {
		section.Holdings = append(section.Holdings, growwHoldingOut{
			Symbol:       h.DisplaySymbol(),
			Title:        h.Title,
			Quantity:     h.Quantity,
			AvgPrice:     h.AvgPrice,
			CurrentPrice: h.CurrentPrice(),
			PnL:          h.PnL,
			PnLPercent:   h.PnLPercent,
		})
		section.TotalValue += h.CurrentValue
		section.TotalPnL += h.PnL
	}
	return section
}

func fetchKiteHoldings(ctx context.Context, cfg config.Config) *kiteSection {
	client := kite.New(cfg.KiteSessionPath)
	section := &kiteSection{Connected: client.Connected()}

	if !section.Connected {
		return section
	}

	result, err := client.Holdings(ctx)
	if err != nil {
		section.Error = err.Error()
		if isKiteNotConnected(err) {
			section.Connected = false
		}
		return section
	}

	for _, h := range result.Holdings {
		section.Holdings = append(section.Holdings, kiteHoldingOut{
			Symbol:       h.TradingSymbol,
			Quantity:     h.Quantity,
			AvgPrice:     h.AveragePrice,
			LastPrice:    h.LastPrice,
			PnL:          h.PnL,
			DayChangePct: h.DayChangePct,
		})
		section.TotalValue += h.LastPrice * h.Quantity
		section.TotalPnL += h.PnL
	}

	mfResult, err := client.MFHoldings(ctx)
	if err == nil {
		for _, h := range mfResult.Holdings {
			section.MFHoldings = append(section.MFHoldings, kiteMFHoldingOut{
				SchemeName: h.SchemeName,
				Quantity:   h.Quantity,
				AvgPrice:   h.AveragePrice,
				LastPrice:  h.LastPrice,
				PnL:        h.PnL,
				Current:    h.CurrentValue,
			})
		}
	}

	return section
}

func printGrowwText(s *growwSection) bool {
	fmt.Printf("  Groww\n")

	if !s.Connected {
		fmt.Println("    Not connected. Connect via the web UI at /groww.")
		fmt.Println()
		return false
	}

	if s.Error != "" {
		fmt.Printf("    Error: %s\n", s.Error)
		fmt.Println()
		return true
	}

	if len(s.Holdings) == 0 {
		fmt.Println("    No holdings.")
		fmt.Println()
		return true
	}

	rows := make([][]string, 0, len(s.Holdings))
	for _, h := range s.Holdings {
		name := h.Symbol
		if h.Title != "" {
			name = h.Title
		}
		rows = append(rows, []string{
			name,
			fmt.Sprintf("%.0f", h.Quantity),
			moneyPlain(h.AvgPrice),
			moneyPlain(h.CurrentPrice),
			fmtSign(h.PnL) + moneyPlain(h.PnL),
			fmt.Sprintf("%s%.2f%%", fmtSign(h.PnLPercent), h.PnLPercent),
		})
	}
	fmt.Println(formatTable([]string{"Stock", "Qty", "Avg", "Current", "P&L", "P&L %"}, rows))
	fmt.Printf("    Total value: %s  P&L: %s%s\n", moneyPlain(s.TotalValue), fmtSign(s.TotalPnL), moneyPlain(s.TotalPnL))
	fmt.Println()
	return true
}

func printKiteText(s *kiteSection) bool {
	fmt.Printf("  Kite\n")

	if !s.Connected {
		fmt.Println("    Not connected. Connect via the web UI at /kite.")
		fmt.Println()
		return false
	}

	if s.Error != "" {
		fmt.Printf("    Error: %s\n", s.Error)
		fmt.Println()
		return true
	}

	if len(s.Holdings) > 0 {
		rows := make([][]string, 0, len(s.Holdings))
		for _, h := range s.Holdings {
			rows = append(rows, []string{
				h.Symbol,
				fmt.Sprintf("%.0f", h.Quantity),
				moneyPlain(h.AvgPrice),
				moneyPlain(h.LastPrice),
				fmtSign(h.PnL) + moneyPlain(h.PnL),
				fmt.Sprintf("%s%.2f%%", fmtSign(h.DayChangePct), h.DayChangePct),
			})
		}
		fmt.Println(formatTable([]string{"Stock", "Qty", "Avg", "LTP", "P&L", "Day %"}, rows))
		fmt.Printf("    Total value: %s  P&L: %s%s\n", moneyPlain(s.TotalValue), fmtSign(s.TotalPnL), moneyPlain(s.TotalPnL))
	} else {
		fmt.Println("    No equity holdings.")
	}

	if len(s.MFHoldings) > 0 {
		fmt.Println()
		fmt.Println("    Mutual Funds:")
		rows := make([][]string, 0, len(s.MFHoldings))
		for _, h := range s.MFHoldings {
			rows = append(rows, []string{
				h.SchemeName,
				fmt.Sprintf("%.2f", h.Quantity),
				moneyPlain(h.AvgPrice),
				moneyPlain(h.Current),
				fmtSign(h.PnL) + moneyPlain(h.PnL),
			})
		}
		fmt.Println(formatTable([]string{"Scheme", "Qty", "Avg", "Current", "P&L"}, rows))
	}

	fmt.Println()
	return true
}

func isGrowwNotConnected(err error) bool {
	return errors.Is(err, groww.ErrNotConnected)
}

func isKiteNotConnected(err error) bool {
	return errors.Is(err, kite.ErrNotConnected)
}

func fmtSign(v float64) string {
	if v < 0 {
		return ""
	}
	return "+"
}
