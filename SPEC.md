# herdr-shortcut Product and Implementation Specification

Status: implementation contract for v0.1.0

Repository: `https://github.com/matheus3301/herdr-shortcut`

## 1. Mission

Build a polished Herdr plugin, written in Go, that shows the authenticated
Shortcut user's active Stories in a fast terminal UI and launches any coding
agent harness supported by the installed Herdr binary to work on the selected
Story.

The finished product must be installable as a normal Herdr community plugin:

```sh
herdr plugin install matheus3301/herdr-shortcut
```

It must be useful without editing Herdr itself, must never persist or print the
Shortcut token, and must work on macOS and Linux on amd64 and arm64.

## 2. Primary User Flow

1. The user invokes the `matheus3301.shortcut.open` plugin action.
2. Herdr opens a focused popup containing the Shortcut task picker.
3. The plugin authenticates to Shortcut, resolves the current member, fetches
   that member's non-completed Stories, resolves workflow-state names, and shows
   the Stories in a responsive list.
4. The user can navigate with the keyboard or mouse, filter locally, refresh,
   inspect a Story in the browser, and choose a Story to work on.
5. Choosing a Story opens a launch dialog. The dialog selects an agent harness
   from the kinds reported by the installed Herdr binary, selects a working
   directory from the current Herdr context or configured repositories, and lets
   the user enter a custom directory.
6. Clicking the launch control or pressing Enter creates a new tab in the
   current Herdr workspace, starts the selected harness in that tab's root pane,
   and submits a rich prompt containing the selected Story's current details.
7. The popup exits, leaving the new agent tab focused and working.

No Shortcut write operation is in scope for v0.1.0. The plugin is a read-only
Shortcut client and a Herdr launcher.

## 3. Authoritative External Contracts

Implementation must follow these verified contracts as of 2026-07-23.

### Herdr v0.7.5

References:

- `https://herdr.dev/docs/plugins/`
- `https://herdr.dev/docs/cli-reference/`
- `https://herdr.dev/docs/socket-api/`
- `https://herdr.dev/docs/agent-automation/`
- `https://github.com/ogulcancelik/herdr/tree/v0.7.5`

Relevant guarantees:

- A plugin is a directory with `herdr-plugin.toml` and out-of-process argv
  commands. There is no plugin SDK.
- Runtime commands receive `HERDR_BIN_PATH`, `HERDR_PLUGIN_ROOT`,
  `HERDR_PLUGIN_CONFIG_DIR`, `HERDR_PLUGIN_STATE_DIR`, and
  `HERDR_PLUGIN_CONTEXT_JSON`.
- Invocation context fields include `workspace_id`, `workspace_cwd`, `tab_id`,
  `focused_pane_id`, and `focused_pane_cwd` when available.
- `herdr plugin pane open` can open a manifest pane as a `popup`; popup panes do
  not alter tiled layout and close when the command exits.
- `herdr tab create --workspace ID --cwd PATH --label LABEL --focus` returns
  `.result.tab.tab_id` and `.result.root_pane.pane_id`.
- `herdr agent start NAME --kind KIND --pane ID -- [native args...]` starts the
  selected supported harness in an existing available shell pane and returns
  only when the agent is detected and ready.
- `herdr agent prompt NAME TEXT` atomically submits a prompt.
- Live agent names match `[a-z][a-z0-9_-]{0,31}` and must be unique.
- Running bare `herdr agent` is the installed binary's authoritative discovery
  surface and prints a machine-parseable `kinds:` line. Herdr v0.7.5 reports
  `pi|claude|codex|gemini|cursor|devin|agy|cline|omp|mastracode|opencode|copilot|kimi|kiro|droid|amp|grok|hermes|kilo|qodercli|maki`.
- Plugins should call the injected `HERDR_BIN_PATH` instead of assuming `herdr`
  is on `PATH`.

Set `min_herdr_version = "0.7.5"`. The implementation relies on the v0.7.5
`agent start --kind --pane` and `agent prompt` contracts.

