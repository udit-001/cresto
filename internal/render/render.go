package render

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
)

const dpi = 150

var ErrPdftoppmNotFound = errors.New("pdftoppm not found: install poppler-utils")

func Render(pdfPath string) ([]byte, error) {
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		return nil, ErrPdftoppmNotFound
	}
	if _, err := os.Stat(pdfPath); err != nil {
		return nil, fmt.Errorf("pdf file: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "cresto-render-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	outputPrefix := tmpDir + "/page"
	cmd := exec.Command("pdftoppm", "-png", "-r", fmt.Sprintf("%d", dpi), "-singlefile", pdfPath, outputPrefix)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("pdftoppm failed: %w: %s", err, stderr.String())
	}

	pngPath := outputPrefix + ".png"
	img, err := os.ReadFile(pngPath)
	if err != nil {
		return nil, fmt.Errorf("read rendered image: %w", err)
	}
	return img, nil
}
