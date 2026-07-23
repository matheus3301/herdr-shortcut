package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// canonicalDir validates that path exists and is a directory, then returns its
// absolute, symlink-resolved (canonical) form. Stored config is never mutated.
// It fails closed if the path cannot be made absolute or its symlinks cannot be
// resolved, so a stale or vanished cwd is caught immediately before any tab is
// created.
func canonicalDir(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("cannot resolve path %q: %v", path, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("path does not exist: %s", abs)
		}
		return "", fmt.Errorf("cannot access path: %v", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("not a directory: %s", abs)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("cannot resolve symlinks for %s: %v", abs, err)
	}
	return resolved, nil
}

// validateDir revalidates and canonicalizes a selected cwd immediately before it
// is used. It honors an injected validator so tests can drive launches without a
// real filesystem, defaulting to canonicalDir otherwise.
func (a *App) validateDir(path string) (string, error) {
	if a.env.DirValidator != nil {
		return a.env.DirValidator(path)
	}
	return canonicalDir(path)
}
