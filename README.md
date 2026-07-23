# herdr-shortcut

[![CI](https://github.com/matheus3301/herdr-shortcut/actions/workflows/ci.yml/badge.svg)](https://github.com/matheus3301/herdr-shortcut/actions/workflows/ci.yml)
[![Release](https://github.com/matheus3301/herdr-shortcut/actions/workflows/release.yml/badge.svg)](https://github.com/matheus3301/herdr-shortcut/actions/workflows/release.yml)
[![Go 1.26](https://img.shields.io/badge/go-1.26-00ADD8?logo=go)](https://go.dev/)
[![Herdr 0.7.5+](https://img.shields.io/badge/herdr-0.7.5%2B-5B4FE9)](https://herdr.dev)
[![Shortcut API v3](https://img.shields.io/badge/shortcut-API%20v3-7B68EE)](https://developer.shortcut.com/api/rest/v3)
[![License: MIT](https://img.shields.io/badge/license-MIT-green)](LICENSE)

A polished [Herdr](https://herdr.dev) plugin that shows the Stories assigned to
you in [Shortcut](https://shortcut.com) in a fast terminal UI, then launches a
coding-agent harness in a new Herdr tab to work on the one you pick. It supports
**every agent kind the installed Herdr binary reports** — Claude Code, Codex,
Gemini, and the rest — discovered dynamically at runtime.

It is a **read-only** Shortcut client and a Herdr launcher. It never writes to
Shortcut, and it never persists or prints your API token.

## Features

- Fast Bubble Tea task picker for your active, non-completed Stories.
- Local filtering by Story ID, title, state, type, and labels.
- Deterministic ordering: started work first, then by state, deadline, and
  recency.
- Keyboard **and** mouse: navigate, filter, refresh, open in browser, and launch
  by click.
- Per-launch harness selection from the kinds Herdr reports (no hard-coded list),
  with per-kind native arguments.
- One keypress opens a launch dialog to pick the harness and a working directory
  (current Herdr pane, workspace, configured repositories, or a custom path) and
  starts the chosen agent with a rich, current prompt about the Story.
- No telemetry, update pings, or hidden network access. **At runtime the plugin
  itself** contacts only the configured Shortcut API and the explicit browser
  open you request. Two things it invokes make their own network requests, by
  design and outside the plugin's control: a configured `token_command` (e.g. a
  secrets manager) and the launched coding-agent harness. (Plugin
  **installation** and CI may also download the release archive or an official
  Herdr build — each verified against a pinned SHA-256 checksum.)
- macOS and Linux, amd64 and arm64.

## Demo

This is an illustrative text mock-up, not a real screenshot:

```text
 Shortcut / My tasks                          @matheus · 7 results · updated 2m ago
 / filter  (press / to search)
 ID       TYPE   STATE          TITLE                              AGE   DUE
 SC-4821  feat   In Progress    Add retry to the webhook worker      3h   2026-08-01
 SC-4790  bug    In Progress    Fix flaky checkout redirect          1d   ·
 SC-4763  chore  Ready          Bump Go toolchain to 1.26            2d   ·
 SC-4711  feat   Backlog        Team dashboard filters               5d   2026-08-10
 ↑↓ move · Enter launch · / filter · o open · r refresh · ? help · q quit
```

Pressing `Enter` opens the launch dialog:

```text
 Launch agent
 SC-4821  Add retry to the webhook worker
 Tab: SC-4821 claude    Agent: sc-4821-claude

 ▎ Agent harness: (3/21 ↑↓)
 ▸ claude
   codex
   gemini
   Working directory:
 ▸ Focused pane   /Users/me/src/webhooks
   Workspace      /Users/me/src
   Custom path…

 [ Launch agent ]   [ Cancel ]
```

Tab switches between the harness and working-directory regions; the harness list
holds every kind the installed Herdr reports.

## Prerequisites

- [Herdr](https://herdr.dev) **v0.7.5 or newer** with the `plugin` command
  (verify with `herdr plugin`).
- At least one coding-agent harness that your Herdr supports — for example
  [Claude Code](https://claude.com/claude-code), Codex, or Gemini. Run
  `herdr agent` to see the kinds it reports; `agent.default_kind` must be one of
  them.
- A [Shortcut API token](https://app.shortcut.com/settings/account/api-tokens).
- Optional for building from source: Go 1.26 (the single supported and tested
  version; the repo pins it via `mise.toml`).

## Install

```sh
herdr plugin install matheus3301/herdr-shortcut
```

At install time the plugin builds `bin/herdr-shortcut` from source when a
compatible Go toolchain is present, and otherwise downloads and **verifies the
SHA-256 checksum** of the release archive for your OS and architecture.

## Providing your Shortcut token

Create a token at
<https://app.shortcut.com/settings/account/api-tokens>. The token is read from
the `SHORTCUT_API_TOKEN` environment variable or a configured `token_command`.
It is **never** stored in a config file.

**Environment variable** (simplest):

```sh
export SHORTCUT_API_TOKEN="your-token"
```

**macOS Keychain** — store once, then reference it with a `token_command`:

```sh
security add-generic-password -a "$USER" -s shortcut-token -w "your-token"
```

```toml
# config.toml
[shortcut]
token_command = ["security", "find-generic-password", "-s", "shortcut-token", "-w"]
```

**1Password CLI**:

```toml
[shortcut]
token_command = ["op", "read", "op://Private/Shortcut/token"]
```

The `token_command` is an argv array run directly (never through a shell); one
trailing newline is trimmed from its output.

## Configuration

Configuration is optional. With `SHORTCUT_API_TOKEN` set and a working directory
available from Herdr, the defaults work out of the box.

Find the exact config directory Herdr uses for this plugin:

```sh
herdr plugin config-dir matheus3301.shortcut
```

Place `config.toml` there. Outside Herdr, the plugin looks in
`$XDG_CONFIG_HOME/herdr-shortcut/config.toml` and then
`$HOME/.config/herdr-shortcut/config.toml`.

See [`config.example.toml`](config.example.toml) for the complete, commented
reference. Full schema and defaults:

| Key | Default | Notes |
| --- | --- | --- |
| `shortcut.api_base_url` | `https://api.app.shortcut.com/api/v3` | Absolute HTTPS (HTTP only for loopback). |
| `shortcut.query` | `owner:{member} is:story !is:done !is:archived` | `{member}` is your mention name. |
| `shortcut.page_size` | `100` | 1–250. |
| `shortcut.max_stories` | `250` | 1–1000, not below `page_size`. |
| `shortcut.request_timeout` | `15s` | Positive, ≤ 5m. |
| `shortcut.token_command` | `[]` | argv array; no shell. |
| `agent.default_kind` | `claude` | Initially selected harness; must be a kind Herdr reports. |
| `[agent.args_by_kind]` | none | Map of kind → argv, passed only to that harness after `--`. |
| `agent.name_template` | `sc-{id}-{kind}` | `{id}`/`{kind}`; sanitized to `[a-z][a-z0-9_-]{0,31}`. |
| `agent.tab_label_template` | `SC-{id} {kind}` | `{id}`/`{kind}`. |
| `agent.focus` | `true` | Focus the new tab. |
| `agent.prompt_template` | see below | Placeholders listed below. |
| `[[repositories]]` | none | `name` + `path`; `~` and env vars expanded. |

Prompt placeholders: `{id} {name} {url} {description} {story_type} {state}`
`{labels} {estimate} {deadline} {branch_name} {team_id} {epic_id} {kind}`. Use
`{{` and `}}` for literal braces.

**Prompt limits.** Control characters are normalized (single-value fields are
flattened to one line; only the description keeps meaningful newlines). Each
short field is capped at 1024 characters, the description at 8000, and the whole
prompt at 16000, with a `…[truncated]` marker when shortened.

**Focus behavior.** With `agent.focus = true` (the default) the launch creates a
focused tab, so when the popup closes the new agent tab is already in front. Set
it to `false` to create the tab in the background.

### Optional Herdr keybinding

No global keybinding is installed by default, to avoid conflicting with your
configuration. To bind one, add a native `plugin_action` keybinding to your
Herdr config (`~/.config/herdr/config.toml`):

```toml
[[keys.command]]
key = "prefix+alt+s"
type = "plugin_action"
command = "matheus3301.shortcut.open"
```

`command` is the fully-qualified action id (`<plugin-id>.<action-id>`). Apply it
with `herdr server reload-config`. Key syntax follows your Herdr
version; see `herdr --default-config`.

## Usage

Invoke the **Shortcut: My tasks** action from Herdr (or your keybinding) to open
the picker popup.

The plugin binary is installed at `<plugin-root>/bin/herdr-shortcut` and is not
placed on your `PATH`; Herdr runs it for you. When developing, build it locally
(`make build`) and run `./bin/herdr-shortcut <command>`.

### Keyboard

| Key | Action |
| --- | --- |
| `j` / `k`, `↓` / `↑` | Move selection (also scrolls help / result overlays) |
| `g` / `G`, `Home` / `End` | First / last Story |
| `PgDn` / `PgUp` | Page the list (and scroll overlays) |
| `/` | Focus local filter (`Esc` clears) |
| `Enter` | Open the launch dialog |
| `o` | Open the selected Story in your browser |
| `r` | Refresh from Shortcut |
| `?` | Toggle help |
| `q` | Quit (from the list, the launch dialog's non-editing regions, and result screens; dismisses help) |
| `Ctrl+C` | Quit from anywhere |

Launch orchestration is deliberately non-cancellable once it starts: while a
tab or agent may be being created, `Ctrl+C` and other quit keys are ignored until
Herdr returns a result. This prevents orphaned tabs and preserves recovery state.

In the launch dialog, `Tab` switches between the harness and working-directory
regions and `Esc` cancels. On a launch failure, `c` copies the recovery command,
`←`/`→` scroll it horizontally, and `Enter` retries — resuming the existing tab,
and asking for confirmation before resubmitting a prompt that may already have
been delivered.

### Mouse

- Wheel scrolls the list, the help/result overlays, and — in the launch dialog —
  whichever region is focused, so every harness kind and the **Custom path…**
  entry stay reachable even in short terminals.
- Click a Story row to select it and open the launch dialog.
- In the dialog, click a harness row, a working-directory row, **Launch agent**,
  or **Cancel**.

## What happens when you launch a Story

1. The selected kind is validated against the kinds Herdr reports, and the Story
   is re-fetched from Shortcut for current details.
2. A prompt is rendered from your template (control characters normalized,
   lengths capped).
3. A unique agent name is generated from your `{id}`/`{kind}` template.
4. A new focused tab is created in the current workspace with your chosen cwd.
5. The selected harness is started in that tab's root pane
   (`herdr agent start … --kind <selected> --pane … -- <args for that kind>`).
6. The prompt is submitted (`herdr agent prompt …`).
7. The popup closes, leaving the new agent tab focused and working.

If a step after tab creation fails, the tab is **not** closed and the failure is
reported with the tab/pane IDs and an exact, copyable recovery command that
contains no credentials. On the failure screen, press `c` to copy the command to
the clipboard, or `←`/`→` to scroll it horizontally (it is never wrapped or
whitespace-collapsed). `Esc` returns to the picker while preserving the partial
launch, so re-launching the same Story and harness resumes the existing tab
instead of creating a duplicate. If the prompt submission failed after the agent
started, the prompt is **never** resubmitted automatically — `Enter` asks for
explicit confirmation first, because it may already have been delivered.

## Doctor

Diagnose configuration and connectivity without printing any secret. Run it as a
managed Herdr action (output goes to the plugin log):

```sh
herdr plugin action invoke matheus3301.shortcut.doctor
herdr plugin log list --plugin matheus3301.shortcut
```

`herdr plugin log list --plugin matheus3301.shortcut` returns the captured output
as a `plugin_log_list` envelope, for example (empty until the action has run):

```json
{"id":"cli:plugin","result":{"logs":[],"type":"plugin_log_list"}}
```

Or, when developing, run the built binary directly: `./bin/herdr-shortcut doctor`.

Doctor verifies configuration, token resolution, Shortcut authentication, that
Herdr is at least v0.7.5 with the `plugin` command, that an active workspace is
available, which agent kinds Herdr reports (and that `default_kind` is among
them), and each configured repository path. It exits non-zero if any required
capability is unavailable — it never reports success from configuration alone.

## Architecture

```text
cmd/herdr-shortcut ──> internal/app ──> internal/tui        (Bubble Tea UI + hit testing)
                                    ├──> internal/shortcut   (typed REST v3 client)
                                    ├──> internal/prompt     (deterministic prompt rendering)
                                    ├──> internal/herdr       (typed Herdr CLI adapter)
                                    ├──> internal/config      (config + token resolution)
                                    └──> internal/browser     (Story URL opener)
```

Data flow: the `open` action asks Herdr to open the `tasks` popup, which runs
`herdr-shortcut tui`. The TUI discovers the harness kinds (`herdr agent`), loads
the member, workflows, and Stories through the Shortcut client, resolves state
names, sorts, and renders. On launch, the app re-fetches the Story, renders the
prompt, and drives the Herdr adapter to create the tab and start and prompt the
selected harness.

## Development

The repository pins its toolchain with [mise](https://mise.jdx.dev): **Go 1.26**
is the single supported and tested version. Run `mise install` once to match it,
or prefix commands with `mise exec --`. CI and the release workflow use the same
version.

```sh
make help          # list targets
make check         # fmt, tidy, vet, race tests, coverage, build
make test-race     # go test -race ./...
make coverage      # coverage.txt with an enforced 80% threshold
make build         # build ./bin/herdr-shortcut
make verify-plugin # validate the manifest against Herdr in isolated state
make smoke-nogo    # test the no-Go install fallback with local fake assets
```

`make check` runs every gate that needs no network or credentials. Tests are
deterministic and require no real Shortcut token, network, Herdr session, or
coding-agent account.

`make verify-plugin` links the plugin into a throwaway Herdr state directory and
confirms it and its action and pane are discoverable, without touching your
active Herdr session. By default it downloads the **pinned official Herdr
v0.7.5** (verified SHA-256) for reproducibility; set `HERDR_BIN=/path/to/herdr`
to use a specific binary, or `HERDR_USE_PATH=1` to use a `herdr` from your
`PATH`.

`make smoke-nogo` generates fake release assets locally, hides Go from `PATH`,
and runs the install script's download-and-checksum path end to end.

## Releasing

Releases are cut by pushing an **annotated** (or signed) `vX.Y.Z` tag whose
version matches `herdr-plugin.toml` and `internal/buildinfo`, on a commit that is
already on `main`:

```sh
git tag -a v0.1.0 -m "herdr-shortcut v0.1.0"
git push origin v0.1.0
```

The release workflow requires an annotated/signed tag whose commit is on `main`,
verifies the version match, runs the full test, security (`govulncheck`), and
plugin-verification gates, builds the release binaries with Go 1.26 (the single
supported toolchain) and scans them, and uses GoReleaser to publish `.tar.gz`
archives, `checksums.txt`, and an SBOM for darwin/linux on amd64/arm64. CI never
bumps or pushes commits.

## Troubleshooting

- **`herdr plugin` is missing / plugin commands not found.** Some Homebrew
  `herdr` **0.7.5 bottles report the right version but omit the `plugin`
  command.** Verify with `herdr plugin`. If it is absent, install the official
  Herdr release binary. This plugin never modifies your Herdr installation
  automatically.
- **Invalid token.** Run the doctor action
  (`herdr plugin action invoke matheus3301.shortcut.doctor`); it reports
  `Shortcut authentication` failures. Recreate the token and re-export
  `SHORTCUT_API_TOKEN` (or check your `token_command`).
- **Rate limited (HTTP 429).** Shortcut allows 200 requests/minute. The client
  retries with backoff; if you see rate-limit errors, wait and press `r`.
- **Agent harness not available.** Run `herdr agent` to see the kinds Herdr
  reports; set `agent.default_kind` to one of them and ensure that harness is
  installed for Herdr. `herdr-shortcut doctor` lists the discovered kinds.
- **No working directory.** Launch from a Herdr pane that has a cwd, or add
  `[[repositories]]` entries, or use **Custom path…** in the dialog.
- **Popup does not appear.** Confirm the plugin is linked (`herdr plugin list`)
  and that you invoked `matheus3301.shortcut.open`.
- **Launch failed after the tab was created.** The tab is kept. Use the recovery
  command shown, or type your request directly in the new agent tab.

## Security

- The token is held only in memory and sent only via the `Shortcut-Token` header
  over HTTPS. It is never written to config, state, logs, errors, prompts,
  snapshots, or process arguments, and it is scrubbed from the environment of the
  new tab (`tab create --env SHORTCUT_API_TOKEN=`) so neither the root shell nor
  the launched harness inherits it.
- No shell is used for credentials, Herdr commands, browser URLs, or prompts.
- A configured `token_command` and the coding harness launched by Herdr are
  independent programs and may perform their own network access. The new agent
  pane explicitly receives an empty `SHORTCUT_API_TOKEN`.
- Only `https://` Shortcut Story URLs are opened in the browser.
- The plugin makes no network requests of its own beyond the Shortcut API and the
  browser open. A configured `token_command` and the launched agent harness run
  independently and may reach the network on their own.

See [SECURITY.md](SECURITY.md) to report vulnerabilities.

## Limitations (v0.1.0)

No Shortcut writes, Story creation, Windows support, background polling, or
notifications. See the specification's non-goals.

## Contributing

Contributions are welcome — see [CONTRIBUTING.md](CONTRIBUTING.md) and the
[Code of Conduct](CODE_OF_CONDUCT.md).

## License

[MIT](LICENSE) © Matheus Monteiro and contributors.
