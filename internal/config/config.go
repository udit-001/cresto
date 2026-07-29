package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

type Config struct {
	LMStudioBaseURL string `toml:"lm_studio_base_url"`
	ModelName       string `toml:"model_name"`
	DataDir         string `toml:"data_dir"`
	Port            int    `toml:"port"`

	// SQLitePath and PDFStoragePath are derived from DataDir and set
	// during Load / Default. They are not TOML fields.
	SQLitePath     string `toml:"-"`
	PDFStoragePath string `toml:"-"`
}

const DefaultPort = 7777

var configDir = defaultConfigDir

func defaultConfigDir() string {
	d, err := os.UserConfigDir()
	if err != nil {
		d = filepath.Join(homeDir(), ".config")
	}
	return filepath.Join(d, "cresto")
}

func homeDir() string {
	d, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return d
}

func ConfigDir() string {
	return configDir()
}

func Path() string {
	return filepath.Join(ConfigDir(), "cresto.toml")
}

func PidPath() string {
	return filepath.Join(ConfigDir(), "server.pid")
}

func DefaultDataDir() string {
	return filepath.Join(homeDir(), ".cresto")
}

func DBPath(cfg *Config) string {
	dir := DefaultDataDir()
	if cfg != nil && cfg.DataDir != "" {
		dir = cfg.DataDir
	}
	return filepath.Join(dir, "income.db")
}

func PDFsPath(cfg *Config) string {
	dir := DefaultDataDir()
	if cfg != nil && cfg.DataDir != "" {
		dir = cfg.DataDir
	}
	return filepath.Join(dir, "payslips")
}

func Default() Config {
	dataDir := DefaultDataDir()
	return Config{
		LMStudioBaseURL: "http://localhost:1234/v1",
		ModelName:       "mistralai/ministral-3-3b",
		DataDir:         dataDir,
		SQLitePath:      filepath.Join(dataDir, "income.db"),
		PDFStoragePath:  filepath.Join(dataDir, "payslips"),
		Port:            DefaultPort,
	}
}

func Load() (*Config, error) {
	p := Path()
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	d := Default()
	if cfg.LMStudioBaseURL == "" {
		cfg.LMStudioBaseURL = d.LMStudioBaseURL
	}
	if cfg.ModelName == "" {
		cfg.ModelName = d.ModelName
	}
	if cfg.DataDir == "" {
		cfg.DataDir = d.DataDir
	}
	if cfg.Port == 0 {
		cfg.Port = d.Port
	}
	cfg.SQLitePath = DBPath(&cfg)
	cfg.PDFStoragePath = PDFsPath(&cfg)
	return &cfg, nil
}

func Save(cfg *Config) error {
	if strings.HasPrefix(cfg.DataDir, "~/") {
		cfg.DataDir = filepath.Join(homeDir(), cfg.DataDir[2:])
	} else if cfg.DataDir == "~" {
		cfg.DataDir = homeDir()
	}
	dir := ConfigDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	p := Path()
	f, err := os.Create(p)
	if err != nil {
		return fmt.Errorf("create %s: %w", p, err)
	}
	defer f.Close()
	if err := toml.NewEncoder(f).Encode(cfg); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	return nil
}

func SetConfigDirForTesting(dir string) func() {
	orig := configDir
	configDir = func() string { return dir }
	return func() { configDir = orig }
}
