package cli

import (
	"path/filepath"

	"github.com/spf13/cobra"
	"cresto/internal/tailwind"
)

var tailwindFlags struct {
	force bool
}

var tailwindCmd = &cobra.Command{
	Use:   "tailwind",
	Short: "Manage the Tailwind CSS CLI binary",
	Args:  cobra.NoArgs,
	RunE:  runShowHelp,
	Long: `Download and manage the Tailwind CSS v4 standalone CLI binary.

The binary is placed at .bin/tailwindcss in the project root.
Use 'cresto tailwind download' to download it for your platform.`,
}

var tailwindDownloadCmd = &cobra.Command{
	Use:   "download",
	Short: "Download the Tailwind CSS standalone CLI binary",
	Long: `Download the Tailwind CSS v4 standalone CLI binary for your platform.
The binary is placed at .bin/tailwindcss in the project root.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		root := mustRepoRoot()
		binPath := filepath.Join(root, ".bin", "tailwindcss")
		return tailwind.Download(binPath, "", tailwindFlags.force)
	},
}

var tailwindBuildCmd = &cobra.Command{
	Use:   "build",
	Short: "Compile Tailwind CSS input → embedded app.css",
	Long: `Compile web/input.css into internal/web/static/app.css using
the Tailwind CLI binary at .bin/tailwindcss.

The output is embedded into the Go binary at build time.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		root := mustRepoRoot()
		binPath := filepath.Join(root, ".bin", "tailwindcss")
		return tailwind.Build(binPath, root,
			"web/input.css",
			"internal/web/static/app.css",
			[]string{"**/*.html", "**/*.go"},
		)
	},
}

func init() {
	rootCmd.AddCommand(tailwindCmd)
	tailwindCmd.AddCommand(tailwindDownloadCmd)
	tailwindCmd.AddCommand(tailwindBuildCmd)
	tailwindDownloadCmd.Flags().BoolVarP(&tailwindFlags.force, "force", "f", false, "Re-download even if binary already exists")
}
