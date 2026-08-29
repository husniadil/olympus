package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/husniadil/olympus"
	"github.com/husniadil/olympus/backend"
)

// scriptsNote appears in the help of every command with a table, so a person
// reading it learns which output to parse before they write the script rather
// than after it breaks.
const scriptsNote = "\n\nHuman-readable output is not stable. Use --json in scripts."

func (a *App) startCmd() *cobra.Command {
	var dir string
	var cols, rows int
	var keepCorpse bool

	cmd := &cobra.Command{
		Use:   "start <name> [-- command...]",
		Short: "Create a session, or reuse one that is already alive",
		Long: "Create a session, or reuse one that is already alive." +
			"\n\nThis is idempotent: it reports whether the session was created, reused, or reaped and replaced." +
			"\n\nA command after `--` is executed as the session's process, never typed into a shell. Not every backend can choose a pane's process; `olympus capabilities` reports it as spawn_command, and a backend without it refuses the command rather than typing it." +
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

			s, err := ol.Session(cmd.Context(), args[0], opts...)
			if err != nil {
				return err
			}
			row := s.Row()
			row.Outcome = s.Outcome()
			return a.emit(row, nil, func(w io.Writer) {
				fmt.Fprintf(w, "%s %s\n", row.Outcome, s.Name())
			})
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "working directory for the session")
	cmd.Flags().IntVar(&cols, "cols", 0, "initial width (ignored by backends with no spawn-time sizing)")
	cmd.Flags().IntVar(&rows, "rows", 0, "initial height (ignored by backends with no spawn-time sizing)")
	cmd.Flags().BoolVar(&keepCorpse, "keep-corpse", false, "leave a dead session to inspect after its command exits")
	return cmd
}

func (a *App) lsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List sessions on the resolved backend",
		Long: "List sessions on the resolved backend." +
			"\n\nSessions are backend-scoped: they never migrate and never merge, so this never shows sessions belonging to another backend." +
			scriptsNote,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ol, err := a.open()
			if err != nil {
				return err
			}
			defer ol.Close()

			sessions, err := ol.Sessions(cmd.Context())
			if err != nil {
				return err
			}
			// Never null: an empty collection serializes as [].
			if sessions == nil {
				sessions = []backend.Session{}
			}

			return a.emit(sessions, nil, func(w io.Writer) {
				if len(sessions) == 0 {
					// The resolved backend is named here on purpose. An empty
					// list that should not be empty is exactly when a user
					// needs to learn that backends are scoped (§0.4).
					fmt.Fprintf(w, "no sessions on the %s backend\n", ol.Backend())
					return
				}
				table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
				fmt.Fprintln(table, "NAME\tLIVENESS\tATTACHED\tDEAD\tDIRECTORY")
				for _, s := range sessions {
					fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n",
						s.Name, s.Liveness.OrUnknown(), yesNo(s.Attached), yesNo(s.Dead), s.CWD)
				}
				_ = table.Flush()
			})
		},
	}
}

func (a *App) stopCmd() *cobra.Command {
	var force bool
	var presses int
	var interruptTimeout time.Duration
	cmd := &cobra.Command{
		Use:   "stop <target>",
		Short: "End a session, interrupting it before forcing",
		Long: "End a session, interrupting it before forcing." +
			"\n\nReports which happened: gone (it was already absent), graceful (it stopped on the interrupt), or killed (it had to be forced). All three are successes." +
			scriptsNote,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ol, err := a.open()
			if err != nil {
				return err
			}
			defer ol.Close()

			var opts []olympus.StopOption
			if force {
				opts = append(opts, olympus.Force())
			}
			if presses > 0 {
				opts = append(opts, olympus.Presses(presses))
			}
			if interruptTimeout > 0 {
				opts = append(opts, olympus.InterruptTimeout(interruptTimeout))
			}
			stopped, err := ol.Stop(cmd.Context(), args[0], opts...)
			if err != nil {
				return err
			}
			return a.emit(stopped, stopped.Warnings, func(w io.Writer) {
				fmt.Fprintf(w, "%s %s\n", stopped.Outcome, args[0])
			})
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "skip the graceful attempt entirely")
	cmd.Flags().IntVar(&presses, "presses", 0, "interrupts to send before waiting (default 1)")
	cmd.Flags().DurationVar(&interruptTimeout, "interrupt-timeout", 0, "how long to wait for the interrupt to work before forcing (default 2s)")
	return cmd
}

func (a *App) infoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info <target>",
		Short: "Show a session's detail and whether it is present",
		Long: "Show a session's detail and whether it is present." +
			"\n\nThis does NOT fail on a target that does not exist: it reports state as present, absent, or error. Absent means definitely gone; error means the backend could not be asked. A caller reconciling state needs those to be different answers." +
			scriptsNote,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ol, err := a.open()
			if err != nil {
				return err
			}
			defer ol.Close()

			info, err := ol.Info(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return a.emit(info, info.Warnings, func(w io.Writer) {
				fmt.Fprintf(w, "state: %s\n", info.State)
				if info.Session != nil {
					fmt.Fprintf(w, "name: %s\nliveness: %s\ndirectory: %s\n",
						info.Session.Name, info.Session.Liveness.OrUnknown(), info.Session.CWD)
				}
				if len(info.Panes) > 0 {
					fmt.Fprintln(w, "\npanes:")
					table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
					fmt.Fprintln(table, "  PANE\tDEAD\tCOMMAND\tPATH")
					for _, p := range info.Panes {
						fmt.Fprintf(table, "  %s\t%s\t%s\t%s\n", p.ID, yesNo(p.Dead), p.CurrentCommand, p.CurrentPath)
					}
					_ = table.Flush()
				}
				fmt.Fprintf(w, "\ncapabilities: %s\n", describeCapabilities(info.Capabilities))
			})
		},
	}
}

func describeCapabilities(caps backend.Capabilities) string {
	var on []string
	for _, c := range []struct {
		name string
		set  bool
	}{
		{"native-scrollback", caps.NativeScrollback},
		{"views", caps.Views},
		{"remain-on-exit", caps.RemainOnExit},
		{"server-env", caps.ServerEnv},
		{"control-keys", caps.ControlKeys},
		{"alt-screen-tracking", caps.TracksAltScreen},
	} {
		if c.set {
			on = append(on, c.name)
		}
	}
	if len(on) == 0 {
		return "none"
	}
	return strings.Join(on, ", ")
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
