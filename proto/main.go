package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"

	ledongthucPdf "github.com/ledongthuc/pdf"
	dslipakPdf "github.com/dslipak/pdf"
)

const path = "../payslip-1784482033.pdf"

func main() {
	fmt.Println("=== PROTOTYPE: Go PDF extraction comparison ===")
	fmt.Println("Question: Can Go libraries extract payslip text well enough")
	fmt.Println("          to feed a redact-then-LLM pipeline?")
	fmt.Println()

	runBaseline()
	runLedongthuc()
	runDslipak()
	runLiteParseText()
	runLiteParseMarkdown()

	fmt.Println("\n=== VERDICT GUIDE ===")
	fmt.Println("Check each output for:")
	fmt.Println("  1. Component names readable (Basic Pay, Allowance, Overtime, Tax)?")
	fmt.Println("  2. Numbers attached to components (8000, 500, 300, 800)?")
	fmt.Println("  3. Table columns distinguishable (Earnings vs Deductions)?")
	fmt.Println("  4. PII locatable for redaction (PAN, bank account, name)?")
}

func runBaseline() {
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("BASELINE: pdftotext -layout (poppler, subprocess)")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	cmd := exec.Command("pdftotext", "-layout", path, "-")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		fmt.Printf("  ERROR: %v\n", err)
		return
	}
	fmt.Println(out.String())
}

func runLedongthuc() {
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("LIBRARY 1: github.com/ledongthuc/pdf")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	f, r, err := ledongthucPdf.Open(path)
	if err != nil {
		fmt.Printf("  ERROR opening: %v\n", err)
		return
	}
	defer f.Close()
	var buf bytes.Buffer
	for i := 1; i <= r.NumPage(); i++ {
		text, err := r.Page(i).GetPlainText(nil)
		if err != nil {
			fmt.Printf("  ERROR page %d: %v\n", i, err)
			continue
		}
		buf.WriteString(text)
	}
	fmt.Println(buf.String())
}

func runDslipak() {
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("LIBRARY 2: github.com/dslipak/pdf")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	r, err := dslipakPdf.Open(path)
	if err != nil {
		fmt.Printf("  ERROR opening: %v\n", err)
		return
	}
	total := r.NumPage()
	var sb strings.Builder
	for i := 1; i <= total; i++ {
		text, err := r.Page(i).GetPlainText(nil)
		if err != nil {
			fmt.Printf("  ERROR page %d: %v\n", i, err)
			continue
		}
		sb.WriteString(text)
	}
	fmt.Println(sb.String())
}

var _ = os.Args

func runLiteParseText() {
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("LIBRARY 3: LiteParse CLI (text, --no-ocr)")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	cmd := exec.Command("lit", "parse", "--no-ocr", "--format", "text", path)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("  ERROR: %v\n", err)
		return
	}
	fmt.Println(out.String())
}

func runLiteParseMarkdown() {
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("LIBRARY 4: LiteParse CLI (markdown, --no-ocr)")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	cmd := exec.Command("lit", "parse", "--no-ocr", "--format", "markdown", path)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("  ERROR: %v\n", err)
		return
	}
	fmt.Println(out.String())
}
