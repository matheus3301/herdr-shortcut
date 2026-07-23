package app

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/matheus3301/herdr-shortcut/internal/buildinfo"
	"github.com/matheus3301/herdr-shortcut/internal/config"
	"github.com/matheus3301/herdr-shortcut/internal/prompt"
)

// repoRoot locates the repository root from this test file's location so the
// test is independent of the working directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

type manifest struct {
	ID              string   `toml:"id"`
	Name            string   `toml:"name"`
	Version         string   `toml:"version"`
	MinHerdrVersion string   `toml:"min_herdr_version"`
	Platforms       []string `toml:"platforms"`
	Build           []struct {
		Command   []string `toml:"command"`
		Platforms []string `toml:"platforms"`
	} `toml:"build"`
	Actions []struct {
		ID       string   `toml:"id"`
		Title    string   `toml:"title"`
		Contexts []string `toml:"contexts"`
		Command  []string `toml:"command"`
	} `toml:"actions"`
	Panes []struct {
		ID        string   `toml:"id"`
		Title     string   `toml:"title"`
		Placement string   `toml:"placement"`
		Width     string   `toml:"width"`
		Height    string   `toml:"height"`
		Command   []string `toml:"command"`
	} `toml:"panes"`
}

func loadManifest(t *testing.T) manifest {
	t.Helper()
	var m manifest
	if _, err := toml.DecodeFile(filepath.Join(repoRoot(t), "herdr-plugin.toml"), &m); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	return m
}

func TestManifestAgreement(t *testing.T) {
	t.Parallel()
	m := loadManifest(t)

	if m.ID != PluginID {
		t.Errorf("manifest id %q != app.PluginID %q", m.ID, PluginID)
	}
	if m.Name != "Shortcut" {
		t.Errorf("manifest name = %q", m.Name)
	}
	if m.Version != buildinfo.Version {
		t.Errorf("manifest version %q != buildinfo.Version %q", m.Version, buildinfo.Version)
	}
	if m.MinHerdrVersion != "0.7.5" {
		t.Errorf("min_herdr_version = %q, want 0.7.5", m.MinHerdrVersion)
	}
	if !reflect.DeepEqual(m.Platforms, []string{"linux", "macos"}) {
		t.Errorf("platforms = %v", m.Platforms)
	}

	if len(m.Build) != 1 || !reflect.DeepEqual(m.Build[0].Command, []string{"sh", "scripts/build.sh"}) {
		t.Errorf("build command = %+v", m.Build)
	}

	actions := map[string]struct {
		Title    string
		Contexts []string
		Command  []string
	}{}
	for _, a := range m.Actions {
		actions[a.ID] = struct {
			Title    string
			Contexts []string
			Command  []string
		}{a.Title, a.Contexts, a.Command}
	}
	open, hasOpen := actions[ActionOpen]
	if !hasOpen || open.Title != "Shortcut: My tasks" {
		t.Errorf("open action = %+v", open)
	}
	if !reflect.DeepEqual(open.Contexts, []string{"workspace", "tab", "pane"}) {
		t.Errorf("open contexts = %v", open.Contexts)
	}
	if !reflect.DeepEqual(open.Command, []string{"./bin/herdr-shortcut", "open"}) {
		t.Errorf("open command = %v", open.Command)
	}
	doctor, hasDoctor := actions["doctor"]
	if !hasDoctor || !reflect.DeepEqual(doctor.Command, []string{"./bin/herdr-shortcut", "doctor"}) {
		t.Errorf("doctor action = %+v (present=%v)", doctor, hasDoctor)
	}

	if len(m.Panes) != 1 {
		t.Fatalf("expected 1 pane, got %d", len(m.Panes))
	}
	p := m.Panes[0]
	if p.ID != PaneEntrypoint || p.Placement != "popup" || p.Width != "90%" || p.Height != "80%" {
		t.Errorf("pane = %+v", p)
	}
	if !reflect.DeepEqual(p.Command, []string{"./bin/herdr-shortcut", "tui"}) {
		t.Errorf("pane command = %v", p.Command)
	}
}

func TestConfigExampleMatchesDefaults(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "config.example.toml"))
	if err != nil {
		t.Fatalf("read config.example.toml: %v", err)
	}
	got, err := config.LoadData(data, func(string) string { return "/home/example" })
	if err != nil {
		t.Fatalf("config.example.toml must be valid: %v", err)
	}
	def := config.Default()
	if got.Shortcut.APIBaseURL != def.Shortcut.APIBaseURL ||
		got.Shortcut.Query != def.Shortcut.Query ||
		got.Shortcut.PageSize != def.Shortcut.PageSize ||
		got.Shortcut.MaxStories != def.Shortcut.MaxStories ||
		got.Shortcut.RequestTimeout != def.Shortcut.RequestTimeout {
		t.Errorf("example shortcut section drifted from defaults:\n got %+v\n def %+v", got.Shortcut, def.Shortcut)
	}
	if got.Agent.DefaultKind != def.Agent.DefaultKind ||
		got.Agent.NameTemplate != def.Agent.NameTemplate ||
		got.Agent.TabLabelTemplate != def.Agent.TabLabelTemplate ||
		got.Agent.Focus != def.Agent.Focus {
		t.Errorf("example agent section drifted from defaults")
	}
	if got.Agent.PromptTemplate != prompt.DefaultTemplate {
		t.Errorf("example prompt_template does not match prompt.DefaultTemplate")
	}
}

func TestGoreleaserArchiveNameMatchesBuildScript(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	gr, err := os.ReadFile(filepath.Join(root, ".goreleaser.yml"))
	if err != nil {
		t.Fatalf("read .goreleaser.yml: %v", err)
	}
	// The archive name template must match what scripts/build.sh downloads:
	// herdr-shortcut_<version>_<os>_<arch>.tar.gz
	wantTemplate := `{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}`
	if !strings.Contains(string(gr), wantTemplate) {
		t.Errorf(".goreleaser.yml archive name_template must be %q", wantTemplate)
	}
	if !strings.Contains(string(gr), "checksums.txt") {
		t.Error(".goreleaser.yml must produce checksums.txt")
	}
	// The SBOM key must be the valid plural `sboms:` list, not the rejected
	// singular `sbom:`.
	if !strings.Contains(string(gr), "\nsboms:") {
		t.Error(".goreleaser.yml must use the 'sboms:' key")
	}
	if strings.Contains(string(gr), "\nsbom:") {
		t.Error(".goreleaser.yml must not use the invalid 'sbom:' key")
	}

	build, err := os.ReadFile(filepath.Join(root, "scripts", "build.sh"))
	if err != nil {
		t.Fatalf("read build.sh: %v", err)
	}
	if !strings.Contains(string(build), `${BIN_NAME}_${VERSION}_${GOOS}_${GOARCH}.tar.gz`) {
		t.Error("build.sh archive name must match the GoReleaser template")
	}
	if !strings.Contains(string(build), "checksums.txt") {
		t.Error("build.sh must verify checksums.txt")
	}
}

func TestManifestVersionMatchesReleaseTagFormat(t *testing.T) {
	t.Parallel()
	// The release workflow requires the tag (vX.Y.Z) to equal v + manifest version.
	m := loadManifest(t)
	if m.Version != buildinfo.Version {
		t.Fatalf("manifest %q vs buildinfo %q", m.Version, buildinfo.Version)
	}
	// Sanity: semantic version shape.
	parts := strings.Split(m.Version, ".")
	if len(parts) != 3 {
		t.Errorf("version %q is not semantic", m.Version)
	}
}