### Shortcut REST API v3

References:

- `https://developer.shortcut.com/api/rest/v3`
- `https://developer.shortcut.com/api/rest/v3/shortcut.openapi.json`
- `https://www.shortcut.com/help/fields-and-features/search-operators`

Relevant guarantees:

- Base URL: `https://api.app.shortcut.com/api/v3`.
- Authentication header: `Shortcut-Token: <token>`.
- `GET /member` returns the authenticated member's UUID and `mention_name`.
- `GET /workflows` returns workflows and their states, including state ID,
  name, type, and position.
- `GET /search/stories` accepts `query`, `page_size` (1-250), `detail`, and a
  `next` token. Its response has `data`, `total`, and `next`; no more than 1,000
  search matches can be paged.
- `GET /stories/{id}` returns the complete current Story.
- The API rate limit is 200 requests per minute. A limit breach returns 429.
- Search operators use AND semantics. `owner:<mention_name>`, `is:story`,
  `!is:done`, and `!is:archived` are valid.

The default query template is:

```text
owner:{member} is:story !is:done !is:archived
```

`{member}` is replaced with the authenticated member's mention name. Quote and
escape the value safely if needed.

## 4. Repository Deliverables

The repository must contain at least:

```text
.
|-- .github/
|   |-- dependabot.yml
|   |-- ISSUE_TEMPLATE/
|   |-- pull_request_template.md
|   `-- workflows/
|       |-- ci.yml
|       `-- release.yml
|-- cmd/herdr-shortcut/main.go
|-- internal/
|   |-- app/          # orchestration/use cases
|   |-- config/       # config loading and token resolution
|   |-- herdr/        # typed Herdr CLI adapter
|   |-- prompt/       # deterministic prompt rendering
|   |-- shortcut/     # typed Shortcut HTTP client
|   `-- tui/          # Bubble Tea model, rendering, and hit testing
|-- scripts/
|   |-- build.sh
|   `-- verify-plugin.sh
|-- .gitignore
|-- .goreleaser.yml
|-- CODE_OF_CONDUCT.md
|-- CONTRIBUTING.md
|-- LICENSE
|-- Makefile
|-- README.md
|-- SECURITY.md
|-- SPEC.md
|-- config.example.toml
|-- go.mod
|-- go.sum
`-- herdr-plugin.toml
```

The exact internal file split may change if a simpler structure is clearer, but
the API, Herdr, configuration, prompt, and UI boundaries must remain separately
testable. Do not create an abstract framework or generic repository layer.

## 5. Plugin Manifest

Create a valid `herdr-plugin.toml` with:

- ID `matheus3301.shortcut`.
- Name `Shortcut`.
- Version `0.1.0`.
- Minimum Herdr version `0.7.5`.
- Platforms `linux` and `macos`.
- A Unix build command: `sh scripts/build.sh`.
- An action with local ID `open`, title `Shortcut: My tasks`, contexts
  `workspace`, `tab`, and `pane`, invoking `./bin/herdr-shortcut open`.
- A pane with local ID `tasks`, title `Shortcut`, placement `popup`, width
  `90%`, height `80%`, invoking `./bin/herdr-shortcut tui`.

Do not claim Windows support in v0.1.0. Do not install a default global
keybinding that may conflict with the user's configuration. Document an optional
keybinding in the README.

The `open` subcommand must invoke the pane through `HERDR_BIN_PATH` using argv,
not a shell. Preserve the original action context by explicitly passing the
available workspace, target pane, and cwd to `plugin pane open` where supported.

## 6. Command-Line Surface

The binary must expose:

```text
herdr-shortcut open
herdr-shortcut tui
herdr-shortcut doctor
herdr-shortcut version
herdr-shortcut help
```

Requirements:

- `open` opens or focuses the managed task-picker popup.
- `tui` runs the interactive picker and is the manifest pane entrypoint.
- `doctor` validates configuration, token resolution, Shortcut authentication,
  required Herdr context, supported agent-kind discovery through Herdr, the
  configured default kind, and configured repository directories. It prints no
  secret values.
- `version` prints a single stable machine-readable line containing the semantic
  version.
- Invalid commands and invalid configuration exit nonzero with concise help.
- `--version`, `-v`, `--help`, and `-h` behave conventionally.

Keep argument parsing in the standard library unless a dependency clearly
reduces code and test burden.

## 7. Configuration and Credentials

Load TOML from:

1. `$HERDR_PLUGIN_CONFIG_DIR/config.toml` when Herdr provides the directory.
2. `$XDG_CONFIG_HOME/herdr-shortcut/config.toml` outside Herdr.
3. `$HOME/.config/herdr-shortcut/config.toml` as the final fallback.

Ship `config.example.toml`. Missing config is valid: defaults must be usable if
`SHORTCUT_API_TOKEN` is present and the current Herdr pane has a cwd.

Support this schema:

```toml
[shortcut]
api_base_url = "https://api.app.shortcut.com/api/v3"
query = "owner:{member} is:story !is:done !is:archived"
page_size = 100
max_stories = 250
request_timeout = "15s"
token_command = []

