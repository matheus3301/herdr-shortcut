package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/matheus3301/herdr-shortcut/internal/prompt"
)

func envFunc(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDefaults(t *testing.T) {
	t.Parallel()
	c := Default()
	if c.Shortcut.APIBaseURL != DefaultAPIBaseURL {
		t.Errorf("APIBaseURL = %q", c.Shortcut.APIBaseURL)
	}
	if c.Shortcut.PageSize != 100 || c.Shortcut.MaxStories != 250 {
		t.Errorf("page/max defaults wrong: %d/%d", c.Shortcut.PageSize, c.Shortcut.MaxStories)
	}
	if c.Shortcut.RequestTimeout != 15*time.Second {
		t.Errorf("timeout default = %s", c.Shortcut.RequestTimeout)
	}
	if c.Agent.DefaultKind != "claude" || !c.Agent.Focus {
		t.Errorf("agent defaults wrong: %+v", c.Agent)
	}
	if c.Agent.NameTemplate != "sc-{id}-{kind}" || c.Agent.TabLabelTemplate != "SC-{id} {kind}" {
		t.Errorf("template defaults wrong: %+v", c.Agent)
	}
	if c.Agent.PromptTemplate != prompt.DefaultTemplate {
		t.Errorf("prompt template default mismatch")
	}
	if err := c.Validate(envFunc(nil)); err != nil {
		t.Fatalf("default config must validate: %v", err)
	}
}

func TestPathPrecedence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"herdr dir wins", map[string]string{"HERDR_PLUGIN_CONFIG_DIR": "/h", "XDG_CONFIG_HOME": "/x", "HOME": "/home/u"}, "/h/config.toml"},
		{"xdg next", map[string]string{"XDG_CONFIG_HOME": "/x", "HOME": "/home/u"}, "/x/herdr-shortcut/config.toml"},
		{"home fallback", map[string]string{"HOME": "/home/u"}, "/home/u/.config/herdr-shortcut/config.toml"},
		{"none", map[string]string{}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Path(envFunc(tc.env)); got != tc.want {
				t.Errorf("Path() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLoadMissingFileUsesDefaults(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	c, err := Load(envFunc(map[string]string{"HERDR_PLUGIN_CONFIG_DIR": dir, "HOME": dir}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.SourcePath != "" {
		t.Errorf("SourcePath should be empty for defaults, got %q", c.SourcePath)
	}
	if c.Shortcut.PageSize != DefaultPageSize {
		t.Errorf("expected defaults")
	}
}

func TestLoadFileMergesOverDefaults(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := `
[shortcut]
page_size = 50
request_timeout = "5s"

[agent]
focus = false

[[repositories]]
name = "Main"
path = "` + dir + `"
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(envFunc(map[string]string{"HERDR_PLUGIN_CONFIG_DIR": dir, "HOME": dir}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.SourcePath != filepath.Join(dir, "config.toml") {
		t.Errorf("SourcePath = %q", c.SourcePath)
	}
	if c.Shortcut.PageSize != 50 {
		t.Errorf("page_size = %d, want 50", c.Shortcut.PageSize)
	}
	if c.Shortcut.MaxStories != DefaultMaxStories {
		t.Errorf("max_stories should keep default, got %d", c.Shortcut.MaxStories)
	}
	if c.Shortcut.RequestTimeout != 5*time.Second {
		t.Errorf("timeout = %s", c.Shortcut.RequestTimeout)
	}
	if c.Agent.Focus {
		t.Errorf("focus should be false")
	}
	if len(c.Repositories) != 1 || c.Repositories[0].Name != "Main" {
		t.Errorf("repositories not parsed: %+v", c.Repositories)
	}
}

func TestLoadRejectsUnknownKeys(t *testing.T) {
	t.Parallel()
	_, err := LoadData([]byte("[shortcut]\npage_sixe = 10\n"), envFunc(nil))
	if err == nil || !strings.Contains(err.Error(), "unknown configuration key") {
		t.Fatalf("expected unknown-key error, got %v", err)
	}
}

func TestValidationErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantSub string
	}{
		{"empty url", func(c *Config) { c.Shortcut.APIBaseURL = "" }, "api_base_url"},
		{"http non-loopback", func(c *Config) { c.Shortcut.APIBaseURL = "http://api.example.com" }, "https"},
		{"relative url", func(c *Config) { c.Shortcut.APIBaseURL = "/api/v3" }, "absolute"},
		{"ftp scheme", func(c *Config) { c.Shortcut.APIBaseURL = "ftp://x/y" }, "https"},
		{"page size 0", func(c *Config) { c.Shortcut.PageSize = 0 }, "page_size"},
		{"page size 251", func(c *Config) { c.Shortcut.PageSize = 251 }, "page_size"},
		{"max stories 0", func(c *Config) { c.Shortcut.MaxStories = 0 }, "max_stories"},
		{"max stories 1001", func(c *Config) { c.Shortcut.MaxStories = 1001 }, "max_stories"},
		{"max < page", func(c *Config) { c.Shortcut.PageSize = 200; c.Shortcut.MaxStories = 100 }, "not be lower"},
		{"timeout zero", func(c *Config) { c.Shortcut.RequestTimeout = 0 }, "positive"},
		{"timeout too big", func(c *Config) { c.Shortcut.RequestTimeout = time.Hour }, "at most"},
		{"bad query placeholder", func(c *Config) { c.Shortcut.Query = "owner:{bogus}" }, "query"},
		{"empty default_kind", func(c *Config) { c.Agent.DefaultKind = "" }, "default_kind"},
		{"invalid default_kind", func(c *Config) { c.Agent.DefaultKind = "Bad Kind" }, "default_kind"},
		{"invalid args_by_kind key", func(c *Config) { c.Agent.ArgsByKind = map[string][]string{"Bad Kind": {}} }, "args_by_kind"},
		{"name template bad placeholder", func(c *Config) { c.Agent.NameTemplate = "sc-{bogus}" }, "name_template"},
		{"kind in name ok but other bad", func(c *Config) { c.Agent.TabLabelTemplate = "{team_id}" }, "tab_label_template"},
		{"bad label template", func(c *Config) { c.Agent.TabLabelTemplate = "{bogus}" }, "tab_label_template"},
		{"bad prompt template", func(c *Config) { c.Agent.PromptTemplate = "{bogus}" }, "prompt_template"},
		{"empty token cmd arg", func(c *Config) { c.Shortcut.TokenCommand = []string{"op", ""} }, "token_command"},
		{"empty repo name", func(c *Config) { c.Repositories = []Repository{{Name: "", Path: "/tmp"}} }, "name"},
		{"empty repo path", func(c *Config) { c.Repositories = []Repository{{Name: "x", Path: ""}} }, "path"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := Default()
			tc.mutate(&c)
			err := c.Validate(envFunc(map[string]string{"HOME": "/home/u"}))
			if err == nil {
				t.Fatalf("expected error containing %q", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestValidationAllowsLoopbackHTTP(t *testing.T) {
	t.Parallel()
	for _, u := range []string{"http://localhost:8080/api/v3", "http://127.0.0.1/api/v3", "http://[::1]:9000/api"} {
		c := Default()
		c.Shortcut.APIBaseURL = u
		if err := c.Validate(envFunc(nil)); err != nil {
			t.Errorf("loopback %q should validate: %v", u, err)
		}
	}
}

func TestDuplicateRepositoryPaths(t *testing.T) {
	t.Parallel()
	c := Default()
	c.Repositories = []Repository{
		{Name: "A", Path: "~/src/x"},
		{Name: "B", Path: "$HOME/src/x"},
	}
	err := c.Validate(envFunc(map[string]string{"HOME": "/home/u"}))
	if err == nil || !strings.Contains(err.Error(), "same path") {
		t.Fatalf("expected duplicate-path error, got %v", err)
	}
}

func TestExpandPath(t *testing.T) {
	t.Parallel()
	env := envFunc(map[string]string{"HOME": "/home/u", "PROJECTS": "/data/projects"})
	tests := []struct {
		in   string
		want string
	}{
		{"~/src/app", "/home/u/src/app"},
		{"~", "/home/u"},
		{"$PROJECTS/app", "/data/projects/app"},
		{"${PROJECTS}/app", "/data/projects/app"},
		{"/abs/path", "/abs/path"},
	}
	for _, tc := range tests {
		got, err := ExpandPath(tc.in, env)
		if err != nil {
			t.Errorf("ExpandPath(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ExpandPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestExpandPathTildeNoHome(t *testing.T) {
	t.Parallel()
	if _, err := ExpandPath("~/x", envFunc(nil)); err == nil {
		t.Fatal("expected error when HOME unset")
	}
}

func TestExpandPathRejectsUnresolvedVar(t *testing.T) {
	t.Parallel()
	_, err := ExpandPath("$NOPE/x", envFunc(map[string]string{"HOME": "/h"}))
	if err == nil || !strings.Contains(err.Error(), "unresolved environment variable") {
		t.Fatalf("expected unresolved-var error, got %v", err)
	}
}

func TestValidationWhitespaceQueryAndEmptyHost(t *testing.T) {
	t.Parallel()
	c := Default()
	c.Shortcut.Query = "   "
	if err := c.Validate(envFunc(nil)); err == nil || !strings.Contains(err.Error(), "query") {
		t.Errorf("whitespace query should fail: %v", err)
	}
	c = Default()
	c.Shortcut.APIBaseURL = "https://:8443/api"
	if err := c.Validate(envFunc(nil)); err == nil || !strings.Contains(err.Error(), "hostname") {
		t.Errorf("empty hostname should fail: %v", err)
	}
}

func TestRequestTimeoutParseError(t *testing.T) {
	t.Parallel()
	_, err := LoadData([]byte("[shortcut]\nrequest_timeout = \"notaduration\"\n"), envFunc(nil))
	if err == nil || !strings.Contains(err.Error(), "request_timeout") {
		t.Fatalf("expected request_timeout parse error, got %v", err)
	}
}
