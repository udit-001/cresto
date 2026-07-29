package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"cresto/internal/render"
)

var renderCmd = &cobra.Command{
	Use:   "render <pdf>",
	Short: "Render a PDF payslip to PNG image",
	Args:  cobra.ExactArgs(1),
	Long: `Render a PDF payslip page to a PNG image using pdftoppm (poppler-utils).

The output is written to payslip.png in the current directory.

Example:
  cresto render ~/Downloads/payslip.pdf`,
	RunE: func(cmd *cobra.Command, args []string) error {
		pdfPath := args[0]
		img, err := render.Render(pdfPath)
		if err != nil {
			return err
		}
		outputPath := "payslip.png"
		if err := os.WriteFile(outputPath, img, 0644); err != nil {
			return fmt.Errorf("write image: %w", err)
		}
		fmt.Printf("rendered %s → %s (%d bytes)\n", pdfPath, outputPath, len(img))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(renderCmd)
}
