package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"cresto/internal/config"
	"cresto/internal/store"
)

var jsonOut bool

type ctxStore struct{}

func mustStore(cmd *cobra.Command) *store.Store {
	s, ok := cmd.Context().Value(ctxStore{}).(*store.Store)
	if !ok || s == nil {
		panic("store not available in context — command missing from PersistentPreRunE skip list?")
	}
	return s
}

var rootCmd = &cobra.Command{
	Use:   "cresto",
	Short: "Income tracker — server-rendered payslip management",
	Long: `A web-based tool for managing and analyzing payslips.

Upload PDF payslips, extract structured data via a local LLM
(LM Studio), review and confirm extracted values, and view
aggregated earnings across periods and employers.

Most data commands support --json for machine-readable output.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if isSkippedCommand(cmd) {
			return nil
		}
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("config error: %w", err)
		}
		dbPath := config.DBPath(cfg)
		s, err := store.Open(dbPath)
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		ctx := context.WithValue(cmd.Context(), ctxStore{}, s)
		cmd.SetContext(ctx)
		return nil
	},
	PersistentPostRunE: func(cmd *cobra.Command, args []string) error {
		if s, ok := cmd.Context().Value(ctxStore{}).(*store.Store); ok && s != nil {
			return s.Close()
		}
		return nil
	},
}

// isSkippedCommand returns true for commands that don't need the store:
// help/completion, parent-only commands (they just show help), and the
// operational commands that manage their own resources.
func isSkippedCommand(cmd *cobra.Command) bool {
	if cmd.Name() == "help" || cmd.Name() == "completion" {
		return true
	}
	// Parent commands (config, tailwind) and their children are skipped.
	skippedParents := map[string]bool{
		"config": true, "tailwind": true,
	}
	if skippedParents[cmd.Name()] {
		return true
	}
	if cmd.Parent() != nil && skippedParents[cmd.Parent().Name()] {
		return true
	}
	// Parent-only commands (has subcommands, no Run/RunE) just show help.
	if cmd.HasSubCommands() && cmd.RunE == nil && cmd.Run == nil {
		return true
	}
	// Leaf operational commands that manage their own resources.
	leafSkipped := map[string]bool{
		"start": true, "stop": true, "migrate": true,
		"parse": true, "render": true,
		"holdings": true, "quote": true,
	}
	return leafSkipped[cmd.Name()]
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	rootCmd.SilenceErrors = true
	rootCmd.SilenceUsage = true
}

func Execute() {
	cmd, err := rootCmd.ExecuteC()
	if err != nil {
		if jsonOut {
			printJSON(map[string]any{"error": err.Error()})
		} else {
			fmt.Fprintln(os.Stderr, cmd.ErrPrefix(), err.Error())
			fmt.Fprintln(os.Stderr, cmd.UsageString())
		}
		os.Exit(1)
	}
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

func resolveConfig() config.Config {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: couldn't load config: %v\n", err)
	}
	if cfg != nil {
		return *cfg
	}
	return config.Default()
}

func formatError(msg string, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %w", msg, err)
	}
	return fmt.Errorf("%s", msg)
}
