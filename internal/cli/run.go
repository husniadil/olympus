package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/husniadil/olympus"
	"github.com/husniadil/olympus/backend"
)

func (a *App) runCmd() *cobra.Command {
	var detach bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "run [target] <command>",
		Short: "Run a command in a session and wait for it to finish",
		Long: strings.TrimSpace(`
Run a command in a session and wait for it to finish.

With no target, the command runs in a throwaway session created for it and
killed afterwards — on success, failure and timeout alike.

EXIT CODE: without --json this exits with the COMMAND's own exit code, so it
composes in a pipeline exactly like running the command directly. Genuine
infrastructure failures still use Olympus's own codes. With --json it exits 0
for any successful run and the command's status is in data.exit_code instead.`) +
			scriptsNote,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var opts []olympus.RunOption
			if timeout > 0 {
				opts = append(opts, olympus.RunTimeout(timeout))
			}

			if len(args) == 1 {
				return a.runThrowaway(cmd, args[0], detach, opts)
			}

			return a.withSession(cmd, args[0], func(_ *olympus.Olympus, s *olympus.Session) error {
				if detach {
					job, err := s.Start(cmd.Context(), args[1], opts...)
					if err != nil {
						return err
					}
					return a.emit(olympus.Started{CommandID: job.ID()}, nil, func(w io.Writer) {
						fmt.Fprintln(w, job.ID())
					})
				}

				result, err := s.Exec(cmd.Context(), args[1], opts...)
				if err != nil {
					return err
				}
				if err := a.emit(result, nil, func(w io.Writer) {
					fmt.Fprint(w, result.Output)
					if result.Output != "" && !strings.HasSuffix(result.Output, "\n") {
						fmt.Fprintln(w)
					}
				}); err != nil {
					return err
				}

				// The deviation, applied locally and never taught to the shared
				// mapping. A completed run has two independent outcomes —
				// whether the protocol worked, and what the command's own
				// status was — and a successful run carrying a second,
				// unrelated exit code is not a failure. Teaching that to the
				// shared path would leak run-specific meaning into code every
				// operation shares (behavior §12.1).
				if !a.json && result.ExitCode != 0 {
					return &exitCodeError{code: exitCode(result.ExitCode)}
				}
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&detach, "detach", false, "start the command and return an id to poll instead of waiting")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "how long to wait for the command (default 60s)")
	return cmd
}

// runThrowaway runs a command in a session made for it and killed afterwards.
func (a *App) runThrowaway(cmd *cobra.Command, command string, detach bool, opts []olympus.RunOption) error {
	if detach {
		// There would be nothing left to poll: the session is killed the
		// moment the run returns (behavior §6.10).
		return backend.Errorf(backend.CodeUsage,
			"a detached run needs a target: a throwaway session is killed when the run returns, leaving nothing to poll")
	}

	ol, err := a.open()
	if err != nil {
		return err
	}
	defer ol.Close()

	result, warnings, err := ol.RunOnce(cmd.Context(), command, opts)
	if err != nil {
		return err
	}
	if err := a.emit(result, warnings, func(w io.Writer) {
		fmt.Fprint(w, result.Output)
		if result.Output != "" && !strings.HasSuffix(result.Output, "\n") {
			fmt.Fprintln(w)
		}
	}); err != nil {
		return err
	}
	if !a.json && result.ExitCode != 0 {
		return &exitCodeError{code: exitCode(result.ExitCode)}
	}
	return nil
}

func (a *App) pollCmd() *cobra.Command {
	var lines int
	cmd := &cobra.Command{
		// Top-level, not a subcommand of run. Making it `run poll <target> <id>`
		// would reserve "poll" as a session name: a session literally named
		// poll becomes unaddressable by run, because subcommand resolution
		// wins (api §1.1).
		Use:   "poll <target> <id>",
		Short: "Ask whether a detached run has finished",
		Long: strings.TrimSpace(`
Ask whether a detached run has finished.

Nothing is written down: the id is baked into the markers the run left in the
session's own scrollback, and polling re-scans for them. Branch on status first
— exit_code is present only when status is completed, never a placeholder zero.

An id that was never issued and a command still running are indistinguishable,
and both read as pending. Bounding how long you wait is yours to do.`) +
			scriptsNote,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ol, err := a.open()
			if err != nil {
				return err
			}
			defer ol.Close()

			// Deliberately not routed through Open: polling answers about a
			// COMMAND, and a target that never existed answers died rather
			// than not-found (behavior §6.8).
			s, err := ol.Open(cmd.Context(), args[0])
			if err != nil {
				if backend.CodeOf(err) == backend.CodeSessionNotFound {
					result := olympus.PollResult{State: "died", Reason: "the session is no longer present"}
					return a.emit(result, nil, func(w io.Writer) {
						fmt.Fprintf(w, "died: %s\n", result.Reason)
					})
				}
				return err
			}

			var opts []olympus.RunOption
			if lines > 0 {
				opts = append(opts, olympus.PollWindow(lines))
			}
			result, err := s.Poll(cmd.Context(), args[1], opts...)
			if err != nil {
				return err
			}
			return a.emit(result, result.Warnings, func(w io.Writer) {
				switch result.State {
				case "completed":
					fmt.Fprint(w, result.Output)
					if result.Output != "" && !strings.HasSuffix(result.Output, "\n") {
						fmt.Fprintln(w)
					}
					fmt.Fprintf(w, "completed (exit %d)\n", *result.ExitCode)
				case "died":
					fmt.Fprintf(w, "died: %s\n", result.Reason)
				default:
					fmt.Fprintln(w, "pending")
				}
			})
		},
	}
	cmd.Flags().IntVar(&lines, "lines", 0, "scrollback window to search for the completion marker (default 10000; ignored where scrollback is native)")
	return cmd
}
