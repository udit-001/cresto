package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"cresto/internal/store"
)

var migrateCmd = &cobra.Command{
	Use:   "migrate <action>",
	Short: "Run schema migrations",
	Long: `Run goose schema migrations against the cresto database.

Actions:
  up      Apply all pending migrations
  down    Roll back the most recent migration
  status  Show applied/pending migration versions

Migrations also run automatically on 'start' startup. This command
exists for manual control.

Example:
  cresto migrate status
  cresto migrate up`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := resolveConfig()
		if err := store.MigrateAction(cfg.SQLitePath, args[0]); err != nil {
			return fmt.Errorf("migrate %s: %w", args[0], err)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(migrateCmd)
}
