package cli

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/husniadil/olympus"
	"github.com/husniadil/olympus/backend"
)

// agents lists the coding agents running in panes. A bare verb rather than a
// group: it has one read and no write.
func (a *App) agentsCmd() *cobra.Command {
	var last bool
	cmd := &cobra.Command{
		Use:   "agents",
		Short: "List the agents running in panes, with status where the backend knows it",
		Long: "List the coding agents running in panes on the resolved backend: which pane, which agent, what it is working on, and its status — working, idle, blocked — where it can be told." +
			"\n\nEvery backend answers. How a row was found is disclosed in detected_by: herdr detects agents itself and reports status, title and usage; on the other backends an agent is a pane whose process tree holds a known agent (claude, codex, gemini, aider, opencode, goose, amp, cursor, pi, omp, copilot, devin, agy, cline, droid, kimi, kiro, kilo, hermes, qodercli, qwen, mastracode, maki, muse, grok, letta) — its foreground command where the pane has no pid — and its status is read off a capture of the pane by the agent's manifest, one capture per row. status_source says which: native or screen. What no rule recognises is unknown rather than guessed. `olympus capabilities` reports agent_status where rows can carry a status." +
			scriptsNote,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ol, err := a.open()
			if err != nil {
				return err
			}
			defer ol.Close()

			var opts []olympus.AgentOption
			if last {
				opts = append(opts, olympus.WithLast())
			}
			agents, err := ol.Agents(cmd.Context(), opts...)
			if err != nil {
				return err
			}
			// Never null: an empty collection serializes as [].
			if agents == nil {
				agents = []backend.Agent{}
			}

			return a.emit(agents, nil, func(w io.Writer) {
				if len(agents) == 0 {
					fmt.Fprintf(w, "no agents on the %s backend\n", ol.Backend())
					return
				}
				table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
				if last {
					// The line replaces the cwd rather than joining it: both
					// are long, and what the agent said is why --last was asked
					// for.
					fmt.Fprintln(table, "PANE\tAGENT\tSTATUS\tTITLE\tLAST")
					for _, ag := range agents {
						fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n", ag.PaneID, ag.Agent, ag.Status, ag.Title, ag.Last)
					}
					_ = table.Flush()
					return
				}
				fmt.Fprintln(table, "PANE\tAGENT\tSTATUS\tTITLE\tCWD")
				for _, ag := range agents {
					fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n", ag.PaneID, ag.Agent, ag.Status, ag.Title, ag.CWD)
				}
				_ = table.Flush()
			})
		},
	}
	cmd.Flags().BoolVar(&last, "last", false,
		"Also read the one line each agent last said, or the question a blocked one is waiting on; costs one capture per row")
	return cmd
}
