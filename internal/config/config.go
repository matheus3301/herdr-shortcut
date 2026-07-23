// Package config loads and validates herdr-shortcut configuration and resolves
// the Shortcut API token. It never stores, logs, or otherwise exposes the
// token: plaintext token fields in TOML are unsupported and the token is only
// ever produced by ResolveToken as a transient in-memory value.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/matheus3301/herdr-shortcut/internal/herdr"
	"github.com/matheus3301/herdr-shortcut/internal/prompt"
	"github.com/matheus3301/herdr-shortcut/internal/tmpl"
)

// Documented default values. Keep these synchronized with config.example.toml
// and the README (enforced by tests).
const (
	DefaultAPIBaseURL       = "https://api.app.shortcut.com/api/v3"
	DefaultQuery            = "owner:{member} is:story !is:done !is:archived"
	DefaultPageSize         = 100
	DefaultMaxStories       = 250
	DefaultRequestTimeout   = 15 * time.Second
	DefaultKind             = "claude"
	DefaultNameTemplate     = "sc-{id}-{kind}"
	DefaultTabLabelTemplate = "SC-{id} {kind}"
	DefaultAgentFocus       = true
)

// Validation bounds.
const (
	minPageSize       = 1
	maxPageSize       = 250
	minMaxStories     = 1
	maxMaxStories     = 1000
	maxRequestTimeout = 5 * time.Minute
)

// Config is the fully resolved, validated configuration.
type Config struct {
	Shortcut     Shortcut
	Agent        Agent
	Repositories []Repository
	// SourcePath is the file the config was loaded from, or "" when built-in
	// defaults were used.
	SourcePath string
}

// Shortcut holds Shortcut API client configuration.
type Shortcut struct {
	APIBaseURL     string
	Query          string
	PageSize       int
	MaxStories     int
	RequestTimeout time.Duration
	// TokenCommand is an argv array executed directly (never via a shell) to
	// obtain the token when SHORTCUT_API_TOKEN is unset.
	TokenCommand []string
}

// Agent holds coding-agent launch configuration. The harness kind is chosen per
// launch from the kinds discovered on the installed Herdr binary; DefaultKind is
// the initial selection and ArgsByKind provides native arguments per kind.
type Agent struct {
	DefaultKind      string
	ArgsByKind       map[string][]string
	NameTemplate     string
	TabLabelTemplate string
	Focus            bool
	PromptTemplate   string
}

// Repository is a configured working directory choice for launches.
type Repository struct {
	Name string
	Path string
}

// Default returns the built-in default configuration.
func Default() Config {
	return Config{
		Shortcut: Shortcut{
			APIBaseURL:     DefaultAPIBaseURL,
			Query:          DefaultQuery,
			PageSize:       DefaultPageSize,
			MaxStories:     DefaultMaxStories,
			RequestTimeout: DefaultRequestTimeout,
		},
		Agent: Agent{
			DefaultKind:      DefaultKind,
			NameTemplate:     DefaultNameTemplate,
			TabLabelTemplate: DefaultTabLabelTemplate,
			Focus:            DefaultAgentFocus,
			PromptTemplate:   prompt.DefaultTemplate,
		},
	}
}

// Path resolves the configuration file path following the documented
// precedence. It returns "" when no location can be determined.
func Path(env func(string) string) string {
	if env == nil {
		env = os.Getenv
	}
	if dir := env("HERDR_PLUGIN_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "config.toml")
	}
	if dir := env("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "herdr-shortcut", "config.toml")
	}
	if home := env("HOME"); home != "" {
		return filepath.Join(home, ".config", "herdr-shortcut", "config.toml")
	}
	return ""
}

// Load resolves the config path from env, loads the file when present, applies
// defaults for anything unset, and validates the result. A missing config file
// is valid and yields the defaults.
func Load(env func(string) string) (Config, error) {
	if env == nil {
		env = os.Getenv
	}
	cfg := Default()
	path := Path(env)
	if path != "" {
		data, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := decodeInto(&cfg, data); err != nil {
				return Config{}, fmt.Errorf("config %s: %w", path, err)
			}
			cfg.SourcePath = path
		case errors.Is(err, fs.ErrNotExist):
			// Missing config is valid; keep defaults.
		default:
			return Config{}, fmt.Errorf("read config %s: %w", path, err)
		}
	}
	if err := cfg.Validate(env); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// LoadData parses config bytes over the defaults and validates. It is used by
