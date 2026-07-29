package tailwind

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Build runs the Tailwind CLI to compile inputCSS into outputCSS, scanning the
// given content globs for class usage. The resulting file is what gets
// //go:embed'd into the binary. Requires a prior Download call.
func Build(binPath, root, inputCSS, outputCSS string, contentGlobs []string) error {
	if _, err := os.Stat(binPath); err != nil {
		return fmt.Errorf("tailwind CLI not found at %s — run `tailwind download` first", binPath)
	}

	inputFull := filepath.Join(root, inputCSS)
	outputFull := filepath.Join(root, outputCSS)

	args := []string{
		"--input", inputFull,
		"--output", outputFull,
		"--minify",
	}
	for _, g := range contentGlobs {
		args = append(args, "--content", g)
	}

	fmt.Println("  building CSS...")
	cmd := exec.Command(binPath, args...)
	cmd.Dir = root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tailwind build: %w", err)
	}

	info, _ := os.Stat(outputFull)
	fmt.Printf("  ✓ wrote %s (%d bytes)\n", outputCSS, info.Size())
	return nil
}
