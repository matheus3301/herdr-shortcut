# Contributing to herdr-shortcut

Thanks for your interest in improving `herdr-shortcut`! This document explains
how to set up a development environment and the quality bar for changes.

## Prerequisites

- Go 1.26 — the single supported and tested version, pinned via `mise.toml`
  (run `mise install`, or prefix commands with `mise exec --`).
- A POSIX shell (`sh`) for the build and verification scripts.
- Optionally, [Herdr](https://herdr.dev) v0.7.5+ for end-to-end verification.
  `make verify-plugin` can download an official Herdr binary if one is not
  installed.

## Getting started

```sh
git clone https://github.com/matheus3301/herdr-shortcut
cd herdr-shortcut
make check
```

`make check` runs every local quality gate that does not require network access
or credentials: formatting, `go mod tidy` verification, `go vet`, race tests,
coverage, build, and plugin-manifest verification.

## Development workflow

Common targets (`make help` lists them all):

```sh
make fmt          # gofmt -w
make vet          # go vet ./...
make test         # go test ./...
make test-race    # go test -race ./...
make coverage     # write coverage.txt and enforce the 80% threshold
make build        # build ./bin/herdr-shortcut
make verify-plugin
make smoke-nogo   # test the no-Go install fallback
make check        # the full local gate
```

## Quality bar

- Keep the code small and idiomatic. Do not introduce an abstract framework or a
  generic repository layer.
- The API, Herdr, configuration, prompt, and UI boundaries must stay separately
  testable.
- No panics from user input, API data, terminal dimensions, missing environment,
  or malformed subprocess output.
- Never invoke a shell for credentials, Herdr commands, browser URLs, or prompts.
- Never place a secret in the repository, fixtures, logs, prompts, snapshots, or
  CI configuration. Tests must not require a real token, network, Herdr session,
  or coding-agent account.
- No TODO/FIXME placeholders, disabled tests, or documentation for behavior that
  does not exist.
- New behavior needs deterministic tests. Aggregate statement coverage must stay
  at or above 80%.

## Releasing

Releases are cut by pushing a `vX.Y.Z` tag that must be an **annotated or signed
tag object** (the release workflow rejects lightweight tags):

```sh
git tag -a v0.1.1 -m "herdr-shortcut v0.1.1"   # or: git tag -s v0.1.1 -m ...
git push origin v0.1.1
```

The tag version must equal the version in `herdr-plugin.toml` and
`internal/buildinfo`; the workflow verifies this, runs the full gates and
`goreleaser check`, then publishes archives, `checksums.txt`, and an SBOM. CI
never bumps versions or pushes commits.

## Commit messages

We use [Conventional Commits](https://www.conventionalcommits.org/): `feat:`,
`fix:`, `docs:`, `test:`, `refactor:`, `chore:`, `ci:`.

## Pull requests

1. Fork and branch from `main`.
2. Make focused changes with tests.
3. Run `make check` and ensure it passes.
4. Open a PR using the pull-request template and describe the change and its
   verification.

By contributing you agree that your contributions are licensed under the
project's [MIT License](LICENSE).
