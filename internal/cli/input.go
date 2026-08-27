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

// withSession opens a handle without creating anything.
//
// Every verb that addresses an existing session goes through here, so none of
// them can accidentally bring a session into being by being asked about it.
func (a *App) withSession(cmd *cobra.Command, target string, fn func(*olympus.Olympus, *olympus.Session) error) error {
	ol, err := a.open()
	if err != nil {
		return err
	}
	defer ol.Close()

	s, err := ol.Open(cmd.Context(), target)
	if err != nil {
		return err
	}
	return fn(ol, s)
}

func (a *App) typeCmd() *cobra.Command {
	var submit bool
	cmd := &cobra.Command{
		Use:   "type <target> <text>",
		Short: "Place literal text in the input line without submitting it",
		Long: "Place literal text in the input line without submitting it." +
			"\n\nPlacing text and submitting it are separate operations. Use --submit for an unverified terminator, or `olympus send` to confirm the text landed before submitting." +
			scriptsNote,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.withSession(cmd, args[0], func(_ *olympus.Olympus, s *olympus.Session) error {
				if err := s.Type(cmd.Context(), args[1]); err != nil {
					return err
				}
				if submit {
					if err := s.Submit(cmd.Context()); err != nil {
						return err
					}
				}
				return a.emit(map[string]any{"target": s.Name(), "submitted": submit}, nil, nil)
			})
		},
	}
	cmd.Flags().BoolVar(&submit, "submit", false, "send the terminator afterwards, without verifying the text landed")
	return cmd
}

func (a *App) sendCmd() *cobra.Command {
	var atomic bool
	var noEnter bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "send <target> <text>",
		Short: "Deliver text, confirm it landed, then submit it",
		Long: "Deliver text, confirm it is on screen, and only then submit it." +
			"\n\nWith --atomic the text and its terminator are delivered as one unit instead, which is retry-safe but skips the on-screen check. The two cannot be combined: verifying needs a separate terminator, and any cross-invocation retry of a verified send re-types the text and doubles it." +
			scriptsNote,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.withSession(cmd, args[0], func(_ *olympus.Olympus, s *olympus.Session) error {
				var err error
				if atomic {
					err = s.SendAtomic(cmd.Context(), args[1])
				} else {
					var opts []olympus.SendOption
					if timeout > 0 {
						opts = append(opts, olympus.VerifyBudget(timeout))
					}
					if noEnter {
						opts = append(opts, olympus.WithoutSubmit())
					}
					err = s.Send(cmd.Context(), args[1], opts...)
				}
				if err != nil {
					return err
				}
				return a.emit(map[string]any{"target": s.Name(), "atomic": atomic, "submitted": !noEnter || atomic}, nil, nil)
			})
		},
	}
	cmd.Flags().BoolVar(&atomic, "atomic", false, "deliver and submit as one unit, without verifying")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "per-attempt verify budget, spent twice (default 5s)")
	cmd.Flags().BoolVar(&noEnter, "no-enter", false, "confirm the text landed but leave it unsubmitted")
	return cmd
}

func (a *App) pressCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "press <target> <key>...",
		Short: "Press named keys",
		Long: "Press named keys, for example enter, escape, c-c, up." +
			"\n\nKey names are Olympus's own and are translated per backend, so the same name works everywhere." +
			scriptsNote,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			keys := make([]backend.Key, 0, len(args)-1)
			for _, name := range args[1:] {
				keys = append(keys, backend.Key(strings.ToLower(name)))
			}
			return a.withSession(cmd, args[0], func(_ *olympus.Olympus, s *olympus.Session) error {
				if err := s.Press(cmd.Context(), keys...); err != nil {
					return err
				}
				return a.emit(map[string]any{"target": s.Name(), "keys": args[1:]}, nil, nil)
			})
		},
	}
}

func (a *App) pasteCmd() *cobra.Command {
	var submit bool
	cmd := &cobra.Command{
		Use:   "paste <target> [text]",
		Short: "Place multi-line text in the input line without submitting the last line",
		Long: "Place multi-line text in the input line. Pass - to read the text from stdin." +
			"\n\nThe final line is never submitted without a separate terminator. Whether INTERMEDIATE lines execute depends on the consumer and genuinely differs between backends." +
			scriptsNote,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			text := ""
			if len(args) == 2 {
				text = args[1]
			}
			if text == "-" || len(args) == 1 {
				read, err := io.ReadAll(a.In)
				if err != nil {
					return backend.Wrapf(backend.CodeUsage, err, "reading the text from stdin")
				}
				text = string(read)
			}
			return a.withSession(cmd, args[0], func(_ *olympus.Olympus, s *olympus.Session) error {
				var err error
				if submit {
					err = s.PasteAndSubmit(cmd.Context(), text)
				} else {
					err = s.Paste(cmd.Context(), text)
				}
				if err != nil {
					return err
				}
				return a.emit(map[string]any{"target": s.Name(), "bytes": len(text), "submitted": submit}, nil, nil)
			})
		},
	}
	cmd.Flags().BoolVar(&submit, "enter", false, "submit the final line afterwards, retrying the terminator once")
	return cmd
}

