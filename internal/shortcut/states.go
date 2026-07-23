package shortcut

import (
	"sort"
	"time"
)

// UnknownStateName is shown when a Story references a workflow state that is not
// present in the fetched workflows.
const UnknownStateName = "Unknown state"

// StoryState is a Story's resolved workflow state.
type StoryState struct {
	ID       int64
	Name     string
	Type     string // backlog | unstarted | started | done | "" (unknown)
	Position int64
	Known    bool
}

// ResolvedStory pairs a Story with its resolved workflow state.
type ResolvedStory struct {
	Story
	State StoryState
}

// BuildStateIndex maps workflow-state IDs to their state metadata across all
// workflows. Later workflows do not clobber earlier IDs because state IDs are
// globally unique in Shortcut.
func BuildStateIndex(workflows []Workflow) map[int64]StoryState {
	index := make(map[int64]StoryState)
	for _, w := range workflows {
		for _, s := range w.States {
			index[s.ID] = StoryState{
				ID:       s.ID,
				Name:     s.Name,
				Type:     s.Type,
				Position: s.Position,
				Known:    true,
			}
		}
	}
	return index
}

// Resolve attaches state metadata to each Story. Unknown state IDs remain
// displayable as UnknownStateName rather than failing the whole list.
func Resolve(stories []Story, index map[int64]StoryState) []ResolvedStory {
	out := make([]ResolvedStory, len(stories))
	for i, s := range stories {
		state, ok := index[s.WorkflowStateID]
		if !ok {
			state = StoryState{ID: s.WorkflowStateID, Name: UnknownStateName, Known: false}
		}
		out[i] = ResolvedStory{Story: s, State: state}
	}
	return out
}

// stateTier ranks state types for sorting: started first, then unstarted and
// backlog, then everything else (done/unknown).
func stateTier(t string) int {
	switch t {
	case "started":
		return 0
	case "unstarted", "backlog":
		return 1
	default:
		return 2
	}
}

// Sort orders resolved Stories deterministically per the product spec:
//  1. state type tier (started, then unstarted/backlog, then unknown)
//  2. state position ascending
//  3. deadline ascending, with no deadline last
//  4. updated time descending, with no timestamp last
//  5. Story ID ascending
func Sort(list []ResolvedStory) {
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if ta, tb := stateTier(a.State.Type), stateTier(b.State.Type); ta != tb {
			return ta < tb
		}
		if a.State.Position != b.State.Position {
			return a.State.Position < b.State.Position
		}
		if c := compareDeadline(a.Deadline, b.Deadline); c != 0 {
			return c < 0
		}
		if c := compareUpdatedDesc(a.UpdatedAt, b.UpdatedAt); c != 0 {
			return c < 0
		}
		return a.ID < b.ID
	})
}

// compareDeadline orders earlier deadlines first and nil (no deadline) last.
func compareDeadline(a, b *time.Time) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	case a.Before(*b):
		return -1
	case a.After(*b):
		return 1
	default:
		return 0
	}
}

// compareUpdatedDesc orders more recent updates first and nil last.
func compareUpdatedDesc(a, b *time.Time) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	case a.After(*b):
		return -1
	case a.Before(*b):
		return 1
	default:
		return 0
	}
}
