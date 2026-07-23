package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode"

	"github.com/matheus3301/herdr-shortcut/internal/herdr"
	"github.com/matheus3301/herdr-shortcut/internal/prompt"
	"github.com/matheus3301/herdr-shortcut/internal/shortcut"
	"github.com/matheus3301/herdr-shortcut/internal/tui"
)

// validateLaunchInputs rejects an empty rendered prompt and any per-kind native
// argument that carries a NUL or other control character, before any Herdr
// resource (tab/agent) is created.
func (a *App) validateLaunchInputs(promptText, kind string) error {
	if strings.TrimSpace(promptText) == "" {
		return errors.New("refusing to launch: the rendered prompt is empty")
	}
	for i, arg := range a.cfg.Agent.ArgsByKind[kind] {
		for _, r := range arg {
			if unicode.IsControl(r) { // covers NUL and all C0/C1 controls
				return fmt.Errorf("refusing to launch: agent.args_by_kind[%q][%d] contains a control character", kind, i)
			}
		}
	}
	return nil
}

// launchPrep is the per-launch state fetched fresh from Shortcut.
type launchPrep struct {
	story      shortcut.Story
	promptText string
}

// launch performs the full launch algorithm for a Story in cwd using the
// selected harness kind:
//
//  1. Validate the kind against the discovered kinds, re-fetch the Story and
//     workflows, and render the prompt.
//  2. Resolve/freeze the workspace.
//  3. Generate a unique agent name from the {id}/{kind} template.
//  4. Create a focused tab, then start the selected harness in its root pane.
//  5. Submit the rendered prompt.
//
// A failure before tab creation leaves no Herdr resources (StageFailedEarly). A
// failure after tab creation reports the tab and pane IDs, does not close the
// tab, and records the stage so a retry resumes from the failed step.
func (a *App) launch(ctx context.Context, story shortcut.ResolvedStory, cwd, kind string) tui.LaunchResult {
	if err := a.validateKind(kind); err != nil {
		return earlyFailure(err)
	}
	prep, err := a.prepareLaunch(ctx, story, kind)
	if err != nil {
		return earlyFailure(err)
	}
	if err := a.validateLaunchInputs(prep.promptText, kind); err != nil {
		return earlyFailure(err)
	}

	// Resolve and freeze the target workspace before creating anything.
	workspace, err := a.resolveWorkspace(ctx)
	if err != nil {
		return earlyFailure(err)
	}

	// Revalidate and canonicalize the selected cwd immediately before tab
	// creation: it may have vanished or changed since the dialog validated it,
	// and the launch use case must never hand Herdr an invalid or symlinked path.
	resolvedCwd, err := a.validateDir(cwd)
	if err != nil {
		return earlyFailure(fmt.Errorf("working directory: %w", err))
	}

	names, err := a.herdr.LiveAgentNames(ctx)
	if err != nil {
		return earlyFailure(fmt.Errorf("list Herdr agents: %w", err))
	}
	baseName := a.agentBaseName(prep.story.ID, kind)
	name := herdr.UniqueAgentName(baseName, names)

	// Create the tab. Nothing is created before this point.
	label := a.tabLabel(prep.story.ID, kind)
	tab, err := a.herdr.CreateTab(ctx, workspace, resolvedCwd, label, a.cfg.Agent.Focus)
	if err != nil {
		if herdr.IsAmbiguousTab(err) {
			// Ambiguous: a tab may have been created despite the failure (timeout,
			// cancellation, or a successful-but-unparseable response). Preserve any
			// partial ids, report it as potentially resource-created
			// (StageTabCreated), and never auto-create a second tab.
			res := tui.LaunchResult{
				TabID:     tab.TabID,
				PaneID:    tab.PaneID,
				AgentName: name,
				Err:       fmt.Errorf("create tab: %w", err),
				Stage:     tui.StageTabCreated,
			}
			if tab.PaneID != "" {
				res.Recovery = a.startRecoveryCommand(name, kind, tab.PaneID)
			}
			return res
		}
		return earlyFailure(fmt.Errorf("create tab: %w", err))
	}

	base := tui.LaunchResult{TabID: tab.TabID, PaneID: tab.PaneID, AgentName: name}
	return a.finishLaunch(ctx, base, prep.promptText, kind, baseName, false)
}

// validateKind confirms kind is a valid identifier and is among the kinds
// discovered from the installed Herdr binary.
func (a *App) validateKind(kind string) error {
	if !herdr.IsValidKind(kind) {
		return fmt.Errorf("invalid agent kind %q", kind)
	}
	for _, k := range a.kinds {
		if k == kind {
			return nil
		}
	}
	return fmt.Errorf("agent kind %q is not supported by the installed Herdr; available: %s", kind, strings.Join(a.kinds, ", "))
}

