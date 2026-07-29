package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"cresto/internal/config"
	"cresto/internal/groww"
	"cresto/internal/kite"
	"cresto/internal/mcp"

	"github.com/spf13/cobra"
)

var quoteExchange string
var quoteBroker string

var quoteCmd = &cobra.Command{
	Use:   "quote SYMBOL [SYMBOL...]",
	Short: "Get live price quotes for stocks",
	Long: `Fetch the latest traded price (LTP) for one or more stock symbols
from a connected broker's MCP. No database or running server required.

Symbols are exchange tradingsymbols (e.g. RELIANCE, INFY, SBIN).
The exchange defaults to NSE; use --exchange to specify BSE.

Examples:
  cresto quote RELIANCE
  cresto quote RELIANCE INFY SBIN --json
  cresto quote TCS --exchange BSE
  cresto quote RELIANCE --broker kite`,
	Args: cobra.MinimumNArgs(1),
	RunE: runQuote,
}

func init() {
	rootCmd.AddCommand(quoteCmd)
	quoteCmd.Flags().StringVar(&quoteExchange, "exchange", "NSE", "Exchange: NSE or BSE")
	quoteCmd.Flags().StringVar(&quoteBroker, "broker", "", "Broker to use: groww or kite (auto-selects if empty)")
}

// quoteResult is the JSON shape for --json output.
type quoteResult struct {
	Broker   string       `json:"broker"`
	Exchange string       `json:"exchange"`
	Quotes   []quoteEntry `json:"quotes"`
}

type quoteEntry struct {
	Symbol    string  `json:"symbol"`
	LTP       float64 `json:"ltp"`
	Open      float64 `json:"open,omitempty"`
	High      float64 `json:"high,omitempty"`
	Low       float64 `json:"low,omitempty"`
	Close     float64 `json:"close,omitempty"`
	Change    float64 `json:"change,omitempty"`
	ChangePct float64 `json:"change_pct,omitempty"`
	Error     string  `json:"error,omitempty"`
}

func runQuote(cmd *cobra.Command, args []string) error {
	cfg := resolveConfig()
	ctx := cmd.Context()

	symbols := args
	exchange := strings.ToUpper(quoteExchange)
	if exchange != "NSE" && exchange != "BSE" {
		return fmt.Errorf("invalid exchange %q — use NSE or BSE", quoteExchange)
	}

	// Build the instrument list in exchange:symbol format.
	instruments := make([]string, len(symbols))
	for i, s := range symbols {
		instruments[i] = exchange + ":" + strings.ToUpper(s)
	}

	// Determine which broker to use.
	broker := quoteBroker
	if broker == "" {
		broker = autoSelectBroker(cfg)
	}
	if broker == "" {
		return fmt.Errorf("no broker connected — start the server (cresto start) and connect via the web UI")
	}

	var result *quoteResult
	var err error

	switch broker {
	case "groww":
		result, err = fetchGrowwQuote(ctx, cfg, instruments, exchange)
	case "kite":
		result, err = fetchKiteQuote(ctx, cfg, instruments, exchange)
	default:
		return fmt.Errorf("unknown broker %q — use 'groww' or 'kite'", broker)
	}

	if err != nil {
		return formatError("failed to fetch quote", err)
	}

	if jsonOut {
		printJSON(result)
		return nil
	}

	// Text output
	fmt.Println()
	if len(result.Quotes) == 0 {
		fmt.Println("  No quotes returned.")
		fmt.Println()
		return nil
	}

	rows := make([][]string, 0, len(result.Quotes))
	for _, q := range result.Quotes {
		row := []string{q.Symbol, fmt.Sprintf("%.2f", q.LTP)}
		if q.Change != 0 || q.ChangePct != 0 {
			row = append(row, fmt.Sprintf("%s%.2f", fmtSign(q.Change), q.Change), fmt.Sprintf("%s%.2f%%", fmtSign(q.ChangePct), q.ChangePct))
		} else {
			row = append(row, "-", "-")
		}
		if q.Error != "" {
			row = append(row, q.Error)
		}
		rows = append(rows, row)
	}

	headers := []string{"Symbol", "LTP", "Change", "Change %"}
	if len(symbols) == 1 && result.Quotes[0].Error != "" {
		headers = append(headers, "Error")
	}
	fmt.Println(formatTable(headers, rows))
	fmt.Printf("  via %s · %s\n", result.Broker, result.Exchange)
	fmt.Println()
	return nil
}

