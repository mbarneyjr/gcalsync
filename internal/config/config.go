package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Google   GoogleConfig             `toml:"google"`
	General  GeneralConfig            `toml:"general"`
	Accounts map[string]AccountConfig `toml:"accounts"`
}

type GoogleConfig struct {
	ClientID     string `toml:"client_id"`
	ClientSecret string `toml:"client_secret"`
}

type GeneralConfig struct {
	BlockEventVisibility string `toml:"block_event_visibility,omitempty"`
	AuthorizedPorts      []int  `toml:"authorized_ports"`
	IgnoreBirthdays      bool   `toml:"ignore_birthdays"`
}

type AccountConfig struct {
	Enterprise bool     `toml:"enterprise"`
	Calendars  []string `toml:"calendars"`
}

func DefaultConfigDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "gcalsync")
}

func ConfigPath(configDir string) string {
	return filepath.Join(configDir, "config.toml")
}

func Load(configDir string) (*Config, error) {
	path := ConfigPath(configDir)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if cfg.General.AuthorizedPorts == nil {
		cfg.General.AuthorizedPorts = []int{8080, 8081, 8082}
	}
	if cfg.Accounts == nil {
		cfg.Accounts = make(map[string]AccountConfig)
	}

	return &cfg, nil
}

func (c *Config) Validate() error {
	if c.Google.ClientID == "" {
		return fmt.Errorf("google.client_id is required")
	}
	if c.Google.ClientSecret == "" {
		return fmt.Errorf("google.client_secret is required")
	}
	for name, acct := range c.Accounts {
		if name == "" {
			return fmt.Errorf("account name cannot be empty")
		}
		seen := make(map[string]bool)
		for _, cal := range acct.Calendars {
			if cal == "" {
				return fmt.Errorf("calendar ID cannot be empty in account %q", name)
			}
			if seen[cal] {
				return fmt.Errorf("duplicate calendar %q in account %q", cal, name)
			}
			seen[cal] = true
		}
		_ = acct
	}
	return nil
}

func Save(configDir string, cfg *Config) error {
	path := ConfigPath(configDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating config file: %w", err)
	}
	defer f.Close()

	enc := toml.NewEncoder(f)
	if err := enc.Encode(cfg); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	return nil
}
