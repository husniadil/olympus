package cli

import (
	"github.com/spf13/cobra"

	"github.com/husniadil/olympus"
	"github.com/husniadil/olympus/backend"
)

func (a *App) watchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "watch <target>",
		Short: "Follow a session's output as it is produced",
		Long: "Follow a session's output as it is produced, until you interrupt it or the session ends." +
			"\n\nThis is not `screen` in a loop: a capture shows the pane as it looks now, so anything printed and scrolled past between two captures is gone. Watching taps the output stream instead." +
			"\n\nWhat comes out is raw terminal output, escape sequences included — it is a stream, not a picture. Use `screen` for a picture and `wait` to match on content." +
			"\n\nThere is no --json for this: it is a stream, and wrapping it in an envelope would mean buffering it until it ends.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Said in the help above, enforced here. A stream has no envelope
			// that would not mean buffering it until it ends, which is the one
			// thing a follower must not do — and accepting the flag anyway put
			// raw terminal bytes, escape sequences included, on the channel a
			// parser reads (api §2.3).
			if a.json {
				return backend.Errorf(backend.CodeUsage,
					"watch follows a raw output stream, so it has no --json form: "+
						"drop --json to watch, or use `screen` for a capture you can parse")
			}
			return a.withSession(cmd, args[0], func(_ *olympus.Olympus, s *olympus.Session) error {
				return s.Watch(cmd.Context(), a.Out)
			})
		},
	}
}
