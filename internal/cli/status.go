package cli

import (
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/husniadil/olympus"
)

// A StatusReport is what the status verb emits, in every mode.
//
// Set and wait report the same shape as a plain read so a caller parsing the
// output does not need three parsers for one verb.
type StatusReport struct {
	Session string `json:"session"`
	Status  string `json:"status"`
}

func (a *App) statusCmd() *cobra.Command {
	var (
		set     string
		wait    string
		timeout time.Duration
		poll    time.Duration
	)

	cmd := &cobra.Command{
		Use:   "status [target]",
		Short: "Read, set, or wait for a session's status",
		Long: "Read, set, or wait for a session's status — an opaque label a process INSIDE a session leaves for whoever is driving it from outside." +
			"\n\nIt answers a question a screen capture cannot: a program sitting at a prompt and a program halfway through work can render identically, so the only reliable reporter is the program itself." +
			"\n\nOlympus never interprets the value and defines no vocabulary of states. What counts as busy or waiting belongs to the program in the session, not to the terminal." +
			"\n\nWith no target it uses the session this process is running in, which is what lets a program report its own status without being told where it is. Not every backend can carry one; `olympus capabilities` reports it as session_status." +
			scriptsNote,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if set != "" && wait != "" {
				return olympus.ErrUsage
			}

			// Resolved BEFORE the handle is opened, because with no target the
			// answer decides which backend and which server the handle must
			// address — not just which session.
			target, here, err := a.statusTarget(cmd, args)
			if err != nil {
				return err
			}

			ol, err := a.openAt(here)
			if err != nil {
				return err
			}
			defer ol.Close()

			s, err := ol.Open(cmd.Context(), target)
			if err != nil {
				return err
			}

			report := StatusReport{Session: target}
			switch {
			case set != "":
				if err := s.SetStatus(cmd.Context(), set); err != nil {
					return err
				}
				report.Status = set
			case wait != "":
				got, err := s.WaitForStatus(cmd.Context(), wait,
					olympus.WaitTimeout(timeout), olympus.WaitInterval(poll))
				if err != nil {
					return err
				}
				report.Status = got
			default:
				got, err := s.Status(cmd.Context())
				if err != nil {
					return err
				}
				report.Status = got
			}

			return a.emit(report, nil, func(w io.Writer) {
				if report.Status == "" {
					fmt.Fprintf(w, "%s has not reported a status\n", report.Session)
					return
				}
				fmt.Fprintf(w, "%s\t%s\n", report.Session, report.Status)
			})
		},
	}

	cmd.Flags().StringVar(&set, "set", "", "record this status on the target")
	cmd.Flags().StringVar(&wait, "wait", "", "block until the target reports exactly this status")
	cmd.Flags().DurationVar(&timeout, "timeout", olympus.DefaultWaitTimeout, "how long --wait blocks")
	cmd.Flags().DurationVar(&poll, "interval", olympus.DefaultWaitPoll, "how often --wait re-reads the status")
	return cmd
}

// statusTarget falls back to the session this process is running in, and
// returns what it learned about where that session lives.
//
// The second half matters as much as the first. A reporter that resolves its
// NAME but not its SERVER writes onto whichever backend the defaults point at,
// which on any isolated setup — a private socket, a private directory, the
// arrangement Olympus recommends — is a different server entirely. The write
// succeeds, nobody is watching that session, and the waiter times out against a
// session that never heard anything.
//
// A nested session yields no single answer, so there is nothing to fall back to
// and the caller has to name one.
func (a *App) statusTarget(cmd *cobra.Command, args []string) (string, olympus.Identity, error) {
	if len(args) == 1 {
		return args[0], olympus.Identity{}, nil
	}
	here, err := olympus.Self(cmd.Context())
	if err != nil {
		return "", here, err
	}
	if here.Session == "" {
		return "", here, olympus.ErrUsage
	}
	return here.Session, here, nil
}
