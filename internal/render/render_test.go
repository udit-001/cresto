package render

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRender_RealPDF(t *testing.T) {
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		t.Skip("pdftoppm not installed")
	}

	repoRoot := findRepoRoot(t)
	pdfPath := filepath.Join(repoRoot, "payslip-1784482033.pdf")
	if _, err := os.Stat(pdfPath); err != nil {
		t.Skipf("test PDF not found: %s", pdfPath)
	}

	img, err := Render(pdfPath)
	if err != nil {
		t.Fatalf("Render() error: %v", err)
	}
	if len(img) == 0 {
		t.Fatal("Render() returned empty image")
	}

	if img[0] != 0x89 || img[1] != 'P' || img[2] != 'N' || img[3] != 'G' {
		t.Fatalf("Render() output is not a PNG, got bytes: %x", img[:4])
	}
	if len(img) < 10000 {
		t.Fatalf("Render() image suspiciously small: %d bytes", len(img))
	}
}

func TestRender_FileNotFound(t *testing.T) {
	_, err := Render("/nonexistent/path/to/file.pdf")
	if err == nil {
		t.Fatal("Render() expected error for missing file, got nil")
	}
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not find go.mod")
	return ""
}