// tests and by callers that already hold config bytes.
func LoadData(data []byte, env func(string) string) (Config, error) {
	if env == nil {
		env = os.Getenv
	}
	cfg := Default()
	if err := decodeInto(&cfg, data); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(env); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

type rawConfig struct {
	Shortcut     *rawShortcut `toml:"shortcut"`
	Agent        *rawAgent    `toml:"agent"`
	Repositories []rawRepo    `toml:"repositories"`
}

type rawShortcut struct {
	APIBaseURL     *string  `toml:"api_base_url"`
	Query          *string  `toml:"query"`
	PageSize       *int     `toml:"page_size"`
	MaxStories     *int     `toml:"max_stories"`
	RequestTimeout *string  `toml:"request_timeout"`
	TokenCommand   []string `toml:"token_command"`
}

type rawAgent struct {
	DefaultKind      *string             `toml:"default_kind"`
	ArgsByKind       map[string][]string `toml:"args_by_kind"`
	NameTemplate     *string             `toml:"name_template"`
	TabLabelTemplate *string             `toml:"tab_label_template"`
	Focus            *bool               `toml:"focus"`
	PromptTemplate   *string             `toml:"prompt_template"`
}

type rawRepo struct {
	Name string `toml:"name"`
	Path string `toml:"path"`
}

func decodeInto(cfg *Config, data []byte) error {
	var raw rawConfig
	md, err := toml.Decode(string(data), &raw)
	if err != nil {
		return fmt.Errorf("parse TOML: %w", err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, 0, len(undecoded))
		for _, k := range undecoded {
			keys = append(keys, k.String())
		}
		sort.Strings(keys)
		return fmt.Errorf("unknown configuration key(s): %s", strings.Join(keys, ", "))
	}
	return raw.applyTo(cfg)
}

func (r rawConfig) applyTo(cfg *Config) error {
	if s := r.Shortcut; s != nil {
		if s.APIBaseURL != nil {
			cfg.Shortcut.APIBaseURL = *s.APIBaseURL
		}
		if s.Query != nil {
			cfg.Shortcut.Query = *s.Query
		}
		if s.PageSize != nil {
			cfg.Shortcut.PageSize = *s.PageSize
		}
		if s.MaxStories != nil {
			cfg.Shortcut.MaxStories = *s.MaxStories
		}
		if s.RequestTimeout != nil {
			d, err := time.ParseDuration(*s.RequestTimeout)
			if err != nil {
				return fmt.Errorf("shortcut.request_timeout: %v", err)
			}
			cfg.Shortcut.RequestTimeout = d
		}
		if s.TokenCommand != nil {
			cfg.Shortcut.TokenCommand = s.TokenCommand
		}
	}
	if a := r.Agent; a != nil {
		if a.DefaultKind != nil {
			cfg.Agent.DefaultKind = *a.DefaultKind
		}
		if a.ArgsByKind != nil {
			cfg.Agent.ArgsByKind = a.ArgsByKind
		}
		if a.NameTemplate != nil {
			cfg.Agent.NameTemplate = *a.NameTemplate
		}
		if a.TabLabelTemplate != nil {
			cfg.Agent.TabLabelTemplate = *a.TabLabelTemplate
		}
		if a.Focus != nil {
			cfg.Agent.Focus = *a.Focus
		}
		if a.PromptTemplate != nil {
			cfg.Agent.PromptTemplate = *a.PromptTemplate
		}
	}
	if r.Repositories != nil {
		cfg.Repositories = make([]Repository, len(r.Repositories))
		for i, repo := range r.Repositories {
			cfg.Repositories[i] = Repository{Name: repo.Name, Path: repo.Path}
		}
	}
	return nil
}

// Validate checks every field with actionable, field-specific errors. env is
// used only to expand repository paths for duplicate detection; stored paths
// are never mutated.
func (c Config) Validate(env func(string) string) error {
	if env == nil {
		env = os.Getenv
	}
	if err := validateBaseURL(c.Shortcut.APIBaseURL); err != nil {
		return err
	}
	if c.Shortcut.PageSize < minPageSize || c.Shortcut.PageSize > maxPageSize {
		return fmt.Errorf("shortcut.page_size must be between %d and %d, got %d", minPageSize, maxPageSize, c.Shortcut.PageSize)
	}
	if c.Shortcut.MaxStories < minMaxStories || c.Shortcut.MaxStories > maxMaxStories {
		return fmt.Errorf("shortcut.max_stories must be between %d and %d, got %d", minMaxStories, maxMaxStories, c.Shortcut.MaxStories)
	}
	if c.Shortcut.MaxStories < c.Shortcut.PageSize {
		return fmt.Errorf("shortcut.max_stories (%d) must not be lower than shortcut.page_size (%d)", c.Shortcut.MaxStories, c.Shortcut.PageSize)
	}
	if c.Shortcut.RequestTimeout <= 0 {
		return fmt.Errorf("shortcut.request_timeout must be positive, got %s", c.Shortcut.RequestTimeout)
	}
	if c.Shortcut.RequestTimeout > maxRequestTimeout {
		return fmt.Errorf("shortcut.request_timeout must be at most %s, got %s", maxRequestTimeout, c.Shortcut.RequestTimeout)
	}
	if strings.TrimSpace(c.Shortcut.Query) == "" {
		return errors.New("shortcut.query must not be empty")
	}
	if _, err := tmpl.Parse(c.Shortcut.Query, []string{"member"}); err != nil {
		return fmt.Errorf("shortcut.query: %w", err)
	}
	for i, arg := range c.Shortcut.TokenCommand {
		if strings.TrimSpace(arg) == "" {
			return fmt.Errorf("shortcut.token_command[%d] must not be empty", i)
		}
	}
	if strings.TrimSpace(c.Agent.DefaultKind) == "" {
		return errors.New("agent.default_kind must not be empty")
	}
	if !herdr.IsValidKind(c.Agent.DefaultKind) {
		return fmt.Errorf("agent.default_kind %q is not a valid Herdr kind identifier", c.Agent.DefaultKind)
	}
	for k := range c.Agent.ArgsByKind {
		if !herdr.IsValidKind(k) {
			return fmt.Errorf("agent.args_by_kind key %q is not a valid Herdr kind identifier", k)
		}
	}
	if _, err := tmpl.Parse(c.Agent.NameTemplate, []string{"id", "kind"}); err != nil {
		return fmt.Errorf("agent.name_template: %w", err)
	}
	if _, err := tmpl.Parse(c.Agent.TabLabelTemplate, []string{"id", "kind"}); err != nil {
		return fmt.Errorf("agent.tab_label_template: %w", err)
	}
	if _, err := tmpl.Parse(c.Agent.PromptTemplate, prompt.Placeholders()); err != nil {
		return fmt.Errorf("agent.prompt_template: %w", err)
	}
	if err := c.validateRepositories(env); err != nil {
		return err
	}
	return nil
}

func (c Config) validateRepositories(env func(string) string) error {
	seen := make(map[string]string, len(c.Repositories))
	for i, r := range c.Repositories {
		if strings.TrimSpace(r.Name) == "" {
			return fmt.Errorf("repositories[%d].name must not be empty", i)
		}
		if strings.TrimSpace(r.Path) == "" {
			return fmt.Errorf("repositories[%d].path must not be empty", i)
		}
		expanded, err := ExpandPath(r.Path, env)
		if err != nil {
			return fmt.Errorf("repositories[%d] (%s): %w", i, r.Name, err)
		}
		if prev, dup := seen[expanded]; dup {
			return fmt.Errorf("repositories[%d] (%s) resolves to the same path as %q: %s", i, r.Name, prev, expanded)
		}
		seen[expanded] = r.Name
	}
	return nil
}

func validateBaseURL(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return errors.New("shortcut.api_base_url must not be empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("shortcut.api_base_url is not a valid URL: %v", err)
	}
	if u.Host == "" {
		return fmt.Errorf("shortcut.api_base_url must be absolute, got %q", raw)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("shortcut.api_base_url has an empty hostname, got %q", raw)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if isLoopbackHost(u.Hostname()) {
			return nil
		}
		return fmt.Errorf("shortcut.api_base_url must use https (http is only allowed for loopback hosts), got %q", raw)
	default:
		return fmt.Errorf("shortcut.api_base_url must use https, got scheme %q", u.Scheme)
	}
}
