package herdr

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// MaxAgentNameLen is the maximum length of a Herdr agent name.
const MaxAgentNameLen = 32

// TabResult is the parsed result of `herdr tab create`.
type TabResult struct {
	TabID  string
	PaneID string
}

// AgentInfo is the subset of a Herdr agent used for unique-name generation and
// for reconciling an ambiguous `agent start` against the live agent list.
type AgentInfo struct {
	Name   string `json:"name"`
	Kind   string `json:"agent"`
	PaneID string `json:"pane_id"`
	// InteractiveReady and LaunchPending report the agent's startup lifecycle so
	// a start that failed to return (a CLI wait timeout or a lost response) can be
	// reconciled: an agent that is interactive-ready, or still launch-pending on
	// the target pane, means the start actually took effect and must not be redone.
	InteractiveReady bool `json:"interactive_ready"`
	LaunchPending    bool `json:"launch_pending"`
}

// OpenPaneParams configures `herdr plugin pane open`.
type OpenPaneParams struct {
	PluginID     string
	Entrypoint   string
	Placement    string
	WorkspaceID  string
	TargetPaneID string
	Cwd          string
	Focus        *bool
	// Env are custom KEY=VALUE variables Herdr sets on the spawned pane process.
	// Used to forward the immutable original invocation context to a popup.
	Env map[string]string
}

// OpenPane opens (or focuses) a manifest pane. Popup and overlay placements
// target the active pane, so Herdr rejects workspace_id/target_pane_id for them;
// this method drops those fields for those placements regardless of the params.
// A popup returns a bare "ok" result with no pane id; an overlay (and every
// other placement) returns "plugin_pane_opened".
func (h *Herdr) OpenPane(ctx context.Context, p OpenPaneParams) error {
	args := []string{"plugin", "pane", "open", "--plugin", p.PluginID, "--entrypoint", p.Entrypoint}
	targetsActivePane := p.Placement == "popup" || p.Placement == "overlay"
	if p.Placement != "" {
		args = append(args, "--placement", p.Placement)
	}
	if p.WorkspaceID != "" && !targetsActivePane {
		args = append(args, "--workspace", p.WorkspaceID)
	}
	if p.TargetPaneID != "" && !targetsActivePane {
		args = append(args, "--target-pane", p.TargetPaneID)
	}
	if p.Cwd != "" {
		args = append(args, "--cwd", p.Cwd)
	}
	if p.Focus != nil {
		if *p.Focus {
			args = append(args, "--focus")
		} else {
			args = append(args, "--no-focus")
		}
	}
	// Custom env forwarding, in a deterministic (sorted) order for stable argv.
	if len(p.Env) > 0 {
		keys := make([]string, 0, len(p.Env))
		for k := range p.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			args = append(args, "--env", k+"="+p.Env[k])
		}
	}
	// Only a popup returns a bare "ok" (no pane handle). Every other placement,
	// including an overlay, creates a pane and returns "plugin_pane_opened".
	wantType := "plugin_pane_opened"
	if p.Placement == "popup" {
		wantType = "ok"
	}
	return h.runTyped(ctx, args, wantType, nil)
}

// WorkspaceInfo identifies a Herdr workspace.
type WorkspaceInfo struct {
	WorkspaceID string `json:"workspace_id"`
	Focused     bool   `json:"focused"`
}

// ActiveWorkspaceID returns the focused workspace's id via `workspace list`,
// used to freeze the target workspace when the invocation context lacks one.
func (h *Herdr) ActiveWorkspaceID(ctx context.Context) (string, error) {
	var res struct {
		Workspaces []WorkspaceInfo `json:"workspaces"`
	}
	if err := h.runTyped(ctx, []string{"workspace", "list"}, "workspace_list", &res); err != nil {
		return "", err
	}
	for _, w := range res.Workspaces {
		if w.Focused && w.WorkspaceID != "" {
			return w.WorkspaceID, nil
		}
	}
	return "", &HerdrError{Command: "workspace list", Message: "no active workspace found"}
}

