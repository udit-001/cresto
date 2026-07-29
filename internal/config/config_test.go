package config

import (
	"os"
	"path/filepath"
	"testing"
)

func withTempConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := configDir
	configDir = func() string { return dir }
	t.Cleanup(func() { configDir = orig })
	return dir
}

func TestConfigDir(t *testing.T) {
	dir := withTempConfigDir(t)
	got := ConfigDir()
	if got != dir {
		t.Errorf("ConfigDir() = %q, want %q", got, dir)
	}
}

func TestPath(t *testing.T) {
	withTempConfigDir(t)
	got := Path()
	if filepath.Base(got) != "cresto.toml" {
		t.Errorf("Path() = %q, want base 'cresto.toml'", got)
	}
}

func TestDefaultDataDir(t *testing.T) {
	got := DefaultDataDir()
	if filepath.Base(got) != ".cresto" {
		t.Errorf("DefaultDataDir() = %q, want base '.cresto'", got)
	}
}

func TestDefault(t *testing.T) {
	cfg := Default()
	if cfg.Port != DefaultPort {
		t.Errorf("Default().Port = %d, want %d", cfg.Port, DefaultPort)
	}
	if cfg.LMStudioBaseURL == "" {
		t.Error("Default().LMStudioBaseURL is empty")
	}
	if cfg.ModelName == "" {
		t.Error("Default().ModelName is empty")
	}
	if cfg.SQLitePath == "" {
		t.Error("Default().SQLitePath is empty")
	}
	if cfg.PDFStoragePath == "" {
		t.Error("Default().PDFStoragePath is empty")
	}
}

func TestLoadReturnsNilWhenFileMissing(t *testing.T) {
	withTempConfigDir(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if cfg != nil {
		t.Fatalf("Load() = %v, want nil", cfg)
	}
}

func TestLoadCorrupt(t *testing.T) {
	dir := withTempConfigDir(t)
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "cresto.toml"), []byte("not valid toml {{{"), 0644)

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for corrupt config")
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	withTempConfigDir(t)

	original := &Config{
		LMStudioBaseURL: "http://custom:5678/v1",
		ModelName:       "custom-model",
		DataDir:         "/custom/data",
		Port:            9999,
	}
	if err := Save(original); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	if _, err := os.Stat(Path()); err != nil {
		t.Fatalf("config file not created: %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded == nil {
		t.Fatal("Load() returned nil after Save")
	}
	if loaded.LMStudioBaseURL != original.LMStudioBaseURL {
		t.Errorf("LMStudioBaseURL = %q, want %q", loaded.LMStudioBaseURL, original.LMStudioBaseURL)
	}
	if loaded.ModelName != original.ModelName {
		t.Errorf("ModelName = %q, want %q", loaded.ModelName, original.ModelName)
	}
	if loaded.DataDir != original.DataDir {
		t.Errorf("DataDir = %q, want %q", loaded.DataDir, original.DataDir)
	}
	if loaded.Port != original.Port {
		t.Errorf("Port = %d, want %d", loaded.Port, original.Port)
	}
}

func TestLoadFillsDefaults(t *testing.T) {
	withTempConfigDir(t)

	if err := Save(&Config{}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	def := Default()
	if cfg.LMStudioBaseURL != def.LMStudioBaseURL {
		t.Errorf("LMStudioBaseURL = %q, want %q (default)", cfg.LMStudioBaseURL, def.LMStudioBaseURL)
	}
	if cfg.ModelName != def.ModelName {
		t.Errorf("ModelName = %q, want %q (default)", cfg.ModelName, def.ModelName)
	}
	if cfg.DataDir != def.DataDir {
		t.Errorf("DataDir = %q, want %q (default)", cfg.DataDir, def.DataDir)
	}
	if cfg.Port != def.Port {
		t.Errorf("Port = %d, want %d (default)", cfg.Port, def.Port)
	}
}

func TestLoadDerivesPaths(t *testing.T) {
	withTempConfigDir(t)

	if err := Save(&Config{DataDir: "/custom/data"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.SQLitePath != "/custom/data/income.db" {
		t.Errorf("SQLitePath = %q, want %q", cfg.SQLitePath, "/custom/data/income.db")
	}
	if cfg.PDFStoragePath != "/custom/data/payslips" {
		t.Errorf("PDFStoragePath = %q, want %q", cfg.PDFStoragePath, "/custom/data/payslips")
	}
}

func TestSaveCreatesConfigDir(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "nested", "deep")
	orig := configDir
	configDir = func() string { return nested }
	t.Cleanup(func() { configDir = orig })

	if err := Save(&Config{Port: 1}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(nested, "cresto.toml")); err != nil {
		t.Fatalf("config file not created in nested dir: %v", err)
	}
}

func TestSaveExpandsTilde(t *testing.T) {
	withTempConfigDir(t)

	home, _ := os.UserHomeDir()
	cfg := &Config{DataDir: "~/my-income"}
	if err := Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if cfg.DataDir != filepath.Join(home, "my-income") {
		t.Errorf("expected %s, got %s", filepath.Join(home, "my-income"), cfg.DataDir)
	}
}

func TestDBPath(t *testing.T) {
	tests := []struct {
		name string
		cfg  *Config
		want string
	}{
		{
			name: "with config",
			cfg:  &Config{DataDir: "/tmp/data"},
			want: "/tmp/data/income.db",
		},
		{
			name: "nil config",
			cfg:  nil,
			want: filepath.Join(DefaultDataDir(), "income.db"),
		},
		{
			name: "empty data dir",
			cfg:  &Config{DataDir: ""},
			want: filepath.Join(DefaultDataDir(), "income.db"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DBPath(tc.cfg)
			if got != tc.want {
				t.Errorf("DBPath() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPDFsPath(t *testing.T) {
	tests := []struct {
		name string
		cfg  *Config
		want string
	}{
		{
			name: "with config",
			cfg:  &Config{DataDir: "/tmp/data"},
			want: "/tmp/data/payslips",
		},
		{
			name: "nil config",
			cfg:  nil,
			want: filepath.Join(DefaultDataDir(), "payslips"),
		},
		{
			name: "empty data dir",
			cfg:  &Config{DataDir: ""},
			want: filepath.Join(DefaultDataDir(), "payslips"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := PDFsPath(tc.cfg)
			if got != tc.want {
				t.Errorf("PDFsPath() = %q, want %q", got, tc.want)
			}
		})
	}
}