[agent]
default_kind = "claude"
name_template = "sc-{id}-{kind}"
tab_label_template = "SC-{id} {kind}"
focus = true
prompt_template = """...documented default..."""

[agent.args_by_kind]
claude = []
codex = []

[[repositories]]
name = "Main repository"
path = "~/src/main"
```

Token resolution precedence:

1. Non-empty `SHORTCUT_API_TOKEN`.
2. `shortcut.token_command`, represented as a TOML argv array and executed
   directly without a shell. Trim one trailing newline from stdout.

Do not support a plaintext token field in TOML. Do not place tokens in query
parameters. Do not persist tokens in plugin state. Never include a token or
token-command output in an error, log, debug string, test snapshot, process
argument, or prompt.

Validate all config values with actionable field-specific errors:

- API URL must be absolute HTTPS, except HTTP is allowed for loopback hosts in
  tests/development.
- Page size is 1-250.
- Max Stories is 1-1,000 and no lower than page size.
- Durations must be positive and bounded.
- `agent.default_kind` and every `agent.args_by_kind` key must be valid Herdr
  kind identifiers. Runtime validation must confirm the default against the
  kinds discovered from the installed Herdr binary.
- Native arguments are argv arrays scoped to one kind and are passed only to the
  selected harness.
- Repository names are non-empty, paths are non-empty, and duplicate expanded
  paths are rejected.
- Agent name and tab templates accept only `{id}` and `{kind}`. Prompt templates
  accept only their documented placeholders.

Expand `~` and environment variables in repository paths. Resolve symlinks only
when validating an existing selected cwd; do not mutate paths in stored config.

## 8. Shortcut Client

Use `net/http` with an injected `http.Client` and base URL. Do not use a generated
OpenAPI client for the small endpoint set.

Implement typed methods for:

- `CurrentMember(ctx)`.
- `Workflows(ctx)`.
- `MyStories(ctx, memberMentionName, queryTemplate, pageSize, maxStories)`.
- `Story(ctx, id)`.

HTTP requirements:

- Set `Accept: application/json`, `Content-Type: application/json`,
  `Shortcut-Token`, and a versioned `User-Agent`.
- Honor context cancellation and the configured timeout.
- Close response bodies and bound all body reads.
- Decode nullable Shortcut fields without failing valid responses.
- Return typed, human-readable errors for 401, 403, 404, 422, 429, malformed
  JSON, and network failures.
- Never expose response bodies without a small sanitized cap.
- Retry idempotent GETs for 429 and transient 5xx responses at most three times,
  honoring a bounded `Retry-After` value and using deterministic injectable
  sleeping for tests.
- Do not retry authentication or validation errors.

Pagination requirements:

- Follow Shortcut's `next` value until empty or `max_stories` is reached.
- `next` may be a path and query string. Resolve it against the configured base
  URL but reject any result whose scheme and host differ from that base URL so
  an API response cannot exfiltrate the auth header.
- Detect repeated next tokens and stop with an explicit pagination-loop error.
- Deduplicate Stories by ID while preserving stable order.
- Never request or return more than 1,000 Stories.

Fetch current member and workflows concurrently on initial load when doing so
keeps errors understandable. Resolve each Story's workflow-state ID to state
name, state type, and position. Unknown state IDs remain displayable as
`Unknown state` rather than failing the whole list.

Before launch, fetch the selected Story again with `GET /stories/{id}` so the
agent prompt receives current full details rather than stale list data.

## 9. TUI and Interaction Design

Use Bubble Tea and Lip Gloss. Keep visual design restrained and intentional:
Shortcut purple as the accent, strong selected-row contrast, clear state color,
and no gratuitous boxes around every element.

### Main View

The main view contains:

- Header: `Shortcut / My tasks`, authenticated member, result count, and a small
  loading/last-refreshed indicator.
- Search line: always available; `/` focuses it and typing filters locally by
  Story ID, title, state, type, and labels.
- Story list: ID, type, state, title, updated age, and optional estimate/deadline
  where width allows.
- Footer: only the currently relevant key hints.

Sort initial results deterministically:

1. Workflow state type (`started`, then `unstarted`/`backlog`, then unknown).
2. Workflow state position.
3. Deadline ascending with no deadline last.
4. Updated time descending.
5. Story ID ascending.

The UI must handle narrow and short terminals without panicking or rendering
negative dimensions. At narrow widths, drop optional columns before truncating
the title. At very small sizes, render a useful minimum-size message.

### Required States

Render explicit states for:

- Initial loading.
- Refreshing while preserving the old list.
- Missing credentials with exact setup commands.
- Empty assigned queue.
- API/auth/rate-limit/network error with retry guidance.
- Filter with no matches.
- Launching the selected agent harness.
- Launch failure without losing the selected Story.
- Launch success before clean exit.

### Keyboard

Implement at least:

- `j`/`k`, Down/Up: move selection.
- `g`/`G`, Home/End: first/last result.
- PageDown/PageUp: viewport movement.
- `/`: focus local search.
- Esc: clear/leave search, close dialog, or quit only at the main level.
- Enter: open launch dialog for selected Story.
- `o`: open selected Story's `app_url` in the OS browser.
- `r`: refresh API data.
- `?`: toggle help.
- `q`/Ctrl+C: quit when not editing a field, except while non-cancellable launch
  orchestration is in flight and irreversible Herdr resources may be created.

### Mouse

Enable Bubble Tea mouse cell motion/button events and implement stable hit
testing derived from rendered geometry:

- Wheel scroll moves the viewport.
- Clicking a visible Story row selects it and opens the launch dialog.
- Clicking the launch dialog's harness rows selects an agent kind.
- Clicking the launch dialog's repository rows selects a cwd.
- Clicking `Launch agent` launches the selected harness.
- Clicking `Cancel` returns to the list.
- Clicks outside known hit regions do nothing.

Hit testing must have unit tests for resized layouts, scrolled lists, header
offsets, and clicks outside content. Do not infer rows from hard-coded terminal
coordinates disconnected from the renderer.

### Launch Dialog

Show:

- Story ID and title.
- Agent-harness choices dynamically discovered from the installed Herdr binary,
  with the configured default selected initially. Every kind reported by Herdr
  must be available without an explicit plugin config entry.
- Cwd choices, in order: current focused-pane cwd, current workspace cwd,
  configured repositories, and `Custom path...`, deduplicated after expansion.
- The generated tab label and agent name, updated when the selected kind changes.
- `Launch agent` and `Cancel` controls usable by mouse and keyboard.

The harness and cwd regions must both be fully keyboard and mouse navigable in
short terminals. The UI must clearly distinguish the two selection regions and
show the currently selected kind. Kind discovery failure is an actionable Herdr
error, never a silent fallback to a stale hard-coded list.

Custom path input must validate that the path exists and is a directory before
launch. Preserve the dialog and show the error inline when validation fails.

## 10. Browser Opening

Open Story URLs without a shell:

- macOS: `open <url>`.
- Linux: `xdg-open <url>`.

Only allow `https://` Shortcut Story URLs returned by the API. Surface launcher
errors inside the TUI; never crash.

