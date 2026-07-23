// Package childenv builds sanitized environments for child processes so a
// spawned command (Herdr CLI, browser opener, token command) never inherits the
// Shortcut API token from the parent process environment.
package childenv

import "strings"

// sensitive names are stripped from every child process environment.
var sensitive = map[string]struct{}{
	"SHORTCUT_API_TOKEN": {},
}

// Sanitized returns environ with sensitive variables removed. Pass os.Environ().
func Sanitized(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		name := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			name = kv[:i]
		}
		if _, bad := sensitive[name]; bad {
			continue
		}
		out = append(out, kv)
	}
	return out
}