// autoSelectBroker returns the first connected broker, preferring Kite
// (richer quote data with OHLC) over Groww.
func autoSelectBroker(cfg config.Config) string {
	kiteClient := kite.New(cfg.KiteSessionPath)
	if kiteClient.Connected() {
		return "kite"
	}
	growwClient := groww.New(cfg.GrowwTokenPath)
	if growwClient.Connected() {
		return "groww"
	}
	return ""
}

// fetchKiteQuote calls Kite's get_ltp tool via the MCP.
func fetchKiteQuote(ctx context.Context, cfg config.Config, instruments []string, exchange string) (*quoteResult, error) {
	sess, err := loadKiteSession(cfg)
	if err != nil {
		return nil, kite.ErrNotConnected
	}

	client := mcp.New("https://mcp.kite.trade/mcp", mcp.WithProtocolVersion("2025-03-26"))
	headers := map[string]string{"Mcp-Session-Id": sess}

	// Try get_quotes first (richer data: OHLC + change).
	raw, err := client.CallTool(ctx, headers, "get_quotes", map[string]interface{}{
		"instruments": instruments,
	})
	if err != nil {
		if errors.Is(err, mcp.ErrUnauthorized) {
			return nil, kite.ErrNotConnected
		}
		// Fall back to get_ltp if get_quotes fails.
		raw, err = client.CallTool(ctx, headers, "get_ltp", map[string]interface{}{
			"instruments": instruments,
		})
		if err != nil {
			return nil, fmt.Errorf("kite quote: %w", err)
		}
	}

	result := &quoteResult{Broker: "kite", Exchange: exchange, Quotes: parseQuoteRaw(raw, instruments)}
	return result, nil
}

// fetchGrowwQuote calls Groww's get_ltp tool via the MCP. Groww uses a
// search-based interface (search_queries + segment + query_type), not the
// exact exchange:symbol format Kite uses. We pass the raw symbol names as
// search queries with segment=CASH and query_type=Stocks.
func fetchGrowwQuote(ctx context.Context, cfg config.Config, instruments []string, exchange string) (*quoteResult, error) {
	tok, err := loadGrowwToken(cfg)
	if err != nil {
		return nil, groww.ErrNotConnected
	}

	client := mcp.New("https://mcp.groww.in/mcp/")

	init, err := client.Initialize(ctx, map[string]string{"Authorization": "Bearer " + tok})
	if err != nil {
		if errors.Is(err, mcp.ErrUnauthorized) {
			return nil, groww.ErrNotConnected
		}
		return nil, fmt.Errorf("groww initialize: %w", err)
	}

	headers := map[string]string{"Authorization": "Bearer " + tok}
	if init.SessionID != "" {
		headers["Mcp-Session-Id"] = init.SessionID
	}

	// Extract raw symbol names from exchange:symbol format.
	searchQueries := make([]string, len(instruments))
	for i, inst := range instruments {
		parts := strings.SplitN(inst, ":", 2)
		if len(parts) == 2 {
			searchQueries[i] = parts[1]
		} else {
			searchQueries[i] = inst
		}
	}

	raw, err := client.CallTool(ctx, headers, "get_ltp", map[string]interface{}{
		"search_queries": searchQueries,
		"segment":        "CASH",
		"query_type":     "Stocks",
	})
	if err != nil {
		if errors.Is(err, mcp.ErrUnauthorized) {
			return nil, groww.ErrNotConnected
		}
		return nil, fmt.Errorf("groww quote: %w", err)
	}

	result := &quoteResult{Broker: "groww", Exchange: exchange, Quotes: parseQuoteRaw(raw, instruments)}
	return result, nil
}