## 11. Prompt Rendering

Use a deterministic template with a documented, tested placeholder set:

```text
{id} {name} {url} {description} {story_type} {state} {labels}
{estimate} {deadline} {branch_name} {team_id} {epic_id} {kind}
```

The default prompt must be equivalent in intent to:

```text
Work on Shortcut Story SC-{id}: {name}
URL: {url}
Agent harness: {kind}
Type: {story_type}
State: {state}
Labels: {labels}
Suggested branch: {branch_name}

Description:
{description}

Inspect this repository and its instructions before changing anything. Implement
the Story end to end with the smallest correct change, add or update tests, run
the relevant verification, and summarize the result. Do not change the Shortcut
Story itself unless the user explicitly asks you to.
```

Requirements:

- Preserve meaningful multiline Markdown in the description.
- Normalize control characters that could manipulate a terminal.
- Cap each API-derived field and the final prompt to documented safe limits,
  preserving a clear truncation marker.
- Missing fields render as empty or `none`, never Go formatting artifacts.
- Template errors are configuration errors shown before launch.
- The prompt is passed as one argv value to `herdr agent prompt`; never through a
  shell, temporary world-readable file, or environment variable.

## 12. Herdr Adapter and Agent Launch

Implement a typed Herdr adapter around `exec.CommandContext`. The executable is
`HERDR_BIN_PATH`, falling back to `herdr` only for standalone development.

