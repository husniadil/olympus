package cli

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/husniadil/olympus/backend"
)

// servers is the second subcommand group, for the same reason view is one: its
// operations act on servers rather than sessions, and share a noun (api §1.1).
// The bare verb lists, since listing is what a caller reaches for first, and
// `servers stop <name>` takes the noun's one write.
func (a *App) serversCmd() *cobra.Command {
	group := &cobra.Command{
		Use:   "servers",
		Short: "List the resolved backend's servers, the level above sessions",
		Long: "List the resolved backend's servers: the level above sessions, where every backend can run several, each behind its own socket." +
			"\n\nWhat a row is differs by backend and is disclosed rather than hidden: a tmux server is a socket NAME in tmux's own directory (a server started with --socket-path is not discoverable); a herdr server is one of its named sessions, from `herdr session list`; zmx has exactly one, its socket directory. meja cannot enumerate its profiles, so this is unsupported there." +
			"\n\nSelect one for any other verb with the global --server <name>. Running reports whether the server answers now; a socket with nothing behind it is a known server that is stopped, since killing a server does not remove its socket." +
			scriptsNote,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ol, err := a.open()
			if err != nil {
				return err
			}
			defer ol.Close()

			servers, err := ol.Servers(cmd.Context())
			if err != nil {
				return err
			}
			// Never null: an empty collection serializes as [].
			if servers == nil {
				servers = []backend.Server{}
			}

			return a.emit(servers, nil, func(w io.Writer) {
				if len(servers) == 0 {
					fmt.Fprintf(w, "no servers on the %s backend\n", ol.Backend())
					return
				}
				table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
				fmt.Fprintln(table, "NAME\tRUNNING\tDEFAULT\tSOCKET")
				for _, s := range servers {
					fmt.Fprintf(table, "%s\t%s\t%s\t%s\n", s.Name, yesNo(s.Running), yesNo(s.Default), s.SocketPath)
				}
				_ = table.Flush()
			})
		},
	}
	group.AddCommand(a.serversStopCmd())
	return group
}

func (a *App) serversStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop <name>",
		Short: "Stop a server by name, with every session on it",
		Long: "Stop a server by name, with every session on it." +
			"\n\nReports which happened: gone (it was not running) or killed. Both are successes. An unknown name is not found; a backend that cannot stop a server — zmx has no server process apart from its sessions — answers unsupported." +
			scriptsNote,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ol, err := a.open()
			if err != nil {
				return err
			}
			defer ol.Close()

			stopped, err := ol.StopServer(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return a.emit(stopped, nil, func(w io.Writer) {
				fmt.Fprintf(w, "%s %s\n", stopped.Outcome, stopped.Name)
			})
		},
	}
}
