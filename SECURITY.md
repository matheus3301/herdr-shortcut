# Security Policy

## Supported Versions

The latest tagged release of `herdr-shortcut` receives security fixes. This is a
pre-1.0 project, so only the most recent minor version is supported.

| Version | Supported |
| ------- | --------- |
| 0.1.x   | ✅        |

## Reporting a Vulnerability

Please **do not** open a public issue for security vulnerabilities.

Use GitHub's [private vulnerability reporting][ghsa] on this repository
("Security" tab → "Report a vulnerability"). If that is unavailable, open a
minimal public issue asking a maintainer to open a private advisory, without
including exploit details.

We aim to acknowledge reports within 7 days and to ship a fix or mitigation for
confirmed issues as quickly as is practical.

[ghsa]: https://github.com/matheus3301/herdr-shortcut/security/advisories/new

## Credential Model

`herdr-shortcut` is a read-only Shortcut client and a Herdr launcher. Its
security posture centers on never leaking your Shortcut API token:

- The token is resolved at runtime from either the `SHORTCUT_API_TOKEN`
  environment variable or a configured `token_command` (an argv array executed
  directly, never through a shell).
- The token is held only in memory for the duration of a command and is sent to
  the Shortcut API exclusively through the `Shortcut-Token` request header over
  HTTPS.
- The token is **never** written to configuration files, plugin state, logs,
  error messages, debug output, test snapshots, process arguments, environment
  variables passed to child processes, or the generated agent prompt.
- Plaintext token fields in TOML are intentionally unsupported.
- The tool performs no analytics, telemetry, update checks, or other hidden
  network requests. Network access is limited to the configured Shortcut API
  base URL and explicit browser opens of `https://` Shortcut Story URLs.

If you discover a code path that can expose the token, please treat it as a
security vulnerability and report it privately as described above.