// TokenScrubEnv is the --env argument passed to `tab create` so the
// server-created root shell — and the harness later started in it — cannot
// inherit the Shortcut token from Herdr's own environment. The empty value
// clears the variable in the spawned pane.
const TokenScrubEnv = "SHORTCUT_API_TOKEN="

// CreateTab creates a new tab and returns its tab and root pane IDs. It always
// scrubs the Shortcut token from the new tab's environment. An incomplete or
// otherwise ambiguous response (timeout, malformed/wrong body) is returned with
// any partial ids and an error the caller can classify with IsAmbiguousTab.
func (h *Herdr) CreateTab(ctx context.Context, workspaceID, cwd, label string, focus bool) (TabResult, error) {
	args := []string{"tab", "create"}
	if workspaceID != "" {
		args = append(args, "--workspace", workspaceID)
	}
	if cwd != "" {
		args = append(args, "--cwd", cwd)
	}
	if label != "" {
		args = append(args, "--label", label)
	}
	if focus {
		args = append(args, "--focus")
	} else {
		args = append(args, "--no-focus")
	}
	// Never let the root shell or harness inherit the token.
	args = append(args, "--env", TokenScrubEnv)
	var res struct {
		Tab struct {
			TabID string `json:"tab_id"`
		} `json:"tab"`
		RootPane struct {
			PaneID string `json:"pane_id"`
		} `json:"root_pane"`
	}
	if err := h.runTyped(ctx, args, "tab_created", &res); err != nil {
		// A timeout/cancellation or a successful-but-unparseable body may still
		// have created a tab server-side. Return any partial ids so the caller can
		// resume rather than leak a second tab; classification is via IsAmbiguousTab.
		return TabResult{TabID: res.Tab.TabID, PaneID: res.RootPane.PaneID}, err
	}
	partial := TabResult{TabID: res.Tab.TabID, PaneID: res.RootPane.PaneID}
	if partial.TabID == "" || partial.PaneID == "" {
		// tab_created but missing an id: a tab may well have been created, so treat
		// it as potentially-created and never create a second tab.
		return partial, &HerdrError{Command: "tab create", Code: incompleteTabCode, Message: "incomplete response: missing tab or root pane id"}
	}
	return partial, nil
}

// Error codes distinguishing ambiguous, potentially-resource-creating responses
// from clean failures.
const (
	incompleteTabCode        = "incomplete_tab_response"
	malformedResponseCode    = "malformed_response"
	unexpectedResultTypeCode = "unexpected_result_type"
)

// IsAmbiguousTab reports whether a CreateTab error means a tab MAY have been
// created despite the failure: an incomplete/malformed/wrong-typed successful
// response, or a context timeout/cancellation. The caller must resume (never
// re-create) on these.
func IsAmbiguousTab(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var he *HerdrError
	if errors.As(err, &he) {
		// Herdr reserves exit 1 for server/socket failures. A structured error code
		// proves the server rejected the request, but plain exit-1 output can mean
		// the request took effect and the response was lost.
		if he.ExitCode == 1 && he.Code == "" {
			return true
		}
		switch he.Code {
		case incompleteTabCode, malformedResponseCode, unexpectedResultTypeCode:
			return true
		}
	}
	return false
}

// ListAgents returns the live agents.
func (h *Herdr) ListAgents(ctx context.Context) ([]AgentInfo, error) {
	var res struct {
		Agents []AgentInfo `json:"agents"`
	}
	if err := h.runTyped(ctx, []string{"agent", "list"}, "agent_list", &res); err != nil {
		return nil, err
	}
	return res.Agents, nil
}

// LiveAgentNames returns the names of live, named agents.
func (h *Herdr) LiveAgentNames(ctx context.Context) ([]string, error) {
	agents, err := h.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(agents))
	for _, a := range agents {
		if a.Name != "" {
			names = append(names, a.Name)
		}
	}
	return names, nil
}

