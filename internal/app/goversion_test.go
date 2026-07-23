package app

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// supportedGoVersion is the single Go minor series this repository supports and
// tests. Every version reference — mise.toml, go.mod, the install script, both
// GitHub workflows, and the docs — must agree on it. These tests fail loudly if
// any of them drifts, so a future toolchain bump has to be made everywhere at
// once.
const supportedGoVersion = "1.26"

func repoFileText(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// goMinorSeries reduces a Go version token ("1.26", "1.26.5", "'1.26'",
// "go1.26.0") to its major.minor series ("1.26").
func goMinorSeries(v string) string {
	v = strings.Trim(strings.TrimSpace(v), "'\"")
	v = strings.TrimPrefix(v, "go")
	v = strings.TrimSpace(v)
	parts := strings.Split(v, ".")
	if len(parts) < 2 {
		return v
	}
	return parts[0] + "." + parts[1]
}

func TestGoVersionMiseToml(t *testing.T) {
	t.Parallel()
	var cfg struct {
		Tools struct {
			Go string `toml:"go"`
		} `toml:"tools"`
	}
	if _, err := toml.DecodeFile(filepath.Join(repoRoot(t), "mise.toml"), &cfg); err != nil {
		t.Fatalf("decode mise.toml: %v", err)
	}
	if cfg.Tools.Go == "" {
		t.Fatal("mise.toml must pin the Go toolchain under [tools] go")
	}
	if got := goMinorSeries(cfg.Tools.Go); got != supportedGoVersion {
		t.Errorf("mise.toml [tools] go = %q (series %q), want %q", cfg.Tools.Go, got, supportedGoVersion)
	}
}

func TestGoVersionGoMod(t *testing.T) {
	t.Parallel()
	src := repoFileText(t, "go.mod")
	m := regexp.MustCompile(`(?m)^go\s+(\S+)`).FindStringSubmatch(src)
	if m == nil {
		t.Fatal("go.mod has no go directive")
	}
	if got := goMinorSeries(m[1]); got != supportedGoVersion {
		t.Errorf("go.mod go directive %q (series %q), want %q", m[1], got, supportedGoVersion)
	}
	// The go directive plus mise.toml pin the version; a toolchain directive would
	// pin a specific patch and drift from CI, so it must stay absent.
	if regexp.MustCompile(`(?m)^toolchain\s`).MatchString(src) {
		t.Error("go.mod must not contain a toolchain directive")
	}
}

func TestGoVersionBuildScriptMinimum(t *testing.T) {
	t.Parallel()
	src := repoFileText(t, "scripts/build.sh")
	m := regexp.MustCompile(`(?m)^MIN_GO_MINOR=(\d+)`).FindStringSubmatch(src)
	if m == nil {
		t.Fatal("scripts/build.sh has no MIN_GO_MINOR")
	}
	wantMinor := strings.SplitN(supportedGoVersion, ".", 2)[1]
	if m[1] != wantMinor {
		t.Errorf("scripts/build.sh MIN_GO_MINOR=%s, want %s (Go %s)", m[1], wantMinor, supportedGoVersion)
	}
}

func TestGoVersionWorkflows(t *testing.T) {
	t.Parallel()
	re := regexp.MustCompile(`go-version:\s*['"]?([^'"\s]+)`)
	for _, wf := range []string{".github/workflows/ci.yml", ".github/workflows/release.yml"} {
		src := repoFileText(t, wf)
		matches := re.FindAllStringSubmatch(src, -1)
		if len(matches) == 0 {
			t.Errorf("%s: found no go-version entries", wf)
			continue
		}
		for _, m := range matches {
			v := m[1]
			if v == "stable" || v == "oldstable" {
				t.Errorf("%s: go-version %q must be pinned to %s", wf, v, supportedGoVersion)
				continue
			}
			if got := goMinorSeries(v); got != supportedGoVersion {
				t.Errorf("%s: go-version %q (series %q), want %q", wf, v, got, supportedGoVersion)
			}
		}
	}
}

func TestGoVersionDocs(t *testing.T) {
	t.Parallel()
	// Human-facing docs (the "manifest" of what we support) must advertise the
	// supported version and must not still reference the retired Go 1.22.
	for _, doc := range []string{"README.md", "CONTRIBUTING.md", "SPEC.md"} {
		src := repoFileText(t, doc)
		if !strings.Contains(src, "Go "+supportedGoVersion) {
			t.Errorf("%s does not document Go %s", doc, supportedGoVersion)
		}
		if strings.Contains(src, "1.22") {
			t.Errorf("%s still references the retired Go 1.22", doc)
		}
	}
	// The README Go badge must point at the supported version.
	readme := repoFileText(t, "README.md")
	if !strings.Contains(readme, "badge/go-"+supportedGoVersion) {
		t.Errorf("README Go badge must be badge/go-%s", supportedGoVersion)
	}
}
