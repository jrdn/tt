package cmd

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/jrdn/tt/internal/tui"
)

func newTuiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Interactive TUI",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			m := tui.NewModel(store)
			p := tea.NewProgram(m, tea.WithAltScreen())
			_, err := p.Run()
			return err
		},
	}
}
