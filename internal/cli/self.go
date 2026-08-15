package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/husniadil/olympus"
)

func (a *App) selfCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "self",
		Short: "Report which session this process is running inside",
		Long: "Report which session THIS process is running inside, if any." +
			"\n\nIt is for a program that needs to tell another program where to reach it — reply into this session — which is impossible if it cannot name its own." +
			"\n\nBeing outside a session is an answer, not a failure: it exits 0 with inside false. It also does not take --backend or --socket, because the answer is about where this process actually is, not about how you would address something else." +
			"\n\nWhen sessions are nested, no single address is offered: the environment cannot say which is inner, and a confident wrong answer would send a reply to somebody else's terminal." +
			scriptsNote,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Deliberately not routed through open(): this answers about the
			// process, so a handle's configured backend and socket would be
			// the wrong thing to consult and could contradict the truth.
			here, err := olympus.Self(cmd.Context())
			if err != nil && !here.Inside {
				return err
			}

			return a.emit(here, nil, func(w io.Writer) {
				if !here.Inside {
					fmt.Fprintln(w, "not inside a session")
					return
				}
				if len(here.Nested) > 0 {
					names := make([]string, 0, len(here.Nested))
					for _, n := range here.Nested {
						names = append(names, string(n))
					}
					fmt.Fprintf(w, "inside nested sessions (%s); which is innermost cannot be determined\n",
						strings.Join(names, " and "))
					return
				}
				if here.Session == "" {
					fmt.Fprintf(w, "inside a %s session whose name could not be read: %v\n", here.Backend, err)
					return
				}
				fmt.Fprintf(w, "%s\t%s\n", here.Backend, here.Session)
			})
		},
	}
}
