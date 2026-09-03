//go:build darwin || linux

package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/husniadil/olympus"
	"github.com/husniadil/olympus/backend"
)

func (a *App) attachCmd() *cobra.Command {
	var viewer bool
	var keepOthers bool
	var client bool
	var bare bool
	var cols, rows int

	cmd := &cobra.Command{
		Use:   "attach <target>",
		Short: "Hand this terminal to a session",
		Long: "Hand this terminal to a session until you detach.\n\n" +
			"Detaching is the multiplexer's own key, not this terminal's: Ctrl+C is forwarded into the session rather than interpreted here.\n\n" +
			"Other clients are displaced by default; use --keep-others to co-attach instead.\n\n" +
			"SESSION CLIENT (herdr): --client attaches herdr's own session client — sidebar, tabs, mouse selection, scroll and copy — instead of the raw per-pane stream. The target is a workspace (its id or label), a tab (w5:t2) or a pane (w5:p3), and the client is focused onto it first: a workspace is focused, a tab is focused within it, a pane is zoomed within its tab. With --server the client attaches that named session; otherwise it attaches the server on the resolved socket. --bare adds that client with its chrome hidden, so it renders as a plain pane, and implies --client.\n\n" +
			"BARE (tmux): --bare attaches a fresh view onto the session instead of the session itself — no status bar, no prefix — and kills the view when this attach ends. The target may be <session> or <session>:<window> (an index or a name) to show one window: a view keeps its own current window, so nobody else's is moved. The base's other clients are not displaced, so --keep-others is accepted and has nothing to do; --viewer makes the view read-only. The active pane within a window is shared with the base, so a multi-pane window shows the base's active pane. zmx and meja refuse --bare.\n\n" +
			"EXIT CODE: once the session is confirmed to exist, this hands off to the multiplexer's own client and exits with ITS status. An exit of 3 here is not necessarily a missing session.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// A handoff has no structured form, and cannot be given one.
			//
			// Attaching hands stdout to the multiplexer's client, which then owns
			// it: everything the session draws goes there, and so does the
			// client's own failure text. api §2.3 promises a --json consumer that
			// stdout carries the payload and nothing else, and there is no point
			// at which those bytes could be taken back — so the promise is kept
			// by refusing rather than by pretending.
			//
			// A usage error because one argument fixes it (§12), and it costs the
			// caller nothing they were getting: the output was unparseable either
			// way.
			if a.json {
				return backend.Errorf(backend.CodeUsage,
					"attach hands this terminal to the session, so it has no --json form: "+
						"drop --json to attach, or use `info` to ask about the session instead")
			}
			var opts []olympus.AttachOption
			if viewer {
				opts = append(opts, olympus.AsViewer())
			}
			if keepOthers {
				opts = append(opts, olympus.KeepOtherClients())
			}
			if bare {
				opts = append(opts, olympus.AsBare())
			} else if client {
				opts = append(opts, olympus.WithSessionClient())
			}
			if cols > 0 && rows > 0 {
				opts = append(opts, olympus.AttachSize(cols, rows))
			}

			in, _ := a.In.(*os.File)
			out, _ := a.Out.(*os.File)
			errOut, _ := a.Err.(*os.File)

			runAttach := func(s *olympus.Session) error {
				code, err := s.Attach(cmd.Context(), in, out, errOut, opts...)
				if err != nil {
					return err
				}
				if code != 0 {
					// The client's own status, carried out unchanged.
					return &exitCodeError{code: exitCode(code)}
				}
				return nil
			}

			// A bare target on tmux is `<session>:<window>`, which is not a
			// session, so it must NOT go through withSession — that resolves
			// the target to a session and probes for it. The ergonomic layer
			// splits it, resolves the session half, and reports a missing
			// session or window itself. On herdr the session client takes the
			// same workspace, tab or pane target every other verb takes, so
			// it is resolved and probed like one.
			if bare {
				ol, err := a.open()
				if err != nil {
					return err
				}
				defer ol.Close()
				if ol.Backend() == backend.Tmux {
					return runAttach(ol.OpenSessionName(args[0]))
				}
				s, err := ol.Open(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				return runAttach(s)
			}

			return a.withSession(cmd, args[0], func(_ *olympus.Olympus, s *olympus.Session) error {
				return runAttach(s)
			})
		},
	}
	cmd.Flags().BoolVar(&viewer, "viewer", false, "attach read-only: no input, and no resizing")
	cmd.Flags().BoolVar(&keepOthers, "keep-others", false, "co-attach instead of displacing other clients")
	cmd.Flags().BoolVar(&client, "client", false, "attach the multiplexer's session client (sidebar, selection, scroll, copy), focused onto the target (herdr only)")
	cmd.Flags().BoolVar(&bare, "bare", false, "attach as a plain pane, no chrome: on herdr the session client with its chrome hidden (implies --client); on tmux a throwaway view, with <session>:<window> to pick a window (see BARE)")
	cmd.Flags().IntVar(&cols, "cols", 0, "initial width, for a caller whose stdin is not a terminal")
	cmd.Flags().IntVar(&rows, "rows", 0, "initial height, for a caller whose stdin is not a terminal")
	return cmd
}
