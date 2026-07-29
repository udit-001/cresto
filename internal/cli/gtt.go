package cli

import (
	"fmt"
	"strconv"
	"strings"

	"cresto/internal/kite"

	"github.com/spf13/cobra"
)

var gttCmd = &cobra.Command{
	Use:   "gtt",
	Short: "Manage Good Till Triggered orders on Kite",
	Long: `View, cancel, and place GTT orders via Kite's MCP.
GTT orders stay active until triggered or cancelled — they don't
expire at end of day.

GTT is Kite-only. No --broker flag.

Examples:
  cresto gtt list
  cresto gtt cancel 123456
  cresto gtt place RELIANCE --type single --trigger 3200 --qty 10
  cresto gtt place TCS --type oco --lower 2800 --upper 3200 --qty 5`,
}

var gttListCmd = &cobra.Command{
	Use:   "list",
	Short: "List active GTT orders",
	Long: `List active GTT orders from Kite. Shows trigger ID, symbol, type
(single/OCO), trigger price(s), quantity, status, and created date.

JSON output (--json) returns: {"gtts": [...]} where each GTT has:
trigger_id, tradingsymbol, exchange, type, status, transaction_type,
quantity, trigger_price (single), lower_trigger + upper_trigger (OCO),
created_at. An empty array means no active orders.

Use 'cresto gtt cancel <trigger_id>' to remove an order.

Example:
  cresto gtt list
  cresto gtt list --json`,
	Args: cobra.NoArgs,
	RunE:  runGTTList,
}

var gttCancelCmd = &cobra.Command{
	Use:   "cancel <trigger_id>",
	Short: "Cancel a GTT order by its trigger ID",
	Long: `Cancel a GTT order by its trigger ID. The ID comes from
'cresto gtt list'. Cancellation is irreversible.

JSON output (--json) returns: trigger_id, cancelled (bool), raw (string).

Example:
  cresto gtt cancel 123456
  cresto gtt cancel 123456 --json`,
	Args: cobra.ExactArgs(1),
	RunE:  runGTTCancel,
}

var gttPlaceType string
var gttPlaceTrigger float64
var gttPlaceLower float64
var gttPlaceUpper float64
var gttPlaceQty float64
var gttPlaceExchange string
var gttPlaceProduct string

var gttPlaceCmd = &cobra.Command{
	Use:   "place SYMBOL",
	Short: "Place a GTT order (Cresto's first broker write)",
	Long: `Place a Good Till Triggered order via Kite's MCP. The last_price
is auto-fetched — the agent passes only the trigger decision.

Transaction type defaults to SELL (wealth protection).
Limit price defaults to the trigger price.

No --dry-run, no confirmation prompt. Verify with 'gtt list'
afterwards and cancel with 'gtt cancel' if needed.

Examples:
  cresto gtt place RELIANCE --type single --trigger 3200 --qty 10
  cresto gtt place TCS --type oco --lower 2800 --upper 3200 --qty 5
  cresto gtt place INFY --type single --trigger 1500 --qty 20 --exchange BSE`,
	Args: cobra.ExactArgs(1),
	RunE: runGTTPlace,
}

func init() {
	rootCmd.AddCommand(gttCmd)
	gttCmd.AddCommand(gttListCmd)
	gttCmd.AddCommand(gttCancelCmd)
	gttCmd.AddCommand(gttPlaceCmd)

	gttPlaceCmd.Flags().StringVar(&gttPlaceType, "type", "", "GTT type: single or oco (required)")
	gttPlaceCmd.Flags().Float64Var(&gttPlaceTrigger, "trigger", 0, "Trigger price (for single)")
	gttPlaceCmd.Flags().Float64Var(&gttPlaceLower, "lower", 0, "Lower trigger price (for oco)")
	gttPlaceCmd.Flags().Float64Var(&gttPlaceUpper, "upper", 0, "Upper trigger price (for oco)")
	gttPlaceCmd.Flags().Float64Var(&gttPlaceQty, "qty", 0, "Quantity to sell (required)")
	gttPlaceCmd.Flags().StringVar(&gttPlaceExchange, "exchange", "NSE", "Exchange: NSE or BSE")
	gttPlaceCmd.Flags().StringVar(&gttPlaceProduct, "product", "CNC", "Product: CNC (delivery) or MIS (intraday)")
}

