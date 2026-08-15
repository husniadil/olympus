package cli

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/husniadil/olympus"
)

func (a *App) panesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "panes [target]",
		Short: "List panes, across every session or within one",
		Long: "List panes: every pane on the backend, or one session's when a target is given." +
			"\n\nA pane id is the only handle some callers hold, and resolving one means seeing them all — which is why this is a question in its own right rather than part of `info`." +
			"\n\nA pane id is NOT unique across rows once views exist: a base and its views share the underlying pane. Dedupe by pane_id keeping the earliest created_at to get one row per logical session." +
			scriptsNote,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ol, err := a.open()
			if err != nil {
				return err
			}
			defer ol.Close()

			target := ""
			if len(args) == 1 {
				target = args[0]
			}
			panes, err := ol.Panes(cmd.Context(), target)
			if err != nil {
				return err
			}
			return a.emit(panes, ol.PaneWarnings(), func(w io.Writer) {
				if len(panes) == 0 {
					fmt.Fprintln(w, "no panes")
					return
				}
				table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
				fmt.Fprintln(table, "PANE\tSESSION\tWINDOW\tDEAD\tCOMMAND\tPATH")
				for _, p := range panes {
					fmt.Fprintf(table, "%s\t%s\t%d\t%s\t%s\t%s\n",
						p.ID, p.SessionName, p.WindowIndex, yesNo(p.Dead), p.CurrentCommand, p.CurrentPath)
				}
				_ = table.Flush()
			})
		},
	}
}

func (a *App) newCmd() *cobra.Command {
	var dir string
	var cols, rows int
	var keepCorpse bool

	cmd := &cobra.Command{
		Use:   "new <name> [-- command...]",
		Short: "Create a session, failing if the name is taken",
		Long: "Create a session, failing with a conflict if one by that name already exists." +
			"\n\nMost callers want `start`, which creates or reuses. This is for a caller that means \"this must not already exist\": checking afterwards by reading `start`'s outcome is a race rather than a check." +
			scriptsNote,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ol, err := a.open()
			if err != nil {
				return err
			}
			defer ol.Close()

			opts := []olympus.SessionOption{}
			if dir != "" {
				opts = append(opts, olympus.In(dir))
			}
			if cols > 0 || rows > 0 {
				opts = append(opts, olympus.Size(cols, rows))
			}
			if keepCorpse {
				opts = append(opts, olympus.KeepCorpse())
			}
			if len(args) > 1 {
				opts = append(opts, olympus.Command(args[1:]...))
			}

			s, err := ol.Create(cmd.Context(), args[0], opts...)
			if err != nil {
				return err
			}
			row := s.Row()
			return a.emit(row, nil, func(w io.Writer) {
				fmt.Fprintf(w, "created %s\n", s.Name())
			})
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "working directory for the session")
	cmd.Flags().IntVar(&cols, "cols", 0, "initial width (ignored by backends with no spawn-time sizing)")
	cmd.Flags().IntVar(&rows, "rows", 0, "initial height (ignored by backends with no spawn-time sizing)")
	cmd.Flags().BoolVar(&keepCorpse, "keep-corpse", false, "leave a dead session to inspect after its command exits")
	return cmd
}

func (a *App) capabilitiesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "capabilities",
		Short: "Report what the resolved backend can do",
		Long: "Report the resolved backend's static capabilities." +
			"\n\nBranch on these rather than on an unsupported error: a capability is a question you can ask before doing the work, and an error is one you can only ask after. `olympus doctor` shows the same facts for every installed backend side by side." +
			scriptsNote,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ol, err := a.open()
			if err != nil {
				return err
			}
			defer ol.Close()

			caps := ol.Capabilities()
			return a.emit(caps, nil, func(w io.Writer) {
				fmt.Fprintf(w, "backend: %s\n", caps.Backend)
				fmt.Fprintf(w, "capabilities: %s\n", describeCapabilities(caps))
			})
		},
	}
}
