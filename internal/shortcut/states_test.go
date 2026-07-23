package shortcut

import (
	"testing"
	"time"
)

func TestBuildStateIndexAndResolve(t *testing.T) {
	t.Parallel()
	workflows := []Workflow{
		{ID: 1, Name: "Eng", States: []WorkflowState{
			{ID: 100, Name: "Backlog", Type: "backlog", Position: 0},
			{ID: 101, Name: "In Progress", Type: "started", Position: 1},
		}},
		{ID: 2, Name: "Design", States: []WorkflowState{
			{ID: 200, Name: "Ready", Type: "unstarted", Position: 0},
		}},
	}
	idx := BuildStateIndex(workflows)
	stories := []Story{
		{ID: 1, WorkflowStateID: 101},
		{ID: 2, WorkflowStateID: 999}, // unknown
	}
	resolved := Resolve(stories, idx)
	if resolved[0].State.Name != "In Progress" || !resolved[0].State.Known {
		t.Errorf("state 101 = %+v", resolved[0].State)
	}
	if resolved[1].State.Name != UnknownStateName || resolved[1].State.Known {
		t.Errorf("unknown state = %+v", resolved[1].State)
	}
}

func tp(tm time.Time) *time.Time { return &tm }

func TestSortOrder(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	earlier := base.Add(-24 * time.Hour)
	deadlineSoon := base.Add(48 * time.Hour)
	deadlineLate := base.Add(72 * time.Hour)

	list := []ResolvedStory{
		// unknown state, should sort last
		{Story: Story{ID: 50}, State: StoryState{Type: "", Position: 0}},
		// backlog
		{Story: Story{ID: 40}, State: StoryState{Type: "backlog", Position: 5}},
		// started, position 1, no deadline, updated earlier
		{Story: Story{ID: 30, UpdatedAt: tp(earlier)}, State: StoryState{Type: "started", Position: 1}},
		// started, position 0, deadline late
		{Story: Story{ID: 20, Deadline: tp(deadlineLate)}, State: StoryState{Type: "started", Position: 0}},
		// started, position 0, deadline soon
		{Story: Story{ID: 10, Deadline: tp(deadlineSoon)}, State: StoryState{Type: "started", Position: 0}},
	}
	Sort(list)
	gotOrder := make([]int64, len(list))
	for i, s := range list {
		gotOrder[i] = s.ID
	}
	// started+pos0+deadline soon (10), started+pos0+deadline late (20),
	// started+pos1 (30), backlog (40), unknown (50)
	want := []int64{10, 20, 30, 40, 50}
	for i := range want {
		if gotOrder[i] != want[i] {
			t.Fatalf("sort order = %v, want %v", gotOrder, want)
		}
	}
}

func TestSortDeadlineNilLastAndUpdatedDesc(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	list := []ResolvedStory{
		{Story: Story{ID: 3, UpdatedAt: tp(base.Add(-time.Hour))}, State: StoryState{Type: "started"}},
		{Story: Story{ID: 2, UpdatedAt: tp(base)}, State: StoryState{Type: "started"}},
		{Story: Story{ID: 1, Deadline: tp(base)}, State: StoryState{Type: "started"}},
	}
	Sort(list)
	if list[0].ID != 1 {
		t.Errorf("story with deadline should sort before no-deadline; got %d first", list[0].ID)
	}
	// Between the two no-deadline stories, more recent updated_at wins.
	if list[1].ID != 2 || list[2].ID != 3 {
		t.Errorf("updated-desc order wrong: %d then %d", list[1].ID, list[2].ID)
	}
}
