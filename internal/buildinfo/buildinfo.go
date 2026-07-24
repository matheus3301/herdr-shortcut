// Package buildinfo exposes the single source of truth for the herdr-shortcut
// version. The plugin manifest and release tag are checked against it by tests.
package buildinfo

// Version is the semantic version of herdr-shortcut. It must match the version
// field in herdr-plugin.toml and the release tag (enforced by tests).
const Version = "0.1.1"

// Name is the program name.
const Name = "herdr-shortcut"

// UserAgent is the HTTP User-Agent sent to the Shortcut API. It is versioned so
// Shortcut can attribute traffic to this plugin.
const UserAgent = Name + "/" + Version + " (+https://github.com/matheus3301/herdr-shortcut)"