// StartAgent starts an agent of the given kind in an existing pane. Extra args
// are passed to the underlying agent executable after the "--" separator. The
// returned agent identity is validated against what was requested.
func (h *Herdr) StartAgent(ctx context.Context, name, kind, paneID string, extraArgs []string) error {
	args := []string{"agent", "start", name, "--kind", kind, "--pane", paneID}
	if len(extraArgs) > 0 {
		args = append(args, "--")
		args = append(args, extraArgs...)
	}
	var res struct {
		Agent AgentInfo `json:"agent"`
	}
	if err := h.runTyped(ctx, args, "agent_started", &res); err != nil {
		return err
	}
	return checkIdentity("agent start", res.Agent.Name, res.Agent.Kind, res.Agent.PaneID, name, kind, paneID)
}

// SubmitPrompt atomically submits prompt text to a live agent. It does not wait
// for the agent to settle. The returned agent identity is validated.
func (h *Herdr) SubmitPrompt(ctx context.Context, name, prompt string) error {
	var res struct {
		Agent AgentInfo `json:"agent"`
	}
	if err := h.runTyped(ctx, []string{"agent", "prompt", name, prompt}, "agent_prompted", &res); err != nil {
		return err
	}
	return checkIdentity("agent prompt", res.Agent.Name, res.Agent.Kind, res.Agent.PaneID, name, "", "")
}

// checkIdentity confirms the agent Herdr acted on is the one we asked for. Each
// field is checked only when the response includes it (Herdr may omit some), so
// a lean response is accepted but a mismatched one is rejected.
func checkIdentity(cmd, gotName, gotKind, gotPane, wantName, wantKind, wantPane string) error {
	mismatch := func(field, got, want string) error {
		return &HerdrError{Command: cmd, Message: fmt.Sprintf("%s identity mismatch: %s %q, expected %q", cmd, field, got, want)}
	}
	if wantName != "" && gotName != wantName {
		return mismatch("name", gotName, wantName)
	}
	if wantKind != "" && gotKind != wantKind {
		return mismatch("kind", gotKind, wantKind)
	}
	if wantPane != "" && gotPane != wantPane {
		return mismatch("pane", gotPane, wantPane)
	}
	return nil
}

// AgentKinds discovers the agent-harness kinds supported by the installed Herdr
// binary by running bare `herdr agent` and parsing its machine-readable
// `kinds:` line. Herdr's reported order is preserved, invalid/empty identifiers
// are rejected, and duplicates are removed. A compiled-in list is never
// substituted when discovery fails.
func (h *Herdr) AgentKinds(ctx context.Context) ([]string, error) {
	res, err := h.runner(ctx, h.bin, []string{"agent"})
	if err != nil {
		return nil, fmt.Errorf("herdr agent: %w", err)
	}
	// Bare `herdr agent` prints a usage listing whose `kinds:` line is the
	// discovery surface. Herdr emits it with a non-zero (usage) exit code and on
	// stderr, so parse both streams and do not treat a non-zero exit as failure.
	line, ok := findKindsLine(string(res.Stdout))
	if !ok {
		line, ok = findKindsLine(string(res.Stderr))
	}
	if !ok {
		return nil, &HerdrError{Command: "agent", Message: "could not discover agent kinds: no 'kinds:' line in output", ExitCode: res.ExitCode}
	}
	kinds, err := parseKinds(line)
	if err != nil {
		return nil, &HerdrError{Command: "agent", Message: "could not discover agent kinds: " + err.Error(), ExitCode: res.ExitCode}
	}
	return kinds, nil
}

// findKindsLine returns the text after "kinds:" of the first line whose trimmed
// text begins with "kinds:", tolerating unrelated help lines.
func findKindsLine(out string) (string, bool) {
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "kinds:"); ok {
			return strings.TrimSpace(rest), true
		}
	}
	return "", false
}

