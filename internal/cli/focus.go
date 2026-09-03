package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// A FocusReport is what the focus verb emits.
type FocusReport struct {
	Target string `json:"target"`
}

func (a *App) focusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "focus <target>",
		Short: "Steer the server's focus onto a target, without attaching",
		Long: "Steer the server's focus onto a target: the workspace, tab or pane its session client shows.\n\n" +
			"A session client (`attach --client`, `attach --bare` on herdr) shows whatever the SERVER has focused, and an attach steers the server onto its target first. Every client on the server shows that one focus, so a caller holding two clients onto two targets sees both show the target attached last; bringing one to the front means steering the server again, which is what this does. A workspace is focused; a tab is focused within it; a pane is zoomed within its tab.\n\n" +
			"On tmux, clients attached to one plain session share its current window and pane, so `<session>:<window>` selects the window and a pane id selects its window and then the pane; a bare session name has nothing to steer. Refused as UNSUPPORTED on zmx and meja, whose sessions are one pane; `olympus capabilities` reports it as focus." +
			scriptsNote,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ol, err := a.open()
			if err != nil {
				return err
			}
			defer ol.Close()
			if err := ol.Focus(cmd.Context(), args[0]); err != nil {
				return err
			}
			report := FocusReport{Target: args[0]}
			return a.emit(report, nil, func(w io.Writer) {
				fmt.Fprintf(w, "focused %s\n", report.Target)
			})
		},
	}
}
