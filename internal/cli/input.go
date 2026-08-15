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
					err = s.Send(cmd.Context(), args[1])
				}
				if err != nil {
					return err
				}
				return a.emit(map[string]any{"target": s.Name(), "atomic": atomic}, nil, nil)
			})
		},
	}
	cmd.Flags().BoolVar(&atomic, "atomic", false, "deliver and submit as one unit, without verifying")
	return cmd
}

func (a *App) keyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "key <target> <key>...",
		Short: "Send named keys",
		Long: "Send named keys, for example enter, escape, c-c, up." +
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
	return &cobra.Command{
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
				if err := s.Paste(cmd.Context(), text); err != nil {
					return err
				}
				return a.emit(map[string]any{"target": s.Name(), "bytes": len(text)}, nil, nil)
			})
		},
	}
}

func (a *App) screenCmd() *cobra.Command {
	var colors bool
	var history int
	cmd := &cobra.Command{
		Use:   "screen <target>",
		Short: "Read a session's screen",
		Long: "Read a session's screen." +
			"\n\nAn empty capture with alt_screen set means the pane is on the alternate screen and was skipped by design, not that there was nothing there." +
			scriptsNote,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.withSession(cmd, args[0], func(_ *olympus.Olympus, s *olympus.Session) error {
				var opts []olympus.ScreenOption
				if colors {
					opts = append(opts, olympus.WithColors())
				}
				if history > 0 {
					opts = append(opts, olympus.WithHistory(history))
				}
				screen, err := s.Screen(cmd.Context(), opts...)
				if err != nil {
					return err
				}
				return a.emit(screen, screen.Warnings, func(w io.Writer) {
					// The captured text goes to stdout verbatim: it is the
					// payload, not a rendering of one.
					fmt.Fprint(w, screen.Text)
					if !strings.HasSuffix(screen.Text, "\n") && screen.Text != "" {
						fmt.Fprintln(w)
					}
				})
			})
		},
	}
	cmd.Flags().BoolVar(&colors, "colors", false, "keep ANSI escapes in the captured text")
	cmd.Flags().IntVar(&history, "history", 0, "lines of scrollback to include above the visible screen")
	return cmd
}

func (a *App) waitCmd() *cobra.Command {
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "wait <target> <pattern>",
		Short: "Block until a regular expression appears on the screen",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.withSession(cmd, args[0], func(_ *olympus.Olympus, s *olympus.Session) error {
				var opts []olympus.WaitOption
				if timeout > 0 {
					opts = append(opts, olympus.WaitTimeout(timeout))
				}
				screen, err := s.WaitFor(cmd.Context(), args[1], opts...)
				if err != nil {
					return err
				}
				return a.emit(screen, screen.Warnings, func(w io.Writer) {
					fmt.Fprint(w, screen.Text)
				})
			})
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "how long to wait (default 30s)")
	return cmd
}

func (a *App) exitStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "exit-status <target> <marker>",
		Short: "Read a caller-supplied completion marker off the screen",
		Long: "Read a caller-supplied completion marker off the screen, for the wrapper pattern `cmd; echo MARKER:$?`." +
			"\n\nThe marker is always yours to choose and there is deliberately no default: a fixed one would collide with ordinary output or with stale scrollback." +
			scriptsNote,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.withSession(cmd, args[0], func(_ *olympus.Olympus, s *olympus.Session) error {
				code, found, err := s.ExitStatus(cmd.Context(), args[1])
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
}
