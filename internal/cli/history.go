package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cresto/internal/kite"

	"github.com/spf13/cobra"
)

var historyInterval string
var historyFrom string
var historyTo string

var validIntervals = map[string]bool{
	"minute": true, "3minute": true, "5minute": true,
	"10minute": true, "15minute": true, "30minute": true,
	"60minute": true, "day": true,
}

var historyCmd = &cobra.Command{
	Use:   "history SYMBOL",
	Short: "Fetch historical price data for a stock you hold",
	Long: `Fetch historical OHLCV candle data for a stock from Kite's MCP.
The symbol must be in your Kite holdings — the instrument token is
resolved from there automatically.

Use this to research price trends before placing GTT orders with
'cresto gtt place'. Symbols come from 'cresto holdings --broker kite'.

JSON output (--json) returns: symbol, interval, from, to, candles[]
where each candle has: date, open, high, low, close, volume.

Examples:
  cresto history RELIANCE --interval day --from 2026-01-01
  cresto history TCS --interval day --from 2026-01-01 --to 2026-06-30
  cresto history INFY --interval 5minute --from 2026-07-29 --json`,
	Args: cobra.ExactArgs(1),
	RunE: runHistory,
}

func init() {
	rootCmd.AddCommand(historyCmd)
	historyCmd.Flags().StringVar(&historyInterval, "interval", "day", "Candle interval: minute, 3minute, 5minute, 10minute, 15minute, 30minute, 60minute, day")
	historyCmd.Flags().StringVar(&historyFrom, "from", "", "Start date (YYYY-MM-DD, required)")
	historyCmd.Flags().StringVar(&historyTo, "to", "", "End date (YYYY-MM-DD, defaults to today)")
}

type historyCandleOut struct {
	Date   string  `json:"date"`
	Open   float64 `json:"open"`
	High   float64 `json:"high"`
	Low    float64 `json:"low"`
	Close  float64 `json:"close"`
	Volume float64 `json:"volume"`
}

type historyResult struct {
	Symbol   string             `json:"symbol"`
	Interval string             `json:"interval"`
	From     string             `json:"from"`
	To       string             `json:"to"`
	Candles  []historyCandleOut `json:"candles"`
}

func runHistory(cmd *cobra.Command, args []string) error {
	cfg := resolveConfig()
	ctx := cmd.Context()

	symbol := strings.ToUpper(args[0])

	if !validIntervals[historyInterval] {
		return fmt.Errorf("invalid interval %q — use one of: minute, 3minute, 5minute, 10minute, 15minute, 30minute, 60minute, day", historyInterval)
	}

	if historyFrom == "" {
		return fmt.Errorf("--from is required (YYYY-MM-DD)")
	}

	fromDate, err := time.Parse("2006-01-02", historyFrom)
	if err != nil {
		return fmt.Errorf("invalid --from date %q — use YYYY-MM-DD format", historyFrom)
	}

	toDate := time.Now()
	if historyTo != "" {
		toDate, err = time.Parse("2006-01-02", historyTo)
		if err != nil {
			return fmt.Errorf("invalid --to date %q — use YYYY-MM-DD format", historyTo)
		}
	}

	client := kite.New(cfg.KiteSessionPath)
	if !client.Connected() {
		return fmt.Errorf("kite not connected — start the server (cresto start) and connect via the web UI at /kite")
	}

	token, err := resolveInstrumentToken(ctx, client, symbol)
	if err != nil {
		return err
	}

	hist, err := client.HistoricalData(ctx, token, historyInterval, fromDate, toDate)
	if err != nil {
		return formatError("failed to fetch historical data", err)
	}

	candles := make([]historyCandleOut, len(hist.Candles))
	for i, c := range hist.Candles {
		candles[i] = historyCandleOut{
			Date:   c.Date,
			Open:   c.Open,
			High:   c.High,
			Low:    c.Low,
			Close:  c.Close,
			Volume: c.Volume,
		}
	}

	result := historyResult{
		Symbol:   symbol,
		Interval: historyInterval,
		From:     fromDate.Format("2006-01-02"),
		To:       toDate.Format("2006-01-02"),
		Candles:  candles,
	}

	if jsonOut {
		printJSON(result)
		return nil
	}

	fmt.Println()

	if len(candles) == 0 {
		fmt.Printf("  No candle data returned for %s.\n", symbol)
		fmt.Println()
		return nil
	}

	rows := make([][]string, 0, len(candles))
	for _, c := range candles {
		date := c.Date
		if len(date) > 10 {
			if t, err := time.Parse(time.RFC3339, date); err == nil {
				if t.Hour() == 0 && t.Minute() == 0 {
					date = t.Format("2006-01-02")
				} else {
					date = t.Format("01-02 15:04")
				}
			} else {
				date = date[:16]
			}
		}
		rows = append(rows, []string{
			date,
			fmt.Sprintf("%.2f", c.Open),
			fmt.Sprintf("%.2f", c.High),
			fmt.Sprintf("%.2f", c.Low),
			fmt.Sprintf("%.2f", c.Close),
			fmt.Sprintf("%.0f", c.Volume),
		})
	}

	fmt.Println(formatTable([]string{"Date", "Open", "High", "Low", "Close", "Volume"}, rows))
	fmt.Printf("  %s · %s interval · %d candles\n", symbol, historyInterval, len(candles))
	fmt.Println()
	return nil
}

// resolveInstrumentToken finds the Kite instrument token for a tradingsymbol
// by looking it up in the user's holdings.
func resolveInstrumentToken(ctx context.Context, client *kite.Client, symbol string) (int, error) {
	holdings, err := client.Holdings(ctx)
	if err != nil {
		return 0, formatError("failed to fetch holdings for instrument lookup", err)
	}

	for _, h := range holdings.Holdings {
		if strings.EqualFold(h.TradingSymbol, symbol) {
			if h.InstrumentToken == 0 {
				return 0, fmt.Errorf("%s found in holdings but instrument_token is missing from the Kite response", symbol)
			}
			return h.InstrumentToken, nil
		}
	}

	return 0, fmt.Errorf("%s not found in your Kite holdings — run 'cresto holdings --broker kite --json' to see available symbols", symbol)
}
