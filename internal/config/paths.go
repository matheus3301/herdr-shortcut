package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// ExpandPath expands a leading "~" and environment variables in p using env.
// It cleans the result but does not resolve symlinks and does not require the
// path to exist. Stored configuration paths are never mutated by this function.
func ExpandPath(p string, env func(string) string) (string, error) {
	if env == nil {
		env = os.Getenv
	}
	// Expand environment variables, rejecting any referenced variable that is
	// unset (which would otherwise silently collapse to an empty path segment).
	var missing []string
	expanded := os.Expand(p, func(name string) string {
		v := env(name)
		if v == "" {
			missing = append(missing, name)
		}
		return v
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("unresolved environment variable(s) %s in path %q", strings.Join(missing, ", "), p)
	}
	if expanded == "~" || strings.HasPrefix(expanded, "~/") {
		home := env("HOME")
		if home == "" {
			return "", fmt.Errorf("cannot expand %q because HOME is not set", p)
		}
		if expanded == "~" {
			expanded = home
		} else {
			expanded = filepath.Join(home, expanded[len("~/"):])
		}
	}
	if strings.TrimSpace(expanded) == "" {
		return "", fmt.Errorf("path %q is empty after expansion", p)
	}
	return filepath.Clean(expanded), nil
}

// isLoopbackHost reports whether host names the loopback interface. HTTP is
// permitted only for these hosts so local test servers work while production
// traffic stays on HTTPS.
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