All commands must use argv and parse Herdr's JSON response envelopes. Preserve
Herdr error codes/messages without dumping unbounded output. The one plain-text
discovery call is bare `herdr agent`; parse and validate its `kinds:` line while
tolerating unrelated help lines. Preserve Herdr's reported order, reject empty or
invalid kind identifiers, deduplicate defensively, and do not substitute a
compiled-in list when discovery fails.

Launch algorithm:

1. Validate the selected kind against the kinds discovered from this installed
   Herdr binary, validate selected cwd, and render the prompt.
2. Read the active `workspace_id` from plugin context. If unavailable, query
   current Herdr state and return an actionable error if no workspace exists.
3. Query `herdr agent list` and generate a unique valid name from the configured
   `{id}`/`{kind}` template. Use `-2`, `-3`, and so on for collisions while
   staying within 32 characters.
4. Run `herdr tab create --workspace <workspace> --cwd <cwd> --label <label>
   --focus` (or `--no-focus` when configured).
5. Parse the new tab ID and root pane ID. Reject incomplete responses.
6. Run `herdr agent start <name> --kind <selected-kind> --pane <root-pane-id> --
   <arguments-configured-for-that-kind...>`.
7. Run `herdr agent prompt <name> <rendered-prompt>` without `--wait`.
8. Return a typed success containing tab ID, pane ID, and agent name. Exit the
   popup only after prompt submission succeeds.

Do not use `pane run` to start any harness; the v0.7.5 `agent start` facade is the
authoritative lifecycle-aware path for every supported kind.

Failure semantics:

- A failure before tab creation leaves no Herdr resources.
- A failure after tab creation reports the tab/pane IDs and does not close the
  tab automatically; closing could kill a partially started agent or hide useful
  diagnostics.
- A prompt failure keeps the agent tab and reports a copyable recovery command
  that omits credentials.
- The TUI remains usable after failure and allows retry or cancel.

## 13. Concurrency and Lifecycle

- Network calls and Herdr launches must run as Bubble Tea commands, never block
  the Update loop.
- Cancel obsolete in-flight list requests on refresh or shutdown.
- Ignore late responses using request generation IDs.
- Prevent duplicate launches while one launch is in progress.
- Avoid goroutine leaks, data races, and writes after model shutdown.
- Use injected clocks/sleepers/runners where needed for deterministic tests.

