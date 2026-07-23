package app

import (
	"context"
	"sync"

	"github.com/matheus3301/herdr-shortcut/internal/shortcut"
	"github.com/matheus3301/herdr-shortcut/internal/tui"
)

// load resolves the token, fetches the member and workflows concurrently, then
// the member's Stories, and returns them resolved and sorted. The token is
// re-resolved on every call and never persisted.
func (a *App) load(ctx context.Context) tui.LoadResult {
	token, _, err := a.tokenResolver().ResolveToken(ctx, a.cfg.Shortcut.TokenCommand)
	if err != nil {
		return tui.LoadResult{Err: tui.CredentialsError{Cause: err}}
	}
	client, err := a.buildClient(token)
	if err != nil {
		return tui.LoadResult{Err: err}
	}

	member, workflows, err := fetchMemberAndWorkflows(ctx, client)
	if err != nil {
		return tui.LoadResult{Err: err}
	}

	stories, err := client.MyStories(ctx, member.MentionName, a.cfg.Shortcut.Query, a.cfg.Shortcut.PageSize, a.cfg.Shortcut.MaxStories)
	if err != nil {
		return tui.LoadResult{Err: err}
	}

	index := shortcut.BuildStateIndex(workflows)
	resolved := shortcut.Resolve(stories, index)
	shortcut.Sort(resolved)
	return tui.LoadResult{Member: member.MentionName, Stories: resolved}
}

// fetchMemberAndWorkflows retrieves the current member and workflows
// concurrently. The member error is preferred because authentication failures
// surface there first, keeping the reported error understandable.
func fetchMemberAndWorkflows(ctx context.Context, client *shortcut.Client) (shortcut.Member, []shortcut.Workflow, error) {
	var (
		member    shortcut.Member
		workflows []shortcut.Workflow
		memberErr error
		wfErr     error
		wg        sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		member, memberErr = client.CurrentMember(ctx)
	}()
	go func() {
		defer wg.Done()
		workflows, wfErr = client.Workflows(ctx)
	}()
	wg.Wait()
	if memberErr != nil {
		return shortcut.Member{}, nil, memberErr
	}
	if wfErr != nil {
		return shortcut.Member{}, nil, wfErr
	}
	return member, workflows, nil
}