// resolveWorkspace returns the workspace to launch into, preferring the plugin
// invocation context and otherwise freezing the currently active workspace.
func (a *App) resolveWorkspace(ctx context.Context) (string, error) {
	if a.ctxInfo.WorkspaceID != "" {
		return a.ctxInfo.WorkspaceID, nil
	}
	id, err := a.herdr.ActiveWorkspaceID(ctx)
	if err != nil {
		return "", fmt.Errorf("no workspace in the plugin context and could not resolve an active workspace: %w", err)
	}
	return id, nil
}

// resume re-attempts a launch that already created a tab, reusing the existing
// tab, pane, and agent name (and the same kind) so nothing is duplicated. The
// prompt is re-rendered from fresh Story data.
func (a *App) resume(ctx context.Context, prev tui.LaunchResult, story shortcut.ResolvedStory, kind string) tui.LaunchResult {
	if err := a.validateKind(kind); err != nil {
		prev.Err = err
		return prev
	}
	prep, err := a.prepareLaunch(ctx, story, kind)
	if err != nil {
		// Keep the existing tab context and stage; just report the new error.
		prev.Err = err
		return prev
	}
	if err := a.validateLaunchInputs(prep.promptText, kind); err != nil {
		prev.Err = err
		return prev
	}
	// On resume the unique name is already fixed; use it as the collision-retry
	// base as well (the tab and any partial agent state are being reused).
	return a.finishLaunch(ctx, prev, prep.promptText, kind, prev.AgentName, prev.Stage == tui.StageAgentStarted)
}

// finishLaunch runs the agent-start (unless already started) and prompt-submit
// steps against an existing tab, setting the resulting stage. It reconciles
// ambiguous start failures against the live agent list and never auto-resubmits
// a prompt. baseName is the unsuffixed name used to regenerate a clean unique
// name on a collision retry.
func (a *App) finishLaunch(ctx context.Context, base tui.LaunchResult, promptText, kind, baseName string, agentAlreadyStarted bool) tui.LaunchResult {
	if !agentAlreadyStarted {
		name, err := a.ensureAgentStarted(ctx, base.AgentName, baseName, kind, base.PaneID)
		base.AgentName = name
		if err != nil {
			base.Err = err
			base.Stage = tui.StageTabCreated
			base.Recovery = a.startRecoveryCommand(name, kind, base.PaneID)
			return base
		}
	}
	if err := a.herdr.SubmitPrompt(ctx, base.AgentName, promptText); err != nil {
		base.Err = fmt.Errorf("submit prompt: %w", err)
		base.Stage = tui.StageAgentStarted
		base.Recovery = a.promptRecoveryCommand(base.AgentName, promptText)
		return base
	}
	base.Err = nil
	base.Stage = tui.StageComplete
	return base
}

// ensureAgentStarted starts the harness, reconciling ambiguous failures. If the
// start command errors, it checks whether the agent actually came up on the
// target pane (a timed-out or lost start), and on a name collision it
// regenerates a unique name from the unsuffixed base and retries once. It never
// blindly restarts an agent that may already be running. On a failed retry it
// returns the second (retry) error, which describes the start that actually ran.
func (a *App) ensureAgentStarted(ctx context.Context, name, baseName, kind, paneID string) (string, error) {
	args := a.cfg.Agent.ArgsByKind[kind]
	startErr := a.herdr.StartAgent(ctx, name, kind, paneID, args)
	if startErr == nil {
		return name, nil
	}
	// The start command may have failed after the agent actually came up
	// (e.g. a CLI wait timeout or a lost response). Reconcile before retrying.
	if a.agentLiveOnPane(ctx, name, kind, paneID) {
		return name, nil
	}
	// A name collision means another agent took the name after our list; pick a
	// fresh unique name — regenerated from the unsuffixed base so retries stay
	// clean rather than doubly-suffixed — and retry the start exactly once.
	var he *herdr.HerdrError
	if errors.As(startErr, &he) && he.Code == "agent_name_taken" {
		if names, err := a.herdr.LiveAgentNames(ctx); err == nil {
			// The colliding name is off-limits even if the fresh list omits it.
			taken := append(append([]string{}, names...), name)
			if fresh := herdr.UniqueAgentName(baseName, taken); fresh != name {
				e2 := a.herdr.StartAgent(ctx, fresh, kind, paneID, args)
				if e2 == nil {
					return fresh, nil
				}
				if a.agentLiveOnPane(ctx, fresh, kind, paneID) {
					return fresh, nil
				}
				return fresh, fmt.Errorf("start agent %q: %w", kind, e2)
			}
		}
	}
	return name, fmt.Errorf("start agent %q: %w", kind, startErr)
}

