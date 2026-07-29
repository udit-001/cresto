//go:build integration

package llm

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"cresto/internal/render"
)

func TestExtract_RealPayslip(t *testing.T) {
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		t.Skip("pdftoppm not installed")
	}
	if !lmstudioReachable(t) {
		t.Skip("LMStudio not running on localhost:1234")
	}

	repoRoot := findRepoRoot(t)
	pdfPath := filepath.Join(repoRoot, "payslip-1784482033.pdf")
	if _, err := os.Stat(pdfPath); err != nil {
		t.Skipf("test PDF not found: %s", pdfPath)
	}

	img, err := render.Render(pdfPath)
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	client := NewClient("http://localhost:1234/v1", "mistralai/ministral-3-3b")
	ext, err := client.Extract(img)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	if ext.Company == "" {
		t.Error("Company is empty")
	}
	if ext.PayPeriod == "" {
		t.Error("PayPeriod is empty")
	}
	if len(ext.Earnings) == 0 {
		t.Error("Earnings is empty")
	}
	if ext.Totals.NetPay <= 0 {
		t.Errorf("NetPay = %v, want > 0", ext.Totals.NetPay)
	}

	raw, _ := json.Marshal(ext)
	assertNoPII(t, string(raw))
}

func lmstudioReachable(t *testing.T) bool {
	t.Helper()
	cmd := exec.Command("curl", "-s", "-m", "2", "http://localhost:1234/v1/models")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "data")
}

func assertNoPII(t *testing.T, output string) {
	t.Helper()
	knownPII := []string{
		"James Bond",
		"21278172812781231",
		"AJSKJ2121P",
		"31231213",
		"PYKRP13829382833123",
	}
	for _, pii := range knownPII {
		if strings.Contains(output, pii) {
			t.Errorf("PII leaked in output: %q", pii)
		}
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
