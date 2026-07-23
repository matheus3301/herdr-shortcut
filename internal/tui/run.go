package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// Run builds and runs the picker program with mouse support, returning the
// final model so the caller can inspect the launch outcome.
func Run(deps Deps) (Model, error) {
	m := New(deps)
	opts := []tea.ProgramOption{tea.WithAltScreen(), tea.WithMouseCellMotion()}
	if deps.Context != nil {
		opts = append(opts, tea.WithContext(deps.Context))
	}
	final, err := tea.NewProgram(m, opts...).Run()
	fm, _ := final.(Model)
	return fm, err
}

// LaunchResult returns the last launch outcome, or nil if no launch completed.
func (m Model) LaunchResult() *LaunchResult { return m.launchResult }

// Launched reports whether a launch completed successfully.
func (m Model) Launched() bool {
	return m.launchResult != nil && m.launchResult.Err == nil
}

// HasStories reports whether any Stories are currently loaded.
func (m Model) HasStories() bool { return len(m.stories) > 0 }

// DialogOpen reports whether the launch dialog is open.
func (m Model) DialogOpen() bool { return m.dialog != nil }