// parseQuoteRaw parses the MCP tool response into quote entries. The
// response shape varies by broker:
//   - Kite: {"NSE:RELIANCE": {"last_price": 1275, "ohlc": {...}, ...}, ...}
//   - Groww: array or wrapped array
// We try common shapes and fall back to error entries per symbol.
func parseQuoteRaw(rawText string, instruments []string) []quoteEntry {
	trimmed := strings.TrimSpace(rawText)
	if trimmed == "" {
		return makeQuoteErrors(instruments, "empty response")
	}

	// Try as a JSON array of quote objects.
	var quotes []quoteEntry
	if err := json.Unmarshal([]byte(trimmed), &quotes); err == nil && len(quotes) > 0 {
		return matchQuotesToInstruments(quotes, instruments)
	}

	// Try as a JSON object keyed by instrument (Kite's shape).
	var objMap map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &objMap); err == nil {
		// Kite: {"NSE:RELIANCE": {"last_price": ..., "ohlc": {...}}}
		entries := make([]quoteEntry, 0, len(instruments))
		for _, inst := range instruments {
			sym := stripExchange(inst)
			// Try exact key match, then symbol-only match.
			raw, ok := objMap[inst]
			if !ok {
				// Try case variations.
				for k, v := range objMap {
					if strings.EqualFold(k, inst) || strings.EqualFold(stripExchange(k), sym) {
						raw = v
						ok = true
						break
					}
				}
			}
			if !ok {
				entries = append(entries, quoteEntry{Symbol: sym, Error: "not found"})
				continue
			}

			entry, err := parseKiteQuoteObject(raw, sym)
			if err != nil {
				entries = append(entries, quoteEntry{Symbol: sym, Error: err.Error()})
			} else {
				entries = append(entries, entry)
			}
		}
		if len(entries) > 0 {
			return entries
		}

		// Try wrapped array: {"data": [...]} / {"result": [...]}
		for _, key := range []string{"data", "result", "quotes", "ltp"} {
			if inner, ok := objMap[key]; ok {
				if err := json.Unmarshal(inner, &quotes); err == nil && len(quotes) > 0 {
					return matchQuotesToInstruments(quotes, instruments)
				}
			}
		}
	}

	return makeQuoteErrors(instruments, "could not parse response: "+truncate(rawText, 100))
}

// parseKiteQuoteObject parses a single Kite quote object (the value side
// of {"NSE:RELIANCE": {...}}). Kite returns last_price, ohlc, net_change,
// last_trade_time, etc. When net_change is absent, we derive it from
// last_price - ohlc.close.
func parseKiteQuoteObject(raw json.RawMessage, symbol string) (quoteEntry, error) {
	var kq struct {
		LastPrice  float64 `json:"last_price"`
		NetChange  float64 `json:"net_change"`
		OHLC       struct {
			Open  float64 `json:"open"`
			High  float64 `json:"high"`
			Low   float64 `json:"low"`
			Close float64 `json:"close"`
		} `json:"ohlc"`
		LastTradeTime string `json:"last_trade_time"`
	}
	if err := json.Unmarshal(raw, &kq); err != nil {
		return quoteEntry{}, fmt.Errorf("parse quote: %w", err)
	}

	change := kq.NetChange
	if change == 0 && kq.OHLC.Close > 0 {
		change = kq.LastPrice - kq.OHLC.Close
	}
	changePct := 0.0
	if kq.OHLC.Close > 0 {
		changePct = change / kq.OHLC.Close * 100
	}

	return quoteEntry{
		Symbol:    symbol,
		LTP:       kq.LastPrice,
		Open:      kq.OHLC.Open,
		High:      kq.OHLC.High,
		Low:       kq.OHLC.Low,
		Close:     kq.OHLC.Close,
		Change:    change,
		ChangePct: changePct,
	}, nil
}

// matchQuotesToInstruments maps parsed quote entries to the requested
// instruments by symbol name. Used when the response is an array.
func matchQuotesToInstruments(quotes []quoteEntry, instruments []string) []quoteEntry {
	if len(quotes) == len(instruments) {
		// Assume 1:1 ordering — fill in symbol names from instruments.
		for i := range quotes {
			if quotes[i].Symbol == "" {
				quotes[i].Symbol = stripExchange(instruments[i])
			}
		}
		return quotes
	}
	// Best-effort: return as-is, caller will see what matched.
	return quotes
}

func stripExchange(inst string) string {
	parts := strings.SplitN(inst, ":", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return inst
}

func makeQuoteErrors(instruments []string, msg string) []quoteEntry {
	entries := make([]quoteEntry, len(instruments))
	for i, inst := range instruments {
		parts := strings.SplitN(inst, ":", 2)
		sym := inst
		if len(parts) == 2 {
			sym = parts[1]
		}
		entries[i] = quoteEntry{Symbol: sym, Error: msg}
	}
	return entries
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// loadKiteSession reads the Kite session file.
func loadKiteSession(cfg config.Config) (string, error) {
	data, err := readFile(cfg.KiteSessionPath)
	if err != nil {
		return "", err
	}
	var sess struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(data, &sess); err != nil {
		return "", fmt.Errorf("parse session: %w", err)
	}
	if sess.SessionID == "" {
		return "", fmt.Errorf("empty session ID")
	}
	return sess.SessionID, nil
}

// loadGrowwToken reads the Groww token file.
func loadGrowwToken(cfg config.Config) (string, error) {
	data, err := readFile(cfg.GrowwTokenPath)
	if err != nil {
		return "", err
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(data, &tok); err != nil {
		return "", fmt.Errorf("parse token: %w", err)
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("empty access token")
	}
	return tok.AccessToken, nil
}

func readFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return data, nil
}
