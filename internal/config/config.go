package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Ollama Ollama                `yaml:"ollama"`
	Tiers  map[string]Tier       `yaml:"tiers"`
	Cache  Cache                 `yaml:"cache"`
	Tools  map[string]ToolConfig `yaml:"tools"`
	Hook   Hook                  `yaml:"hook"`
}

type Ollama struct {
	BaseURL        string        `yaml:"base_url"`
	RequestTimeout time.Duration `yaml:"request_timeout"`
}

type Tier struct {
	Model   string         `yaml:"model"`
	Think   *bool          `yaml:"think,omitempty"`
	Options map[string]any `yaml:"options,omitempty"`
}

type Cache struct {
	Path       string        `yaml:"path"`
	MaxEntries int           `yaml:"max_entries"`
	DefaultTTL time.Duration `yaml:"default_ttl"`
}

type ToolConfig struct {
	Tier     string        `yaml:"tier"`
	CacheTTL time.Duration `yaml:"cache_ttl"`
}

type Hook struct {
	Bash BashHook `yaml:"bash"`
}

type BashHook struct {
	Enabled         bool   `yaml:"enabled"`
	ThresholdTokens int    `yaml:"threshold_tokens"`
	Tier            string `yaml:"tier"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	c.Cache.Path = expandHome(c.Cache.Path)
	// OLLAMA_BASE_URL overrides the YAML, so the same checked-in config can
	// drive any host without per-machine edits.
	if v := os.Getenv("OLLAMA_BASE_URL"); v != "" {
		c.Ollama.BaseURL = v
	}
	return &c, nil
}

func (c *Config) ResolveTool(name string) (Tier, time.Duration, error) {
	tc, ok := c.Tools[name]
	if !ok {
		return Tier{}, 0, fmt.Errorf("tool %q not configured", name)
	}
	tier, ok := c.Tiers[tc.Tier]
	if !ok {
		return Tier{}, 0, fmt.Errorf("tier %q referenced by tool %q not defined", tc.Tier, name)
	}
	ttl := tc.CacheTTL
	if ttl == 0 {
		ttl = c.Cache.DefaultTTL
	}
	return tier, ttl, nil
}

func (c *Config) Fallback() (Tier, bool) {
	t, ok := c.Tiers["fallback"]
	return t, ok
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}
