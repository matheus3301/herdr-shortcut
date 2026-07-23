// Package browser opens Shortcut Story URLs in the operating system browser
// without invoking a shell. Only canonical https Shortcut Story URLs are
// permitted.
package browser

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"

	"github.com/matheus3301/herdr-shortcut/internal/childenv"
)

// storyPath matches a canonical Shortcut Story route anchored to the whole path:
// a single workspace segment, then "/story/<numeric-id>", then an optional slug
// segment. Anchoring rejects extra routes and embedded story-like paths.
var storyPath = regexp.MustCompile(`^/[^/]+/story/[0-9]+(?:/[^/]*)?$`)

// Opener opens URLs using the OS default handler. GOOS and Run are injectable
// for deterministic tests. Run must honor ctx for cancellation.
type Opener struct {
	GOOS string
	Run  func(ctx context.Context, name string, args []string) error
}

// Open validates rawURL and launches the platform browser command. It honors ctx
// so a superseded or shutdown open can be cancelled.
func (o Opener) Open(ctx context.Context, rawURL string) error {
	if err := ValidateURL(rawURL); err != nil {
		return err
	}
	goos := o.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	var name string
	switch goos {
	case "darwin":
		name = "open"
	case "linux":
		name = "xdg-open"
	default:
		return fmt.Errorf("opening a browser is not supported on %s", goos)
	}
	run := o.Run
	if run == nil {
		run = defaultRun
	}
	if err := run(ctx, name, []string{rawURL}); err != nil {
		return fmt.Errorf("could not open browser (%s): %w", name, err)
	}
	return nil
}

// ValidateURL permits only strictly canonical https Shortcut Story URLs. It
// rejects non-https schemes, userinfo, non-default ports, path traversal, extra
// routes, and non-Shortcut hosts, so it can never open an arbitrary or
// credential-carrying URL.
func ValidateURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %v", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("refusing to open non-https URL %q", rawURL)
	}
	if u.User != nil {
		return fmt.Errorf("refusing to open URL with embedded userinfo %q", rawURL)
	}
	host := strings.ToLower(u.Hostname())
	if host != "shortcut.com" && !strings.HasSuffix(host, ".shortcut.com") {
		return fmt.Errorf("refusing to open non-Shortcut host %q", host)
	}
	if u.Port() != "" {
		return fmt.Errorf("refusing to open URL with a non-default port %q", rawURL)
	}
	for _, seg := range strings.Split(u.Path, "/") {
		if seg == "." || seg == ".." {
			return fmt.Errorf("refusing to open URL with path traversal %q", rawURL)
		}
	}
	if !storyPath.MatchString(u.EscapedPath()) {
		return fmt.Errorf("URL %q is not a canonical Shortcut Story URL", rawURL)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("URL %q is not a canonical Shortcut Story URL", rawURL)
	}
	return nil
}

func defaultRun(ctx context.Context, name string, args []string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	// The browser child never inherits the Shortcut token.
	cmd.Env = childenv.Sanitized(os.Environ())
	return cmd.Run()
}