## 14. Build and Installation

The install-time `scripts/build.sh` must:

1. Build the exact checkout to `bin/herdr-shortcut` when a compatible Go
   toolchain is present.
2. Otherwise download the exact manifest version's release archive for the
   detected supported OS/architecture.
3. Download and verify the published SHA-256 checksum before installing the
   binary.
4. Fail closed on unsupported platforms, missing assets, checksum mismatch, or
   malformed version data.
5. Use POSIX `sh`, `set -eu`, temporary directories, and cleanup traps.
6. Never use `curl | sh`, `eval`, or an unverified executable.

The committed repository must not contain built binaries or release archives.

## 15. Testing Requirements

Use standard Go tests and `httptest`. Tests must be deterministic, parallel where
safe, and must not require a real Shortcut token, network, Herdr session, or a
real coding-agent account.

Required coverage areas:

- Config defaults, precedence, validation, path expansion, token-command argv,
  and secret-redaction guarantees.
- Shortcut headers, current member, workflows, Story decoding, nullable fields,
  query rendering, ordering, pagination, deduplication, max limits, repeated
  tokens, same-origin enforcement, cancellation, timeout, retry, and every
  important error status.
- Prompt placeholders, multiline descriptions, missing values, control
  characters, truncation, and invalid templates.
- Herdr command argv for open, tab creation, supported-kind discovery, agent
  listing, unique naming, agent start for every discovered kind, prompt
  submission, malformed JSON, nonzero exits, and partial failures.
- Main TUI loading, empty, error, refresh, filter, selection, dialog, custom cwd,
  launch progress/success/failure, resize, keyboard, wheel, and mouse hit testing.
- Browser opener OS dispatch and URL validation.
- Manifest parsing and agreement among manifest version, binary version, release
  config, action IDs, pane IDs, and build paths.
- Application-level table tests that drive a fake Shortcut client and fake Herdr
  runner from Story selection through exact start and prompt calls for every
  Herdr v0.7.5 kind, plus a future unknown-but-valid kind emitted by discovery.

Run race tests in CI. Aim for meaningful package coverage rather than testing
trivial getters; the aggregate statement coverage target is at least 80%.

## 16. Developer Commands

Create a Makefile with these stable targets:

```text
make help
make fmt
make fmt-check
make tidy
make tidy-check
make vet
make test
make test-race
make coverage
make build
make verify-plugin
make check
make clean
```

`make check` must run every local quality gate that does not require network or
credentials. `make verify-plugin` may download/use an official Herdr v0.7.5
binary when Herdr is absent and must isolate HOME/XDG/Herdr state in a temporary
directory. It must validate offline link/list/action discovery without touching
the user's active Herdr session.

## 17. CI, Security, and Release

### CI workflow

On pushes and pull requests:

- Use least-privilege `contents: read` permissions.
- Cancel superseded runs per ref.
- Test at least Ubuntu and macOS.
- Pin maintained major versions of GitHub Actions.
- Cache Go modules/build data through `actions/setup-go`.
- Enforce `go mod tidy`, `gofmt`, `go vet`, `go test -race`, coverage threshold,
  build, `go test` on all packages, `govulncheck`, shell syntax, and plugin
  manifest verification.
- Upload coverage output as an artifact; no third-party token is required.

### Release workflow

On signed or annotated tags matching `v*`:

- Require `contents: write` only in the release job.
- Verify the tag version exactly matches `herdr-plugin.toml` and the binary
  version source.
- Run the full test/build gates again.
- Use GoReleaser to build static binaries for darwin/linux and amd64/arm64.
- Publish `.tar.gz` archives, `checksums.txt`, and an SBOM when supported.
- Generate release notes and create the GitHub Release.
- Do not auto-bump or push commits from CI.

Configure Dependabot for Go modules and GitHub Actions with a reasonable weekly
schedule. Add a security policy explaining private vulnerability reporting and
the credential model. Add issue forms/templates for bugs and feature requests.

