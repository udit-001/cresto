package cli

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"
	"cresto/internal/config"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "View and manage cresto configuration",
	Args:  cobra.NoArgs,
	RunE:  runShowHelp,
	Long: `View and update cresto configuration.

The config file (cresto.toml) lives in your platform app config
directory (~/.config/cresto/ on Linux).

Examples:
  cresto config read               # Show current config
  cresto config set port 8080       # Change web UI port
  cresto config set data_dir ~/mydata  # Change data directory`,
}

var configReadCmd = &cobra.Command{
	Use:   "read",
	Short: "Read current configuration",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("config error: %w", err)
		}

		dataDir := config.DefaultDataDir()
		if cfg != nil && cfg.DataDir != "" {
			dataDir = cfg.DataDir
		}

		port := config.DefaultPort
		portLabel := fmt.Sprintf("%d (default)", port)
		if cfg != nil && cfg.Port != 0 {
			port = cfg.Port
			portLabel = fmt.Sprintf("%d", port)
		}

		lmURL := "http://localhost:1234/v1"
		if cfg != nil && cfg.LMStudioBaseURL != "" {
			lmURL = cfg.LMStudioBaseURL
		}

		model := "mistralai/ministral-3-3b"
		if cfg != nil && cfg.ModelName != "" {
			model = cfg.ModelName
		}

		fmt.Println()
		fmt.Printf("  Config file:     %s\n", config.Path())
		fmt.Printf("  lm_studio_base_url: %s\n", lmURL)
		fmt.Printf("  model_name:      %s\n", model)
		fmt.Printf("  data_dir:        %s\n", dataDir)
		fmt.Printf("  Database:        %s\n", config.DBPath(cfg))
		fmt.Printf("  PDFs:            %s\n", config.PDFsPath(cfg))
		fmt.Printf("  port:            %s\n", portLabel)
		fmt.Println()
		return nil
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a configuration value",
	Long: `Update a configuration key.

Supported keys:
  lm_studio_base_url  LM Studio API base URL (e.g. http://localhost:1234/v1)
  model_name          LLM model name (e.g. mistralai/ministral-3-3b)
  data_dir            Path to the data directory (DB + PDFs)
  port                HTTP server port (1-65535)

Values are saved to the config file. Run 'cresto config read'
to verify.

Examples:
  cresto config set port 8080
  cresto config set model_name mistralai/ministral-3-3b
  cresto config set data_dir ~/my-income-data`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		key := args[0]
		value := args[1]

		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("config error: %w", err)
		}
		if cfg == nil {
			cfg = &config.Config{}
		}

		switch key {
		case "data_dir":
			cfg.DataDir = value
		case "port":
			p, err := strconv.Atoi(value)
			if err != nil || p < 1 || p > 65535 {
				return fmt.Errorf("invalid value %q for port: use 1-65535", value)
			}
			cfg.Port = p
		case "lm_studio_base_url":
			cfg.LMStudioBaseURL = value
		case "model_name":
			cfg.ModelName = value
		default:
			return fmt.Errorf("unknown config key: %s\n\nsupported keys: lm_studio_base_url, model_name, data_dir, port", key)
		}

		if err := config.Save(cfg); err != nil {
			return fmt.Errorf("save config: %w", err)
		}

		if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
			return fmt.Errorf("create data directory: %w", err)
		}

		fmt.Println()
		fmt.Printf("  ✓ %s set to %s\n", key, value)
		fmt.Printf("    Config: %s\n", config.Path())
		fmt.Println()
		return nil
	},
}

func init() {
	rootCmd.AddCommand(configCmd)
	configCmd.AddCommand(configReadCmd)
	configCmd.AddCommand(configSetCmd)
}
