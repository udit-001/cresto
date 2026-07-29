package pdfstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return New(filepath.Join(t.TempDir(), "payslips"))
}

func TestSave_ReturnsRelativePath(t *testing.T) {
	ps := newTestStore(t)
	rel, err := ps.Save("payslip.pdf", strings.NewReader("%PDF-1.4 fake"))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Relative path is just the timestamped filename — no directory prefix.
	if filepath.Base(rel) != rel {
		t.Errorf("rel = %q, want bare filename (no dir)", rel)
	}
	if !strings.HasSuffix(rel, "_payslip.pdf") {
		t.Errorf("rel = %q, want suffix _payslip.pdf", rel)
	}
	if strings.HasPrefix(rel, ps.root) {
		t.Errorf("rel = %q, must not contain the absolute root", rel)
	}
}

func TestSave_PersistsFileUnderRoot(t *testing.T) {
	ps := newTestStore(t)
	rel, err := ps.Save("payslip.pdf", strings.NewReader("%PDF-1.4 fake"))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	abs := ps.Abs(rel)
	got, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", abs, err)
	}
	if string(got) != "%PDF-1.4 fake" {
		t.Errorf("contents = %q", got)
	}
	if !strings.HasPrefix(abs, ps.root+string(os.PathSeparator)) {
		t.Errorf("abs = %q, want under root %q", abs, ps.root)
	}
}

func TestSave_CreatesRootIfMissing(t *testing.T) {
	root := filepath.Join(t.TempDir(), "payslips")
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("precondition: root already exists: %v", err)
	}
	ps := New(root)
	if _, err := ps.Save("payslip.pdf", strings.NewReader("x")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("root not created: %v", err)
	}
}

func TestSave_SanitizesFilename(t *testing.T) {
	ps := newTestStore(t)
	// Path separators in the user-provided name must be neutralised — only
	// the basename may end up in the stored filename.
	rel, err := ps.Save("../../etc/passwd", strings.NewReader("x"))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if strings.Contains(rel, "..") {
		t.Errorf("rel = %q, must not allow escape", rel)
	}
	abs := ps.Abs(rel)
	if !strings.HasPrefix(abs, ps.root+string(os.PathSeparator)) {
		t.Errorf("abs = %q, escapes root", abs)
	}
}

func TestSave_UniqueTimestamps(t *testing.T) {
	ps := newTestStore(t)
	rel1, _ := ps.Save("payslip.pdf", strings.NewReader("a"))
	rel2, _ := ps.Save("payslip.pdf", strings.NewReader("b"))
	if rel1 == rel2 {
		t.Errorf("two saves produced the same filename: %q", rel1)
	}
}

func TestExists_RoundTrip(t *testing.T) {
	ps := newTestStore(t)
	rel, err := ps.Save("payslip.pdf", strings.NewReader("x"))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !ps.Exists(rel) {
		t.Errorf("Exists(%q) = false, want true", rel)
	}
}

func TestExists_MissingFile(t *testing.T) {
	ps := newTestStore(t)
	if ps.Exists("does-not-exist.pdf") {
		t.Errorf("Exists on absent file = true, want false")
	}
}

func TestExists_EmptyRelPath(t *testing.T) {
	ps := newTestStore(t)
	if ps.Exists("") {
		t.Errorf("Exists('') = true, want false")
	}
}

func TestAbs_BareFilename(t *testing.T) {
	ps := newTestStore(t)
	got := ps.Abs("123_payslip.pdf")
	want := filepath.Join(ps.root, "123_payslip.pdf")
	if got != want {
		t.Errorf("Abs = %q, want %q", got, want)
	}
}
