package cli

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/husniadil/olympus"
	"github.com/husniadil/olympus/backend"
)

// view is the one legitimate subcommand group: its operations act on views
// rather than sessions, and share a noun (api §1.1).
func (a *App) viewCmd() *cobra.Command {
	group := &cobra.Command{
		Use:   "view",
		Short: "Create, scroll and list independently-scrollable views onto a session",
		Long: "Create, scroll and list views: extra windows onto an existing session that scroll independently while sharing its pane." +
			"\n\nNot every backend has this concept. `olympus doctor` shows which do." + scriptsNote,
	}
	group.AddCommand(a.viewCreateCmd(), a.viewScrollCmd(), a.viewLsCmd())
	return group
}

func (a *App) viewCreateCmd() *cobra.Command {
	var noMouse bool
	var name string
	cmd := &cobra.Command{
		Use:   "create <base>",
		Short: "Create a view onto a session",
		Long: "Create an independently-scrollable view onto a session." +
			"\n\nThis is NOT a side-effect-free read: on a backend that supports views it mutates server-global state. That is self-contained while Olympus owns the server, which is the default — pointed at your own running server, the changes land there until it is killed.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ol, err := a.open()
			if err != nil {
				return err
			}
			defer ol.Close()

			var opts []olympus.ViewOption
			if noMouse {
				opts = append(opts, olympus.WithoutMouse())
			}
			if name != "" {
				opts = append(opts, olympus.WithViewName(name))
			}

			view, err := ol.CreateView(cmd.Context(), args[0], opts...)
			if err != nil {
				return err
			}
			return a.emit(view, nil, func(w io.Writer) {
				fmt.Fprintln(w, view.Name)
			})
		},
	}
	cmd.Flags().BoolVar(&noMouse, "no-mouse", false, "do not enable wheel scrolling into the view's history")
	cmd.Flags().StringVar(&name, "name", "", "view session name (default olympus-view-<base>-<nonce>)")
	return cmd
}

func (a *App) viewScrollCmd() *cobra.Command {
	var lines int
	cmd := &cobra.Command{
		Use:   "scroll <view>",
		Short: "Scroll a view back into its history",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ol, err := a.open()
			if err != nil {
				return err
			}
			defer ol.Close()

			if err := ol.ScrollView(cmd.Context(), args[0], lines); err != nil {
				return err
			}
			return a.emit(map[string]any{"view": args[0], "lines": lines}, nil, nil)
		},
	}
	cmd.Flags().IntVar(&lines, "lines", 10, "lines to scroll; negative scrolls back toward the live bottom")
	return cmd
}

func (a *App) viewLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls [base]",
		Short: "List views, optionally for one base session",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ol, err := a.open()
			if err != nil {
				return err
			}
			defer ol.Close()

			base := ""
			if len(args) == 1 {
				base = args[0]
			}
			views, err := ol.Views(cmd.Context(), base)
			if err != nil {
				return err
			}
			if views == nil {
				views = []backend.View{}
			}
			return a.emit(views, nil, func(w io.Writer) {
				if len(views) == 0 {
					fmt.Fprintln(w, "no views")
					return
				}
				table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
				fmt.Fprintln(table, "NAME\tBASE\tATTACHED")
				for _, v := range views {
					fmt.Fprintf(table, "%s\t%s\t%s\n", v.Name, v.Base, yesNo(v.Attached))
				}
				_ = table.Flush()
			})
		},
	}
}

func (a *App) serverEnvCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "server-env <key>",
		Short: "Read a key from the multiplexer server's global environment",
		Long: "Read a key from the multiplexer server's global environment." +
			"\n\nAn unset key is a real answer — present is false — and is not the same as a backend with no such concept, which is an unsupported error." + scriptsNote,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ol, err := a.open()
			if err != nil {
				return err
			}
			defer ol.Close()

			value, present, err := ol.ServerEnv(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			data := map[string]any{"key": args[0], "present": present}
			if present {
				data["value"] = value
			}
			return a.emit(data, nil, func(w io.Writer) {
				if !present {
					fmt.Fprintln(w, "not set")
					return
				}
				fmt.Fprintln(w, value)
			})
		},
	}
}
