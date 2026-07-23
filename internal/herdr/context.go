package herdr

import (
	"encoding/json"
	"fmt"
	"strings"
)

// InvocationContext is the subset of Herdr's HERDR_PLUGIN_CONTEXT_JSON that this
// plugin uses. All fields are optional and omitted by Herdr when unavailable.
type InvocationContext struct {
	WorkspaceID    string `json:"workspace_id"`
	WorkspaceCwd   string `json:"workspace_cwd"`
	TabID          string `json:"tab_id"`
	FocusedPaneID  string `json:"focused_pane_id"`
	FocusedPaneCwd string `json:"focused_pane_cwd"`
}

// Custom env vars the `open` action forwards to the popup so the launch dialog
// sees the ORIGINAL invocation context. A popup regenerates its own plugin
// context (targeting the popup pane, not the pane the user launched from), so
// these immutable originals must be preferred for cwd/workspace selection.
const (
	EnvActionWorkspaceID    = "HERDR_SHORTCUT_ACTION_WORKSPACE_ID"
	EnvActionWorkspaceCwd   = "HERDR_SHORTCUT_ACTION_WORKSPACE_CWD"
	EnvActionFocusedPaneCwd = "HERDR_SHORTCUT_ACTION_FOCUSED_PANE_CWD"
)

// ActionEnv returns the forwarded originals for a popup as KEY=VALUE pairs, in a
// deterministic order, omitting empty values. It is used by the `open` action to
// build the `plugin pane open --env` arguments.
func (c InvocationContext) ActionEnv() map[string]string {
	env := make(map[string]string, 3)
	if c.WorkspaceID != "" {
		env[EnvActionWorkspaceID] = c.WorkspaceID
	}
	if c.WorkspaceCwd != "" {
		env[EnvActionWorkspaceCwd] = c.WorkspaceCwd
	}
	if c.FocusedPaneCwd != "" {
		env[EnvActionFocusedPaneCwd] = c.FocusedPaneCwd
	}
	return env
}

// ParseContext parses the HERDR_PLUGIN_CONTEXT_JSON value. An empty string
// yields a zero context and no error, since Herdr may omit it.
func ParseContext(raw string) (InvocationContext, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return InvocationContext{}, nil
	}
	var c InvocationContext
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return InvocationContext{}, fmt.Errorf("parse HERDR_PLUGIN_CONTEXT_JSON: %w", err)
	}
	return c, nil
}

// ContextFromEnv resolves the invocation context from environment variables,
// preferring HERDR_PLUGIN_CONTEXT_JSON and falling back to the discrete
// HERDR_WORKSPACE_ID / HERDR_TAB_ID / HERDR_PANE_ID variables for anything the
// JSON did not provide.
func ContextFromEnv(getenv func(string) string) (InvocationContext, error) {
	c, err := ParseContext(getenv("HERDR_PLUGIN_CONTEXT_JSON"))
	if err != nil {
		return InvocationContext{}, err
	}
	if c.WorkspaceID == "" {
		c.WorkspaceID = getenv("HERDR_WORKSPACE_ID")
	}
	if c.TabID == "" {
		c.TabID = getenv("HERDR_TAB_ID")
	}
	if c.FocusedPaneID == "" {
		c.FocusedPaneID = getenv("HERDR_PANE_ID")
	}
	// Prefer the immutable originals forwarded by the `open` action, when present.
	// The popup's own regenerated context points at the popup pane, so these
	// override it for workspace and cwd selection.
	if v := getenv(EnvActionWorkspaceID); v != "" {
		c.WorkspaceID = v
	}
	if v := getenv(EnvActionWorkspaceCwd); v != "" {
		c.WorkspaceCwd = v
	}
	if v := getenv(EnvActionFocusedPaneCwd); v != "" {
		c.FocusedPaneCwd = v
	}
	return c, nil
}
