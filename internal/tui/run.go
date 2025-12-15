package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

func Run(posDir string) error {
	m, err := New(posDir)
	if err != nil {
		return err
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err = p.Run()
	return err
}
