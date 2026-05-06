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
	Providers       map[string]ProviderConfig `yaml:"providers"`
	DefaultProvider string                    `yaml:"default_provider"`
	Tiers           map[string]Tier           `yaml:"tiers"`
	Cache           Cache                     `yaml:"cache"`
	Tools           map[string]ToolConfig     `yaml:"tools"`
}

// ProviderConfig is one named LLM endpoint. Type selects the implementation
// (currently "ollama" or "openai"); the rest are wire knobs.
type ProviderConfig struct {
	Type           string        `yaml:"type"`
	BaseURL        string        `yaml:"base_url"`
	APIKey         string        `yaml:"api_key,omitempty"`
	RequestTimeout time.Duration `yaml:"request_timeout"`
}

type Tier struct {
	// Provider, when set, overrides default_provider for this tier — letting
	// you route tier 1 through a fast local model and tier 2 through cloud,
	// for example. Empty means "use default_provider".
	Provider string         `yaml:"provider,omitempty"`
	Model    string         `yaml:"model"`
	Think    *bool          `yaml:"think,omitempty"`
	Options  map[string]any `yaml:"options,omitempty"`
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
	if c.Providers == nil {
		c.Providers = map[string]ProviderConfig{}
	}
	if len(c.Providers) == 0 {
		return nil, fmt.Errorf("no providers configured — define at least one under `providers:`")
	}

	applyEnvOverrides(&c)

	if c.DefaultProvider == "" {
		// Single-provider configs don't need an explicit default.
		if len(c.Providers) == 1 {
			for name := range c.Providers {
				c.DefaultProvider = name
			}
		} else {
			return nil, fmt.Errorf("default_provider must be set when multiple providers are configured")
		}
	}
	if _, ok := c.Providers[c.DefaultProvider]; !ok {
		return nil, fmt.Errorf("default_provider %q not in providers map", c.DefaultProvider)
	}
	for tname, tier := range c.Tiers {
		if tier.Provider == "" {
			continue
		}
		if _, ok := c.Providers[tier.Provider]; !ok {
			return nil, fmt.Errorf("tier %q references unknown provider %q", tname, tier.Provider)
		}
	}
	return &c, nil
}

// applyEnvOverrides honours OLLAMA_BASE_URL, OPENAI_BASE_URL and
// OPENAI_API_KEY by patching the first provider of the matching type. This
// keeps the checked-in YAML portable: per-machine endpoint URLs and secrets
// stay in the shell environment.
func applyEnvOverrides(c *Config) {
	patch := func(envKey, providerType string, set func(*ProviderConfig, string)) {
		v := os.Getenv(envKey)
		if v == "" {
			return
		}
		for name, p := range c.Providers {
			if p.Type == providerType {
				set(&p, v)
				c.Providers[name] = p
				return
			}
		}
	}
	patch("OLLAMA_BASE_URL", "ollama", func(p *ProviderConfig, v string) { p.BaseURL = v })
	patch("OPENAI_BASE_URL", "openai", func(p *ProviderConfig, v string) { p.BaseURL = v })
	patch("OPENAI_API_KEY", "openai", func(p *ProviderConfig, v string) { p.APIKey = v })
}

// ResolveTier returns the tier and the resolved provider name (defaulting to
// DefaultProvider when the tier doesn't override).
func (c *Config) ResolveTier(name string) (Tier, string, error) {
	t, ok := c.Tiers[name]
	if !ok {
		return Tier{}, "", fmt.Errorf("tier %q not defined", name)
	}
	pname := t.Provider
	if pname == "" {
		pname = c.DefaultProvider
	}
	if _, ok := c.Providers[pname]; !ok {
		return Tier{}, "", fmt.Errorf("tier %q references unknown provider %q", name, pname)
	}
	return t, pname, nil
}

// ResolveTool returns the tier, its provider name, and the cache TTL for the
// named tool.
func (c *Config) ResolveTool(name string) (Tier, string, time.Duration, error) {
	tc, ok := c.Tools[name]
	if !ok {
		return Tier{}, "", 0, fmt.Errorf("tool %q not configured", name)
	}
	tier, pname, err := c.ResolveTier(tc.Tier)
	if err != nil {
		return Tier{}, "", 0, fmt.Errorf("tool %q: %w", name, err)
	}
	ttl := tc.CacheTTL
	if ttl == 0 {
		ttl = c.Cache.DefaultTTL
	}
	return tier, pname, ttl, nil
}

// Fallback returns the fallback tier with its resolved provider name, or
// (_, _, false) if no fallback is configured.
func (c *Config) Fallback() (Tier, string, bool) {
	t, ok := c.Tiers["fallback"]
	if !ok {
		return Tier{}, "", false
	}
	pname := t.Provider
	if pname == "" {
		pname = c.DefaultProvider
	}
	return t, pname, true
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}
