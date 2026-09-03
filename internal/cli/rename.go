package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// A RenameReport is what the rename verb emits.
type RenameReport struct {
	Target string `json:"target"`
	Name   string `json:"name"`
}

func (a *App) renameCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rename <target> <name>",
		Short: "Give a session, window, tab or pane a new name",
		Long: "Give a target a new name in place: what listings and every client show afterwards, and what it answers to.\n\n" +
			"On herdr the level the target names is renamed — a workspace, a tab (w5:t2) or a pane (w5:p3) — and a label spelled like an id is refused. On tmux a session is renamed, `<session>:<window>` renames the window, and a pane id sets the pane's title.\n\n" +
			"Refused as UNSUPPORTED where names are fixed at creation (zmx, meja); `olympus capabilities` reports it as rename." +
			scriptsNote,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ol, err := a.open()
			if err != nil {
				return err
			}
			defer ol.Close()
			if err := ol.Rename(cmd.Context(), args[0], args[1]); err != nil {
				return err
			}
			report := RenameReport{Target: args[0], Name: args[1]}
			return a.emit(report, nil, func(w io.Writer) {
				fmt.Fprintf(w, "renamed %s to %s\n", report.Target, report.Name)
			})
		},
	}
}
