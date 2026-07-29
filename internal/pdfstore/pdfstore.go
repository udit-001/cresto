// Package pdfstore owns the on-disk directory that holds uploaded payslip
// PDFs. It hides the storage root behind three methods so callers never need
// to filepath.Join against the configured path, and so the database can store
// a stable relative path (just the filename) instead of a host-specific
// absolute path.
//
// Callsites store and read Payslip.RawPDFPath as the value returned by Save
// (a bare filename like "1784499935855319838_payslip.pdf"). They hand that
// same value to Abs when they need a real path for http.ServeFile or render,
// or to Exists when they want to know whether the PDF is still on disk.
package pdfstore

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Store is the PDF storage root. Construct one per app lifetime and share it
// across handlers; all methods are safe for concurrent use (they only touch
// the filesystem, which serialises its own metadata).
type Store struct {
	root string
}

// New returns a Store rooted at dir. The directory is created lazily on the
// first Save, so callers don't need to mkdir ahead of time.
func New(dir string) *Store {
	return &Store{root: dir}
}

// Save writes src to a fresh file under the storage root, returning the
// relative path (just the timestamped filename). The provided filename is
// sanitised: path separators are stripped, spaces collapse to underscores.
// The timestamp prefix disambiguates rapid successive uploads of the same name.
func (s *Store) Save(filename string, src io.Reader) (string, error) {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return "", fmt.Errorf("create pdf dir: %w", err)
	}
	safe := sanitizeFilename(filename)
	rel := fmt.Sprintf("%d_%s", time.Now().UnixNano(), safe)
	dst, err := os.Create(s.Abs(rel))
	if err != nil {
		return "", fmt.Errorf("create pdf file: %w", err)
	}
	defer dst.Close()
	if _, err := io.Copy(dst, src); err != nil {
		os.Remove(s.Abs(rel))
		return "", fmt.Errorf("write pdf: %w", err)
	}
	return rel, nil
}

// Abs joins the storage root with rel, returning the full filesystem path.
// rel is expected to be a bare filename as produced by Save; Abs does not
// validate this and will happily join any relative path the caller passes.
func (s *Store) Abs(rel string) string {
	return filepath.Join(s.root, rel)
}

// Exists reports whether a PDF exists at rel under the storage root. An empty
// rel returns false — handlers use this to distinguish "no PDF attached" from
// "PDF attached but missing from disk".
func (s *Store) Exists(rel string) bool {
	if rel == "" {
		return false
	}
	_, err := os.Stat(s.Abs(rel))
	return err == nil
}

// sanitizeFilename strips path separators and collapses spaces so a
// maliciously-named upload ("../../etc/passwd") can't escape the storage root.
// The timestamp prefix in Save handles uniqueness; this only needs to make
// the user-supplied name safe.
func sanitizeFilename(s string) string {
	s = filepath.Base(s)
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	return s
}
