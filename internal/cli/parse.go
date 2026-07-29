package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"cresto/internal/llm"
	"cresto/internal/render"
)

var parseCmd = &cobra.Command{
	Use:   "parse <pdf>",
	Short: "Render + extract structured JSON from a payslip via LLM",
	Args:  cobra.ExactArgs(1),
	Long: `Render a PDF payslip to an image and extract structured data
using the configured LM Studio LLM.

The result is printed as JSON to stdout.

Example:
  cresto parse ~/Downloads/payslip.pdf`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := resolveConfig()
		pdfPath := args[0]

		img, err := render.Render(pdfPath)
		if err != nil {
			return fmt.Errorf("render error: %w", err)
		}

		client := llm.NewClient(cfg.LMStudioBaseURL, cfg.ModelName)
		fmt.Fprintf(os.Stderr, "parsing %s via %s (%d bytes)...\n", pdfPath, cfg.ModelName, len(img))
		ext, err := client.Extract(img)
		if err != nil {
			return fmt.Errorf("extract error: %w", err)
		}

		out, err := json.MarshalIndent(ext, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal error: %w", err)
		}
		fmt.Println(string(out))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(parseCmd)
}
