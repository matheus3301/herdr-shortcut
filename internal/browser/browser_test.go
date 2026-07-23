package browser

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestMain lets this binary re-exec itself as a fake browser command that
// records whether it inherited SHORTCUT_API_TOKEN.
func TestMain(m *testing.M) {
	if len(os.Args) >= 3 && os.Args[1] == "__browser_env_helper__" {
		status := "UNSET"
		if v, ok := os.LookupEnv("SHORTCUT_API_TOKEN"); ok {
			status = v
		}
		_ = os.WriteFile(os.Args[2], []byte(status), 0o600)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestDefaultRunScrubsToken(t *testing.T) {
	// Not parallel: mutates the process environment.
	t.Setenv("SHORTCUT_API_TOKEN", "browser-secret")
	out := filepath.Join(t.TempDir(), "env.out")
	if err := defaultRun(context.Background(), os.Args[0], []string{"__browser_env_helper__", out}); err != nil {
		t.Fatalf("defaultRun: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(data), "browser-secret") {
		t.Fatalf("browser child inherited the token: %q", data)
	}
}

func TestDefaultRunHonorsContextCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	out := filepath.Join(t.TempDir(), "env.out")
	if err := defaultRun(ctx, os.Args[0], []string{"__browser_env_helper__", out}); err == nil {
		t.Fatal("expected a cancelled context to abort the browser command")
	}
}

func TestValidateURL(t *testing.T) {
	t.Parallel()
	valid := []string{
		"https://app.shortcut.com/acme/story/42/fix-bug",
		"https://shortcut.com/x/story/1",
		"https://app.shortcut.com/o/story/999/",
	}
	for _, u := range valid {
		if err := ValidateURL(u); err != nil {
			t.Errorf("ValidateURL(%q) unexpected error: %v", u, err)
		}
	}
	invalid := []string{
		"http://app.shortcut.com/o/story/1", // not https
		"file:///etc/passwd",
		"javascript:alert(1)",
		"https://evil.com/o/story/1",                    // wrong host
		"https://app.shortcut.com.evil.com/story/1",     // suffix trick
		"https://app.shortcut.com/o/epic/1",             // not a story
		"https://app.shortcut.com/o/story/abc",          // non-numeric id
		"https://app.shortcut.com/o/story",              // no id
		"https://app.shortcut.com/story-map/1",          // not the story route
		"https://user:pass@app.shortcut.com/o/story/1",  // embedded userinfo
		"https://app.shortcut.com:8443/o/story/1",       // non-default port
		"https://app.shortcut.com/o/story/1/../../evil", // path traversal
		"https://app.shortcut.com/o/story/1/%2e%2e",     // encoded traversal
		"https://app.shortcut.com/o/story/1/slug/extra", // extra route segment
		"https://app.shortcut.com/o/story/1?from=email", // non-canonical query
		"https://app.shortcut.com/o/story/1#details",    // non-canonical fragment
		"https://app.shortcut.com/story/1",              // missing workspace segment
		"",
	}
	for _, u := range invalid {
		if err := ValidateURL(u); err == nil {
			t.Errorf("ValidateURL(%q) should have failed", u)
		}
	}
}

func TestOpenDispatch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		goos     string
		wantName string
	}{
		{"darwin", "open"},
		{"linux", "xdg-open"},
	}
	for _, tc := range tests {
		var gotName string
		var gotArgs []string
		o := Opener{GOOS: tc.goos, Run: func(_ context.Context, name string, args []string) error {
			gotName = name
			gotArgs = args
			return nil
		}}
		url := "https://app.shortcut.com/o/story/7/x"
		if err := o.Open(context.Background(), url); err != nil {
			t.Fatalf("Open on %s: %v", tc.goos, err)
		}
		if gotName != tc.wantName {
			t.Errorf("%s: command = %q, want %q", tc.goos, gotName, tc.wantName)
		}
		if !reflect.DeepEqual(gotArgs, []string{url}) {
			t.Errorf("%s: args = %v", tc.goos, gotArgs)
		}
	}
}

func TestOpenUnsupportedOS(t *testing.T) {
	t.Parallel()
	o := Opener{GOOS: "windows", Run: func(context.Context, string, []string) error { return nil }}
	if err := o.Open(context.Background(), "https://app.shortcut.com/o/story/7"); err == nil {
		t.Fatal("expected unsupported-OS error")
	}
}

func TestOpenRejectsInvalidBeforeRunning(t *testing.T) {
	t.Parallel()
	ran := false
	o := Opener{GOOS: "darwin", Run: func(context.Context, string, []string) error { ran = true; return nil }}
	if err := o.Open(context.Background(), "file:///etc/passwd"); err == nil {
		t.Fatal("expected validation error")
	}
	if ran {
		t.Fatal("Run must not be called for an invalid URL")
	}
}

func TestOpenSurfacesRunError(t *testing.T) {
	t.Parallel()
	o := Opener{GOOS: "linux", Run: func(context.Context, string, []string) error { return errors.New("boom") }}
	err := o.Open(context.Background(), "https://app.shortcut.com/o/story/7")
	if err == nil {
		t.Fatal("expected run error to surface")
	}
}
