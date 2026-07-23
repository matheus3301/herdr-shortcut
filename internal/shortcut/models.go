package shortcut

import "time"

// Member is the authenticated Shortcut member (GET /member).
type Member struct {
	ID          string `json:"id"`
	MentionName string `json:"mention_name"`
	Name        string `json:"name"`
}

// Workflow is a Shortcut workflow with its states (GET /workflows).
type Workflow struct {
	ID     int64           `json:"id"`
	Name   string          `json:"name"`
	States []WorkflowState `json:"states"`
}

// WorkflowState is a single state within a workflow. Type is one of "backlog",
// "unstarted", "started", or "done".
type WorkflowState struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Position int64  `json:"position"`
}

// Label is a slim Story label; only the name is used for display and filtering.
type Label struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Story is the subset of Shortcut Story fields used by this plugin. Nullable
// Shortcut fields are decoded as pointers so a JSON null does not fail decoding
// and is distinguishable from a zero value.
type Story struct {
	ID                     int64      `json:"id"`
	Name                   string     `json:"name"`
	Description            string     `json:"description"`
	AppURL                 string     `json:"app_url"`
	StoryType              string     `json:"story_type"`
	WorkflowStateID        int64      `json:"workflow_state_id"`
	Estimate               *int64     `json:"estimate"`
	Deadline               *time.Time `json:"deadline"`
	Labels                 []Label    `json:"labels"`
	FormattedVCSBranchName *string    `json:"formatted_vcs_branch_name"`
	GroupID                *string    `json:"group_id"`
	EpicID                 *int64     `json:"epic_id"`
	UpdatedAt              *time.Time `json:"updated_at"`
	Completed              bool       `json:"completed"`
	Archived               bool       `json:"archived"`
}

// LabelNames returns the label names in order.
func (s Story) LabelNames() []string {
	names := make([]string, 0, len(s.Labels))
	for _, l := range s.Labels {
		names = append(names, l.Name)
	}
	return names
}

// BranchName returns the Shortcut-suggested VCS branch name, or "" when absent.
func (s Story) BranchName() string {
	if s.FormattedVCSBranchName == nil {
		return ""
	}
	return *s.FormattedVCSBranchName
}

// Team returns the Story's group (team) UUID, or "" when absent.
func (s Story) Team() string {
	if s.GroupID == nil {
		return ""
	}
	return *s.GroupID
}

// storySearchResults is the GET /search/stories response envelope. Next is the
// URL path and query string of the next page, or null on the last page.
type storySearchResults struct {
	Total int64   `json:"total"`
	Data  []Story `json:"data"`
	Next  *string `json:"next"`
}
