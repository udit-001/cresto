// Package tailwind wraps the standalone Tailwind CSS v4 CLI: downloading the
// binary from GitHub releases and invoking it to compile web/input.css into
// the embedded app.css. No Node, no npm — the CLI is a single self-contained
// binary downloaded once per checkout.
package tailwind

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const releaseURL = "https://github.com/tailwindlabs/tailwindcss/releases/latest/download"

// Download fetches the Tailwind CLI binary for the current platform and writes
// it to binPath (typically <repo>/.bin/tailwindcss). If binPath already exists,
// Download is a no-op unless force is true. The download is verified against
// the published sha256sums.txt before being installed.
func Download(binPath, version string, force bool) error {
	if _, err := os.Stat(binPath); err == nil && !force {
		fmt.Printf("  tailwindcss already exists at %s (use --force to re-download)\n", binPath)
		return nil
	}

	asset, err := assetForPlatform()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
		return fmt.Errorf("create bin dir: %w", err)
	}

	// Download to .tmp so a partial/corrupt file never replaces a working binary.
	tmpPath := binPath + ".tmp"
	defer os.Remove(tmpPath)

	fmt.Printf("  downloading %s...\n", asset)
	if err := downloadFile(tmpPath, downloadURL(asset, version)); err != nil {
		return err
	}

	fmt.Println("  verifying checksum...")
	expected, err := fetchChecksum(downloadURL("sha256sums.txt", version), asset)
	if err != nil {
		return fmt.Errorf("fetch checksum: %w", err)
	}
	if err := verifyChecksum(tmpPath, expected); err != nil {
		return err
	}

	if err := os.Rename(tmpPath, binPath); err != nil {
		return fmt.Errorf("install binary: %w", err)
	}
	if err := os.Chmod(binPath, 0o755); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}

	fmt.Println("  ✓ tailwindcss CLI ready")
	return nil
}

func downloadURL(asset, version string) string {
	if version != "" {
		v := strings.TrimPrefix(version, "v")
		return fmt.Sprintf("https://github.com/tailwindlabs/tailwindcss/releases/download/v%s/%s", v, asset)
	}
	return fmt.Sprintf("%s/%s", releaseURL, asset)
}

func downloadFile(path, url string) error {
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	out, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer out.Close()
	written, err := io.Copy(out, resp.Body)
	if err != nil {
		return fmt.Errorf("write: %w", err)
	}
	fmt.Printf("  downloaded %.1f MB\n", float64(written)/(1<<20))
	return nil
}

func fetchChecksum(url, assetName string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	// Lines look like: <hex>  ./<filename>
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			name := strings.TrimPrefix(strings.TrimSpace(parts[1]), "./")
			if name == assetName {
				return parts[0], nil
			}
		}
	}
	return "", fmt.Errorf("checksum for %s not found", assetName)
}

func verifyChecksum(path, expected string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hash read: %w", err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != expected {
		return fmt.Errorf("checksum mismatch\n  expected: %s\n  got:      %s", expected, got)
	}
	return nil
}

func assetForPlatform() (string, error) {
	switch runtime.GOOS {
	case "linux":
		switch runtime.GOARCH {
		case "amd64":
			return "tailwindcss-linux-x64", nil
		case "arm64":
			return "tailwindcss-linux-arm64", nil
		}
	case "darwin":
		switch runtime.GOARCH {
		case "amd64":
			return "tailwindcss-macos-x64", nil
		case "arm64":
			return "tailwindcss-macos-arm64", nil
		}
	case "windows":
		if runtime.GOARCH == "amd64" {
			return "tailwindcss-windows-x64.exe", nil
		}
	}
	return "", fmt.Errorf("unsupported platform: %s/%s", runtime.GOOS, runtime.GOARCH)
}