## 18. README and Product Documentation

The README must be production-grade and accurate. Include:

- A concise product statement and feature list.
- Herdr/Shortcut/Go badges and CI/release badges that point to this repository.
- A terminal mockup or checked-in text demo that does not claim to be a real
  screenshot.
- Prerequisites and one-command plugin install.
- Shortcut token creation link and secure macOS Keychain, 1Password CLI, and
  environment-variable examples.
- Exact config directory discovery command and complete config reference.
- Optional Herdr keybinding.
- Keyboard and mouse reference.
- What happens when a Story is launched.
- `doctor` usage.
- Architecture and data-flow overview.
- Development, testing, plugin verification, and release instructions.
- Troubleshooting for missing plugin commands, invalid token, rate limiting,
  unsupported/unavailable selected harnesses, missing cwd, popup problems, and
  failed launch recovery.
- Security model, limitations, contribution link, and MIT license.

Explicitly note that some Homebrew `herdr 0.7.5` bottles have been observed to
report version 0.7.5 while omitting the `plugin` command. Tell affected users to
verify with `herdr plugin` and install the official Herdr release binary if the
command is absent. Do not modify the user's Herdr installation automatically.

Keep `config.example.toml`, CLI help, README, and code defaults synchronized by
tests where practical.

## 19. Open-Source Product Metadata

- MIT license, copyright Matheus Monteiro and contributors.
- Repository description: `Shortcut task picker and coding-agent launcher for
  Herdr`.
- Recommended GitHub topics: `herdr-plugin`, `shortcut`, `coding-agents`,
  `claude-code`, `codex`, `opencode`, `golang`, `tui`, `bubbletea`,
  `developer-tools`.
- Public issues enabled.
- No analytics, telemetry, update pings, or hidden network requests from the
  built-in client. Network access by the configured token command or selected
  external coding harness is outside the plugin client and must be documented.

## 20. Quality Bar and Non-Goals

Quality bar:

- Small, idiomatic Go with explicit dependencies and clear errors.
- No panics from user input, API data, dimensions, missing env, or malformed
  subprocess output.
- No shell invocation for credentials, Herdr commands, browser URLs, or prompts.
  The configured token command is already argv and runs directly.
- No secrets in repository, fixtures, logs, prompts, snapshots, or GitHub Actions.
- No TODOs, FIXME placeholders, disabled tests, fake success paths, or docs for
  behavior that does not exist.
- Public APIs and non-obvious security/concurrency logic have succinct comments.
- `go test -race ./...` and `make check` pass from a clean checkout.

Non-goals for v0.1.0:

- Updating Story status, owners, comments, or fields.
- Creating Shortcut Stories.
- Windows support.
- Background polling, notifications, or webhooks.
- A daemon, database, browser UI, or native Herdr UI extension.
- Automatic repository inference from labels or external links.

## 21. Definition of Done

Implementation is complete only when all of the following are true:

- Every repository deliverable exists and contains real, coherent content.
- The plugin can be built from a clean checkout with Go 1.26 (the single
  supported and tested toolchain, pinned via `mise.toml` and `go.mod`).
- `make check` passes.
- `go test -race ./...` passes.
- `make verify-plugin` validates the manifest against official Herdr v0.7.5 in
  isolated state.
- A local mock end-to-end run demonstrates list -> mouse/keyboard Story select ->
  harness select -> cwd select -> exact `agent start --kind <selected> --pane` ->
  `agent prompt` behavior for all v0.7.5 kinds and a future discovered kind.
- The README install/config/usage commands match the implementation.
- CI and release workflows are valid and least privilege.
- No secret or generated binary is tracked.
- `git status` contains only the intentional implementation changes.
- The implementing agent performs a final self-review against every section of
  this specification and fixes all findings before reporting completion.

When finished, report:

1. The implemented architecture and user flow.
2. Every verification command run and its result.
3. Any deliberate deviation from this specification, with justification.
4. Remaining product limitations, if any, without calling unfinished required
   work a limitation.
