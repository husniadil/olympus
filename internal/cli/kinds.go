package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/husniadil/olympus"
)

// kinds reports the agent vocabulary itself: which agents Olympus knows and by
// what executables. A bare verb rather than a group, like `agents`: it has one
// read and no write.
func (a *App) kindsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "kinds",
		Short: "List the agent vocabulary: which agents Olympus knows, and by what executables",
		Long: "List the agent vocabulary the `agents` listing reports names in: one row per canonical name, with every executable token that names it and the package directories that identify it." +
			"\n\nIt addresses nothing and resolves no backend, so it answers with no multiplexer installed at all. Both lists come from the detection tables themselves, so this verb cannot disagree with what `agents` matches on. Rows are ordered by name; executables lead with the canonical spelling. Muse's versioned launcher (muse-bin-<version>) is matched by shape rather than by a token, so it is not listed." +
			scriptsNote,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Deliberately not routed through open(): the vocabulary is
			// Olympus's own and identical on every backend, so resolving one
			// would only be a way to fail.
			kinds := olympus.Kinds()

			return a.emit(kinds, nil, func(w io.Writer) {
				table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
				fmt.Fprintln(table, "NAME\tEXECUTABLES\tPACKAGES")
				for _, k := range kinds {
					fmt.Fprintf(table, "%s\t%s\t%s\n", k.Name,
						strings.Join(k.Executables, " "), strings.Join(k.Packages, " "))
				}
				_ = table.Flush()
			})
		},
	}
}
