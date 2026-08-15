package cli

import (
	"github.com/spf13/cobra"

	"github.com/husniadil/olympus/internal/mcp"
)

func (a *App) mcpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Serve the MCP tool surface over stdio",
		Long: "Serve the MCP tool surface over stdio, for an MCP client to launch as a subprocess." +
			"\n\nstdio only: there is no HTTP server and no daemon. Diagnostics go to stderr, which the transport leaves alone." +
			"\n\nBackend selection comes from the process environment (OLYMPUS_BACKEND, OLYMPUS_SOCKET, ZMX_DIR), because a stateless request carries no session configuration.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return mcp.Serve(cmd.Context())
		},
	}
}
