package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/husniadil/olympus"
	"github.com/husniadil/olympus/backend"
)

func (a *App) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the Olympus version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.emit(map[string]any{"version": olympus.Version}, nil, func(w io.Writer) {
				fmt.Fprintf(w, "olympus %s\n", olympus.Version)
			})
		},
	}
}

func (a *App) doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Report what is installed, what resolves, and what each backend can do",
		Long: "Report what is installed, what resolves and why, where sessions live, and a capability matrix." +
			"\n\nThis never fails: when nothing is installed it explains that, which is exactly when it is most needed." +
			scriptsNote,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var opts []olympus.Option
			if a.backendName != "" {
				opts = append(opts, olympus.WithBackend(a.backendName))
			}
			if a.socket != "" {
				opts = append(opts, olympus.WithSocket(a.socket))
			}
			if a.socketPath != "" {
				opts = append(opts, olympus.WithSocketPath(a.socketPath))
			}
			if a.zmxDir != "" {
				opts = append(opts, olympus.WithZmxDir(a.zmxDir))
			}

			diagnosis := olympus.Diagnose(cmd.Context(), opts...)
			a.resolved = diagnosis.Resolved.Backend

			return a.emit(diagnosis, nil, func(w io.Writer) {
				writeDiagnosis(w, diagnosis)
			})
		},
	}
}

func writeDiagnosis(w io.Writer, d olympus.Diagnosis) {
	fmt.Fprintln(w, "RESOLVED")
	if d.Resolved.Problem != "" {
		fmt.Fprintf(w, "  nothing resolves: %s\n", d.Resolved.Problem)
	} else {
		fmt.Fprintf(w, "  backend: %s (chosen by: %s)\n", d.Resolved.Backend, d.Resolved.Reason)
		if d.Resolved.Scope != "" {
			fmt.Fprintf(w, "  socket or directory: %s\n", d.Resolved.Scope)
		}
	}

	fmt.Fprintln(w, "\nBACKENDS")
	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "  NAME\tINSTALLED\tVERSION\tFLOOR\tBELOW FLOOR")
	for _, b := range d.Backends {
		version := b.Version
		if version == "" {
			version = "-"
		}
		fmt.Fprintf(table, "  %s\t%s\t%s\t%s\t%s\n",
			b.Name, yesNo(b.Installed), version, b.Floor, yesNo(b.BelowFloor))
	}
	_ = table.Flush()

	// A backend on PATH that cannot be run is the one state the table above
	// spells only by omission — installed yes, version "-" — and a reader has no
	// way to tell that from a probe that timed out. Say it in words.
	for _, b := range d.Backends {
		if b.Problem != "" {
			fmt.Fprintf(w, "  ! %s is %s\n", b.Name, b.Problem)
		}
	}

	// The matrix is not decoration. The backends differ substantially, the
	// default is the less capable of the two, and a user needs one place that
	// says so rather than discovering it one unsupported error at a time
	// (behavior §0.6).
	fmt.Fprintln(w, "\nCAPABILITIES")
	matrix := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(matrix, "  CAPABILITY\t"+capabilityHeader(d))
	for _, row := range capabilityRows() {
		line := "  " + row.label
		for _, b := range d.Backends {
			if !b.Installed {
				line += "\t-"
				continue
			}
			line += "\t" + yesNo(row.get(b.Capabilities))
		}
		fmt.Fprintln(matrix, line)
	}
	_ = matrix.Flush()

	fmt.Fprintln(w, "\nWHERE SESSIONS LIVE")
	for _, b := range d.Backends {
		if b.Installed {
			fmt.Fprintf(w, "  %s: %s\n", b.Name, b.Isolation)
		}
	}

	// Named as an override, not as a feature. The reader who needs this line is
	// the one wondering why a setting of theirs has no effect (§17.5).
	for _, b := range d.Backends {
		if !b.Installed || len(b.Managed) == 0 {
			continue
		}
		keys := make([]string, 0, len(b.Managed))
		for key := range b.Managed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		fmt.Fprintf(w, "\nWHAT OLYMPUS OVERRIDES IN YOUR %s CONFIG\n", strings.ToUpper(string(b.Name)))
		for _, key := range keys {
			value := b.Managed[key]
			if value == "" {
				value = `"" (tmux's own default: your login shell)`
			}
			fmt.Fprintf(w, "  %s = %s\n", key, value)
		}
		fmt.Fprintln(w, "  everything else in your config is left alone, including keybindings and theme")
		fmt.Fprintln(w, "  applied to only servers it starts, never to one that was already running")
	}

	// The distinction only exists once a server is answering, and it is the
	// difference between "these values are ours" and "these values are whatever
	// this server was given" (§17.5).
	if len(d.Resolved.Effective) > 0 {
		keys := make([]string, 0, len(d.Resolved.Effective))
		for key := range d.Resolved.Effective {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		fmt.Fprintln(w, "\nTHE SERVER ANSWERING NOW")
		if d.Resolved.Pinned {
			fmt.Fprintln(w, "  started and configured by Olympus")
		} else {
			fmt.Fprintln(w, "  already running before Olympus reached it, so it is left as it is")
			fmt.Fprintln(w, "  a run's exit code depends on the shell below being POSIX-compatible")
		}
		for _, key := range keys {
			value := d.Resolved.Effective[key]
			if value == "" {
				value = `"" (this server's own default shell)`
			}
			fmt.Fprintf(w, "  %s = %s\n", key, value)
		}
	}

	if len(d.InstallHints) > 0 {
		fmt.Fprintln(w, "\nNOT INSTALLED")
		for _, hint := range d.InstallHints {
			fmt.Fprintf(w, "  %s\n", hint)
		}
	}
}

func capabilityHeader(d olympus.Diagnosis) string {
	header := ""
	for i, b := range d.Backends {
		if i > 0 {
			header += "\t"
		}
		header += string(b.Name)
	}
	return header
}

type capabilityRow struct {
	label string
	get   func(backend.Capabilities) bool
}

func capabilityRows() []capabilityRow {
	return []capabilityRow{
		{"native scrollback", func(c backend.Capabilities) bool { return c.NativeScrollback }},
		{"views", func(c backend.Capabilities) bool { return c.Views }},
		{"remain-on-exit", func(c backend.Capabilities) bool { return c.RemainOnExit }},
		{"server environment", func(c backend.Capabilities) bool { return c.ServerEnv }},
		{"control keys", func(c backend.Capabilities) bool { return c.ControlKeys }},
		{"spawn sizing", func(c backend.Capabilities) bool { return c.SpawnSizing }},
		{"session status", func(c backend.Capabilities) bool { return c.SessionStatus }},
		{"alt-screen tracking", func(c backend.Capabilities) bool { return c.TracksAltScreen }},
		{"servers", func(c backend.Capabilities) bool { return c.Servers }},
		{"session client", func(c backend.Capabilities) bool { return c.SessionClient }},
		{"bare attach", func(c backend.Capabilities) bool { return c.Bare }},
		{"focus", func(c backend.Capabilities) bool { return c.Focus }},
	}
}
