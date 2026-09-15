package cli

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/husniadil/olympus"
)

func (a *App) clientsCmd() *cobra.Command {
	var tag string
	cmd := &cobra.Command{
		Use:   "clients",
		Short: "List the clients attached to the server, and what each shows",
		Long: "List the clients attached to the resolved server: each client's id and tag, and the session, window and pane it shows, with whether that window is zoomed and whether the client has applied its view." +
			"\n\nIt answers which workspace, tab and pane a person's client is on right now, including a pane they focused with the client's own keys. On herdr a session is a workspace and a window is a tab, and every id is a target other verbs take." +
			"\n\n--tag narrows it to the client carrying that tag, the one given to `attach --bare --client-tag`; no client carrying it is not found (exit 3)." +
			"\n\nOnly a herdr server that advertises client_view_focus reports where each client is; every other backend and server answers UNSUPPORTED (exit 7). zoomed and view_applied are omitted where the server does not report them. No server running is an empty list." +
			scriptsNote,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ol, err := a.open()
			if err != nil {
				return err
			}
			defer ol.Close()

			var opts []olympus.ClientsOption
			if tag != "" {
				opts = append(opts, olympus.WithClientTag(tag))
			}
			clients, err := ol.Clients(cmd.Context(), opts...)
			if err != nil {
				return err
			}
			return a.emit(clients, nil, func(w io.Writer) {
				if len(clients) == 0 {
					fmt.Fprintf(w, "no clients on the %s server\n", ol.Backend())
					return
				}
				table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
				fmt.Fprintln(table, "ID\tTAG\tSESSION\tWINDOW\tPANE\tZOOMED\tAPPLIED")
				for _, c := range clients {
					fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
						c.ID, orDash(c.Tag), orDash(c.SessionID), orDash(c.WindowID), orDash(c.PaneID), reported(c.Zoomed), reported(c.ViewApplied))
				}
				_ = table.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&tag, "tag", "", "only the client carrying this tag; not found when none does")
	return cmd
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// reported spells a flag the server may not have given: "-" where it did not.
func reported(v *bool) string {
	if v == nil {
		return "-"
	}
	return yesNo(*v)
}