// parseKinds parses the pipe-separated identifiers of the authoritative kinds
// line. It strictly rejects any empty or invalid component — a malformed
// discovery surface must fail rather than be silently narrowed to a subset —
// while deduplicating valid repeated identifiers and preserving Herdr's order.
func parseKinds(line string) ([]string, error) {
	if strings.TrimSpace(line) == "" {
		return nil, fmt.Errorf("empty kinds list")
	}
	seen := make(map[string]struct{})
	var kinds []string
	for _, raw := range strings.Split(line, "|") {
		k := strings.TrimSpace(raw)
		if k == "" {
			return nil, fmt.Errorf("empty kind identifier in kinds list")
		}
		if !IsValidKind(k) {
			return nil, fmt.Errorf("invalid kind identifier %q in kinds list", k)
		}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		kinds = append(kinds, k)
	}
	return kinds, nil
}

// IsValidKind reports whether k is a valid Herdr agent-kind identifier
// (lowercase, matching [a-z][a-z0-9_-]*).
func IsValidKind(k string) bool {
	if k == "" {
		return false
	}
	for i, r := range k {
		switch {
		case r >= 'a' && r <= 'z':
		case i > 0 && (r >= '0' && r <= '9' || r == '-' || r == '_'):
		default:
			return false
		}
	}
	return true
}

// Version returns the Herdr CLI version (e.g. "0.7.5") by running
// `herdr --version` non-destructively.
func (h *Herdr) Version(ctx context.Context) (string, error) {
	res, err := h.runner(ctx, h.bin, []string{"--version"})
	if err != nil {
		return "", fmt.Errorf("herdr --version: %w", err)
	}
	if res.ExitCode != 0 {
		return "", parseError("--version", res.ExitCode, res.Stderr)
	}
	v := parseVersion(string(res.Stdout))
	if v == "" {
		return "", &HerdrError{Command: "--version", Message: "could not parse herdr version"}
	}
	return v, nil
}

// PluginCommandAvailable reports whether this Herdr build has the `plugin`
// subcommand (some Homebrew bottles omit it). It is non-destructive.
func (h *Herdr) PluginCommandAvailable(ctx context.Context) bool {
	res, err := h.runner(ctx, h.bin, []string{"plugin", "--help"})
	return err == nil && res.ExitCode == 0
}

// parseVersion extracts the first X.Y.Z token from s.
func parseVersion(s string) string {
	for _, f := range strings.Fields(s) {
		f = strings.TrimPrefix(f, "v")
		parts := strings.Split(f, ".")
		if len(parts) < 3 {
			continue
		}
		ok := true
		for _, p := range parts[:3] {
			if p == "" || strings.TrimFunc(p, func(r rune) bool { return r >= '0' && r <= '9' }) != "" {
				ok = false
				break
			}
		}
		if ok {
			return strings.Join(parts[:3], ".")
		}
	}
	return ""
}

// SanitizeAgentName coerces raw into a valid Herdr agent name matching
// [a-z][a-z0-9_-]{0,31}.
func SanitizeAgentName(raw string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(raw) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	s := b.String()
	if s == "" {
		s = "sc"
	}
	if c := s[0]; c < 'a' || c > 'z' {
		s = "s" + s
	}
	if len(s) > MaxAgentNameLen {
		s = s[:MaxAgentNameLen]
	}
	return s
}

// UniqueAgentName sanitizes base and, if it collides with a taken name, appends
// -2, -3, and so on while keeping the result within MaxAgentNameLen.
func UniqueAgentName(base string, taken []string) string {
	base = SanitizeAgentName(base)
	set := make(map[string]struct{}, len(taken))
	for _, t := range taken {
		set[t] = struct{}{}
	}
	if _, used := set[base]; !used {
		return base
	}
	for n := 2; n < 1_000_000; n++ {
		suffix := "-" + strconv.Itoa(n)
		trimmed := base
		if len(trimmed)+len(suffix) > MaxAgentNameLen {
			trimmed = trimmed[:MaxAgentNameLen-len(suffix)]
		}
		candidate := trimmed + suffix
		if _, used := set[candidate]; !used {
			return candidate
		}
	}
	return base
}
