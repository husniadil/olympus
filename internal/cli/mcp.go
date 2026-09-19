package cli

import (
	"github.com/spf13/cobra"

	"github.com/husniadil/olympus/backend"
	"github.com/husniadil/olympus/internal/mcp"
)

func (a *App) mcpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Serve the MCP tool surface over stdio",
		Long: "Serve the MCP tool surface over stdio, for an MCP client to launch as a subprocess." +
			"\n\nstdio only: there is no HTTP server and no daemon. Diagnostics go to stderr, which the transport leaves alone." +
			"\n\nConfiguration comes from the process environment (OLYMPUS_BACKEND, OLYMPUS_SOCKET, OLYMPUS_SOCKET_PATH, OLYMPUS_SERVER), because a stateless request carries no session configuration, and the global addressing flags are refused here rather than ignored. ZMX_DIR is not forwarded: the zmx binary reads it itself.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, flag := range []string{"backend", "socket", "socket-path", "server", "zmx-dir", "no-lock"} {
				if cmd.Flags().Changed(flag) {
					return backend.Errorf(backend.CodeUsage,
						"--%s is not read by the MCP server, which takes its configuration from the environment; see `olympus mcp --help`", flag)
				}
			}
			return mcp.Serve(cmd.Context())
		},
	}
}
