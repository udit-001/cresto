package cli

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"cresto/internal/config"
)

func runShowHelp(cmd *cobra.Command, _ []string) error {
	return cmd.Help()
}

func mustRepoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	log.Fatal("could not find go.mod — run from inside the cresto repo")
	return ""
}

func formatAddr(port int) string {
	if port == 0 {
		port = config.DefaultPort
	}
	return fmt.Sprintf(":%d", port)
}