func (a *App) screenCmd() *cobra.Command {
	var colors bool
	var history int
	cmd := &cobra.Command{
		Use:   "screen <target>...",
		Short: "Read the screen of one or more sessions",
		Long: "Read the screen of one or more sessions, in a single call." +
			"\n\nA target on the alternate screen IS captured: its visible grid is the only way to observe a full-screen application, and meta.alt_screen tells you there is no scrollback behind it. A --history request against such a target is dropped, with a warning." +
			scriptsNote,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ol, err := a.open()
			if err != nil {
				return err
			}
			defer ol.Close()

			var opts []olympus.ScreenOption
			if colors {
				opts = append(opts, olympus.WithColors())
			}
			if history > 0 {
				opts = append(opts, olympus.WithHistory(history))
			}

			captured, err := ol.Screens(cmd.Context(), args, opts...)
			if err != nil {
				return err
			}
			return a.emit(captured, captured.Warnings, func(w io.Writer) {
				for _, target := range args {
					text := captured.Screens[target]
					// Only labelled when there is more than one, so piping a
					// single screen stays byte-for-byte the screen.
					if len(args) > 1 {
						fmt.Fprintf(w, "=== %s ===\n", target)
					}
					fmt.Fprint(w, text)
					if text != "" && !strings.HasSuffix(text, "\n") {
						fmt.Fprintln(w)
					}
				}
			})
		},
	}
	cmd.Flags().BoolVar(&colors, "colors", false, "keep ANSI escapes in the captured text")
	cmd.Flags().IntVar(&history, "history", 0, "lines of scrollback to include above the visible screen")
	return cmd
}

func (a *App) waitCmd() *cobra.Command {
	var timeout, interval time.Duration
	cmd := &cobra.Command{
		Use:   "wait <target> <pattern>",
		Short: "Block until a regular expression appears on the screen",
		Long: "Block until a regular expression appears on the screen." +
			"\n\nThe matched line is reported alongside the screen, so a caller does not have to run the match again to find out which line it was." +
			scriptsNote,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.withSession(cmd, args[0], func(_ *olympus.Olympus, s *olympus.Session) error {
				var opts []olympus.WaitOption
				if timeout > 0 {
					opts = append(opts, olympus.WaitTimeout(timeout))
				}
				if interval > 0 {
					opts = append(opts, olympus.WaitInterval(interval))
				}
				screen, err := s.WaitFor(cmd.Context(), args[1], opts...)
				if err != nil {
					return err
				}
				return a.emit(screen, screen.Warnings, func(w io.Writer) {
					if screen.Line != "" {
						fmt.Fprintln(w, screen.Line)
						return
					}
					fmt.Fprint(w, screen.Text)
				})
			})
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "how long to wait (default 30s)")
	cmd.Flags().DurationVar(&interval, "interval", 0, "how often to re-read the screen (default 250ms)")
	return cmd
}

func (a *App) exitStatusCmd() *cobra.Command {
	var lines int
	cmd := &cobra.Command{
		Use:   "exit-status <target> <marker>",
		Short: "Read a caller-supplied completion marker off the screen",
		Long: "Read a caller-supplied completion marker off the screen, for the wrapper pattern `cmd; echo DONE:$?`." +
			"\n\nThe marker is the whole prefix, separator included: for that wrapper it is `DONE:`, not `DONE`. The exit code is the token immediately after it, and Olympus skips no separator of its own — it has no opinion on the format." +
			"\n\nGetting that wrong fails silently, which is why it is said here: an unmatched marker reports not-found, and not-found legitimately means the command has not finished yet." +
			"\n\nThe marker is always yours to choose and there is deliberately no default: a fixed one would collide with ordinary output or with stale scrollback." +
			scriptsNote,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.withSession(cmd, args[0], func(_ *olympus.Olympus, s *olympus.Session) error {
				code, found, err := s.ExitStatus(cmd.Context(), args[1], lines)
				if err != nil {
					return err
				}
				data := map[string]any{"found": found}
				if found {
					data["exit_code"] = code
				}
				return a.emit(data, nil, func(w io.Writer) {
					if !found {
						fmt.Fprintln(w, "no marker on screen")
						return
					}
					fmt.Fprintf(w, "%d\n", code)
				})
			})
		},
	}
	cmd.Flags().IntVar(&lines, "lines", 0, "scrollback window to search (default 10000; ignored where scrollback is native)")
	return cmd
}