type gttOut struct {
	TriggerID       int     `json:"trigger_id"`
	TradingSymbol   string  `json:"tradingsymbol"`
	Exchange        string  `json:"exchange"`
	Type            string  `json:"type"`
	Status          string  `json:"status"`
	TransactionType string  `json:"transaction_type"`
	Quantity        float64 `json:"quantity"`
	TriggerPrice    float64 `json:"trigger_price,omitempty"`
	LowerTrigger    float64 `json:"lower_trigger,omitempty"`
	UpperTrigger    float64 `json:"upper_trigger,omitempty"`
	CreatedAt       string  `json:"created_at"`
}

type gttListResult struct {
	GTTs []gttOut `json:"gtts"`
}

func runGTTList(cmd *cobra.Command, args []string) error {
	cfg := resolveConfig()
	ctx := cmd.Context()

	client := kite.New(cfg.KiteSessionPath)
	if !client.Connected() {
		return fmt.Errorf("kite not connected — start the server (cresto start) and connect via the web UI at /kite")
	}

	result, err := client.ListGTTs(ctx)
	if err != nil {
		return formatError("failed to fetch GTT orders", err)
	}

	if jsonOut {
		outs := make([]gttOut, 0, len(result.GTTs))
		for _, g := range result.GTTs {
			outs = append(outs, toGTTOut(g))
		}
		printJSON(gttListResult{GTTs: outs})
		return nil
	}

	fmt.Println()

	if len(result.GTTs) == 0 {
		fmt.Println("  No active GTT orders.")
		fmt.Println()
		return nil
	}

	rows := make([][]string, 0, len(result.GTTs))
	for _, g := range result.GTTs {
		gtType := g.Type
		if g.Type == "two-leg" {
			gtType = "OCO"
		}

		trigger := fmt.Sprintf("%.2f", g.TriggerPrice)
		if g.Type == "two-leg" && len(g.Orders) == 2 {
			trigger = fmt.Sprintf("%.2f / %.2f", g.Orders[0].TriggerPrice, g.Orders[1].TriggerPrice)
		}

		created := g.CreatedAt
		if len(created) > 10 {
			created = created[:10]
		}

		rows = append(rows, []string{
			strconv.Itoa(g.TriggerID),
			g.TradingSymbol,
			g.Exchange,
			gtType,
			trigger,
			fmt.Sprintf("%.0f", g.Quantity),
			g.Status,
			created,
		})
	}

	fmt.Println(formatTable([]string{"ID", "Symbol", "Exch", "Type", "Trigger", "Qty", "Status", "Created"}, rows))
	fmt.Println()
	return nil
}

func runGTTCancel(cmd *cobra.Command, args []string) error {
	cfg := resolveConfig()
	ctx := cmd.Context()

	triggerID, err := strconv.Atoi(args[0])
	if err != nil {
		return fmt.Errorf("invalid trigger ID %q — must be a number", args[0])
	}

	client := kite.New(cfg.KiteSessionPath)
	if !client.Connected() {
		return fmt.Errorf("kite not connected — start the server (cresto start) and connect via the web UI at /kite")
	}

	raw, err := client.CancelGTT(ctx, triggerID)
	if err != nil {
		return formatError("failed to cancel GTT", err)
	}

	if jsonOut {
		printJSON(map[string]any{
			"trigger_id": triggerID,
			"cancelled":  true,
			"raw":        truncate(raw, 200),
		})
		return nil
	}

	fmt.Printf("  Cancelled GTT #%d.\n", triggerID)
	return nil
}

func toGTTOut(g kite.GTT) gttOut {
	out := gttOut{
		TriggerID:       g.TriggerID,
		TradingSymbol:   g.TradingSymbol,
		Exchange:        g.Exchange,
		Type:            g.Type,
		Status:          g.Status,
		TransactionType: g.TransactionType,
		Quantity:        g.Quantity,
		TriggerPrice:    g.TriggerPrice,
		CreatedAt:       g.CreatedAt,
	}
	if g.Type == "two-leg" {
		out.Type = "oco"
		if len(g.Orders) == 2 {
			out.LowerTrigger = g.Orders[0].TriggerPrice
			out.UpperTrigger = g.Orders[1].TriggerPrice
		}
	}
	return out
}

type gttPlaceResult struct {
	TriggerID       int     `json:"trigger_id"`
	Symbol          string  `json:"symbol"`
	Type            string  `json:"type"`
	TransactionType string  `json:"transaction_type"`
	Quantity        float64 `json:"quantity"`
	TriggerPrice    float64 `json:"trigger_price,omitempty"`
	LowerTrigger    float64 `json:"lower_trigger,omitempty"`
	UpperTrigger    float64 `json:"upper_trigger,omitempty"`
	LastPrice       float64 `json:"last_price"`
	Exchange        string  `json:"exchange"`
	Product         string  `json:"product"`
	Note            string  `json:"note,omitempty"`
}

