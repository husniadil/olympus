//go:build darwin || linux

package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/husniadil/olympus"
)

func (a *App) attachCmd() *cobra.Command {
	var viewer bool
	var keepOthers bool
	var cols, rows int

	cmd := &cobra.Command{
		Use:   "attach <target>",
		Short: "Hand this terminal to a session",
		Long: "Hand this terminal to a session until you detach.\n\n" +
			"Detaching is the multiplexer's own key, not this terminal's: Ctrl+C is forwarded into the session rather than interpreted here.\n\n" +
			"Other clients are displaced by default; use --keep-others to co-attach instead.\n\n" +
			"EXIT CODE: once the session is confirmed to exist, this hands off to the multiplexer's own client and exits with ITS status. An exit of 3 here is not necessarily a missing session.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.withSession(cmd, args[0], func(_ *olympus.Olympus, s *olympus.Session) error {
				var opts []olympus.AttachOption
				if viewer {
					opts = append(opts, olympus.AsViewer())
				}
				if keepOthers {
					opts = append(opts, olympus.KeepOtherClients())
				}
				if cols > 0 && rows > 0 {
					opts = append(opts, olympus.AttachSize(cols, rows))
				}

				in, _ := a.In.(*os.File)
				out, _ := a.Out.(*os.File)
				errOut, _ := a.Err.(*os.File)

				code, err := s.Attach(cmd.Context(), in, out, errOut, opts...)
				if err != nil {
					return err
				}
				if code != 0 {
					// The client's own status, carried out unchanged.
					return &exitCodeError{code: exitCode(code)}
				}
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&viewer, "viewer", false, "attach read-only: no input, and no resizing")
	cmd.Flags().BoolVar(&keepOthers, "keep-others", false, "co-attach instead of displacing other clients")
	cmd.Flags().IntVar(&cols, "cols", 0, "initial width, for a caller whose stdin is not a terminal")
	cmd.Flags().IntVar(&rows, "rows", 0, "initial height, for a caller whose stdin is not a terminal")
	return cmd
}