// agentLiveOnPane reports whether the named agent of the given kind is live on
// the target pane. It matches name, pane, and kind, and requires a positive
// lifecycle signal (interactive-ready or launch-pending) so a stale or crashed
// entry is not mistaken for a successful start.
func (a *App) agentLiveOnPane(ctx context.Context, name, kind, paneID string) bool {
	agents, err := a.herdr.ListAgents(ctx)
	if err != nil {
		return false
	}
	for _, ag := range agents {
		if ag.Name == name && ag.PaneID == paneID && ag.Kind == kind && (ag.InteractiveReady || ag.LaunchPending) {
			return true
		}
	}
	return false
}

func earlyFailure(err error) tui.LaunchResult {
	return tui.LaunchResult{Err: err, Stage: tui.StageFailedEarly}
}

// prepareLaunch resolves the token, re-fetches the Story and workflows, and
// renders the prompt for the given kind. The token is re-resolved per call and
// never persisted.
func (a *App) prepareLaunch(ctx context.Context, story shortcut.ResolvedStory, kind string) (launchPrep, error) {
	token, _, err := a.tokenResolver().ResolveToken(ctx, a.cfg.Shortcut.TokenCommand)
	if err != nil {
		return launchPrep{}, tui.CredentialsError{Cause: err}
	}
	client, err := a.buildClient(token)
	if err != nil {
		return launchPrep{}, err
	}
	fresh, state, err := a.fetchStoryForLaunch(ctx, client, story)
	if err != nil {
		return launchPrep{}, err
	}
	// Defense in depth: redact the resolved token from the final rendered prompt
	// (which also feeds the copyable recovery command) so it can never leak.
	promptText := prompt.Redact(a.prompt.Render(promptData(fresh, state, kind)), token)
	return launchPrep{story: fresh, promptText: promptText}, nil
}

// fetchStoryForLaunch re-fetches the Story and workflows concurrently and
// resolves the fresh Story's workflow state.
func (a *App) fetchStoryForLaunch(ctx context.Context, client *shortcut.Client, listStory shortcut.ResolvedStory) (shortcut.Story, shortcut.StoryState, error) {
	var (
		fresh     shortcut.Story
		workflows []shortcut.Workflow
		storyErr  error
		wfErr     error
		wg        sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		fresh, storyErr = client.Story(ctx, listStory.ID)
	}()
	go func() {
		defer wg.Done()
		workflows, wfErr = client.Workflows(ctx)
	}()
	wg.Wait()
	if storyErr != nil {
		return shortcut.Story{}, shortcut.StoryState{}, fmt.Errorf("fetch Story SC-%d: %w", listStory.ID, storyErr)
	}
	// Resolve the FRESH Story's state — an unknown or changed state must render as
	// "Unknown state", never the stale list state. If the workflow refresh itself
	// failed, still refuse the stale list state: report the fresh Story with an
	// Unknown state (its id remains visible) rather than a state that may no longer
	// be correct.
	if wfErr != nil {
		return fresh, shortcut.StoryState{ID: fresh.WorkflowStateID, Name: shortcut.UnknownStateName}, nil
	}
	state := shortcut.Resolve([]shortcut.Story{fresh}, shortcut.BuildStateIndex(workflows))[0].State
	return fresh, state, nil
}

// promptRecoveryCommand renders a copyable, credential-free command to submit
// the prompt manually. It uses the injected Herdr binary and POSIX
// single-quoting so a multiline prompt is reproduced faithfully. The prompt
// itself contains no secrets.
func (a *App) promptRecoveryCommand(name, promptText string) string {
	return shellQuoteArg(a.herdr.Bin()) + " agent prompt " + shellSingleQuote(name) + " " + shellSingleQuote(promptText)
}

// startRecoveryCommand renders a command to start the harness in the existing
// pane after a failed start, including the kind's configured arguments.
func (a *App) startRecoveryCommand(name, kind, paneID string) string {
	cmd := shellQuoteArg(a.herdr.Bin()) + " agent start " + shellSingleQuote(name) +
		" --kind " + shellSingleQuote(kind) + " --pane " + shellSingleQuote(paneID)
	args := a.cfg.Agent.ArgsByKind[kind]
	if len(args) > 0 {
		cmd += " --"
		for _, arg := range args {
			cmd += " " + shellSingleQuote(arg)
		}
	}
	return cmd
}

// shellQuoteArg single-quotes s only when it contains characters that a shell
// would interpret, keeping simple paths readable.
func shellQuoteArg(s string) string {
	simple := s != ""
	for _, r := range s {
		if !(r == '/' || r == '.' || r == '-' || r == '_' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			simple = false
			break
		}
	}
	if simple {
		return s
	}
	return shellSingleQuote(s)
}

// shellSingleQuote wraps s in single quotes for POSIX shells, which preserve
// every character (including newlines) literally except the single quote.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