func runGTTPlace(cmd *cobra.Command, args []string) error {
	cfg := resolveConfig()
	ctx := cmd.Context()

	symbol := strings.ToUpper(args[0])
	exchange := strings.ToUpper(gttPlaceExchange)

	if exchange != "NSE" && exchange != "BSE" {
		return fmt.Errorf("invalid exchange %q — use NSE or BSE", gttPlaceExchange)
	}

	gttType := strings.ToLower(gttPlaceType)
	if gttType != "single" && gttType != "oco" {
		return fmt.Errorf("--type must be 'single' or 'oco'")
	}

	if gttPlaceQty <= 0 {
		return fmt.Errorf("--qty must be greater than 0")
	}

	if gttType == "single" && gttPlaceTrigger <= 0 {
		return fmt.Errorf("--trigger is required and must be > 0 for type 'single'")
	}

	if gttType == "oco" {
		if gttPlaceLower <= 0 {
			return fmt.Errorf("--lower is required and must be > 0 for type 'oco'")
		}
		if gttPlaceUpper <= 0 {
			return fmt.Errorf("--upper is required and must be > 0 for type 'oco'")
		}
		if gttPlaceLower >= gttPlaceUpper {
			return fmt.Errorf("--lower must be less than --upper")
		}
	}

	client := kite.New(cfg.KiteSessionPath)
	if !client.Connected() {
		return fmt.Errorf("kite not connected — start the server (cresto start) and connect via the web UI at /kite")
	}

	lastPrice, err := client.GetLTP(ctx, exchange, symbol)
	if err != nil {
		return formatError("failed to fetch last price for "+symbol, err)
	}

	triggerType := gttType
	if gttType == "oco" {
		triggerType = "two-leg"
	}

	params := kite.PlaceGTTParams{
		TradingSymbol:   symbol,
		Exchange:        exchange,
		LastPrice:       lastPrice,
		TransactionType: "SELL",
		TriggerType:    triggerType,
		Product:         strings.ToUpper(gttPlaceProduct),
	}

	if gttType == "single" {
		params.TriggerValue = gttPlaceTrigger
		params.Quantity = gttPlaceQty
		params.LimitPrice = gttPlaceTrigger
	} else {
		params.LowerTriggerValue = gttPlaceLower
		params.LowerQuantity = gttPlaceQty
		params.LowerLimitPrice = gttPlaceLower
		params.UpperTriggerValue = gttPlaceUpper
		params.UpperQuantity = gttPlaceQty
		params.UpperLimitPrice = gttPlaceUpper
	}

	result, err := client.PlaceGTT(ctx, params)
	if err != nil {
		return formatError("failed to place GTT order", err)
	}

	triggerID := result.TriggerID

	placeResult := gttPlaceResult{
		TriggerID:       triggerID,
		Symbol:          symbol,
		Type:            gttType,
		TransactionType: "SELL",
		Quantity:        gttPlaceQty,
		LastPrice:       lastPrice,
		Exchange:        exchange,
		Product:         strings.ToUpper(gttPlaceProduct),
	}

	if gttType == "single" {
		placeResult.TriggerPrice = gttPlaceTrigger
	} else {
		placeResult.LowerTrigger = gttPlaceLower
		placeResult.UpperTrigger = gttPlaceUpper
	}

	if triggerID == 0 {
		placeResult.Note = "trigger_id not parsed from broker response — verify with 'cresto gtt list'"
	}

	if jsonOut {
		printJSON(placeResult)
		return nil
	}

	fmt.Println()
	fmt.Printf("  Placed GTT order for %s.\n", symbol)
	fmt.Printf("  Type: %s (SELL)  Qty: %.0f  LTP: %.2f\n", gttType, gttPlaceQty, lastPrice)
	if gttType == "single" {
		fmt.Printf("  Trigger: %.2f  Exchange: %s  Product: %s\n", gttPlaceTrigger, exchange, strings.ToUpper(gttPlaceProduct))
	} else {
		fmt.Printf("  Lower: %.2f  Upper: %.2f  Exchange: %s  Product: %s\n", gttPlaceLower, gttPlaceUpper, exchange, strings.ToUpper(gttPlaceProduct))
	}
	if triggerID > 0 {
		fmt.Printf("  Trigger ID: %d\n", triggerID)
	} else {
		fmt.Printf("  Trigger ID: unknown — verify with 'cresto gtt list'\n")
	}
	fmt.Println()
	return nil
}
