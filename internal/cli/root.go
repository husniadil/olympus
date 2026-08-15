package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/husniadil/olympus"
	"github.com/husniadil/olympus/backend"
)

// An App holds the streams and options one invocation runs with.
type App struct {
	Out io.Writer
	Err io.Writer
	In  io.Reader

	backendName string
	socket      string
	socketPath  string
	zmxDir      string
	json        bool
	noLock      bool
	quiet       bool

	// resolved is filled in lazily, so even a failure can disclose which
	// backend answered.
	resolved backend.Name
}

// Execute runs the CLI and returns the process exit code.
//
// The exit code is derived in ONE place, from the error's own classification,
// so no command can invent its own mapping. The two operations that
// deliberately deviate — run, which reports the command's status, and attach,
// which reports its client's — do so locally and say so in their help
// (behavior §12.1).
func Execute(ctx context.Context, args []string, out, errOut io.Writer, in io.Reader) int {
	app := &App{Out: out, Err: errOut, In: in}
	root := app.root(ctx)

	// The output mode is read from the raw arguments, before anything can
	// fail. Without this, a failure in PARSING would be reported in human form
	// to a caller who asked for the envelope — and that caller has no way to
	// know, since the flag they passed is exactly what could not be parsed
	// (behavior §12.2).
	//
	// This runs after the command tree is built on purpose: registering a flag
	// writes its default through the bound pointer, so presetting first would
	// be silently undone.
	app.presetOutputMode(args)

	root.SetArgs(args)
	root.SetOut(out)
	root.SetErr(errOut)

	err := root.ExecuteContext(ctx)
	if err == nil {
		return 0
	}

	var exit exitCode
	if asExitCode(err, &exit) {
		return int(exit)
	}
	err = classify(err)
	app.report(err)
	return olympus.ExitCode(backend.CodeOf(err))
}

// presetOutputMode reads the flags that decide WHERE output goes, before
// anything can fail.
func (a *App) presetOutputMode(args []string) {
	for _, arg := range args {
		switch arg {
		case "--json", "--json=true":
			a.json = true
		case "-q", "--quiet", "--quiet=true":
			a.quiet = true
		}
	}
}

// classify puts a code on an error that does not carry one.
//
// Everything below this layer returns a classified error, so an unclassified
// one came from the argument parser: an unknown subcommand, wrong positional
// arity, a rejected flag. Those are all input the caller could have fixed by
// changing one argument, which §12 makes the definition of a usage error —
// and leaving them UNEXPECTED would tell a machine consumer that retrying will
// not help, when correcting the command line is exactly what will.
func classify(err error) error {
	var classified *backend.Error
	if errors.As(err, &classified) {
		return err
	}
	return backend.Wrapf(backend.CodeUsage, err, "invalid arguments")
}

// report emits a failure on whichever channel the caller asked for.
func (a *App) report(err error) {
	if a.json {
		_ = writeJSON(a.Out, failureEnvelope(a.resolved, err))
		return
	}
	fmt.Fprintf(a.Err, "olympus: %s\n", err.Error())
}

// An exitCode carries a deliberate deviation from the shared mapping.
type exitCode int

type exitCodeError struct {
	code exitCode
}

func (e *exitCodeError) Error() string { return "" }

func asExitCode(err error, out *exitCode) bool {
	if e, ok := err.(*exitCodeError); ok {
		*out = e.code
		return true
	}
	return false
}

// Root builds the command tree, for generating documentation and completions
// from the one definition rather than maintaining a second copy of it.
func Root() *cobra.Command {
	return (&App{Out: os.Stdout, Err: os.Stderr, In: os.Stdin}).root(context.Background())
}

func (a *App) root(ctx context.Context) *cobra.Command {
	root := &cobra.Command{
		Use:   "olympus",
		Short: "Drive real terminal sessions from the command line",
		Long: strings.TrimSpace(`
Olympus creates, drives, observes and tears down real terminal sessions on top
of a multiplexer it does not embed.

Human-readable output is for reading and is NOT stable: it may change in any
release. Scripts should use --json, whose shape is semver-bound.`),
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Every error reaches the envelope, including this framework's own flag
	// validation. Without this hook the framework prints its own message and
	// exits BEFORE any application code runs, so whether a usage-class failure
	// is machine-readable would depend on which layer caught it — an
	// implementation detail from the caller's side (behavior §12.2).
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return backend.Wrapf(backend.CodeUsage, err, "invalid arguments")
	})

	flags := root.PersistentFlags()
	flags.StringVar(&a.backendName, "backend", "", "backend to use (zmx or tmux); overrides "+olympus.BackendEnv)
	flags.StringVar(&a.socket, "socket", "", "tmux socket NAME, resolved inside tmux's own directory (tmux backend only)")
	flags.StringVar(&a.socketPath, "socket-path", "", "tmux socket PATH, used verbatim; puts the socket where you choose (tmux backend only)")
	flags.StringVar(&a.zmxDir, "zmx-dir", "", "zmx socket directory (zmx backend only)")
	flags.BoolVar(&a.json, "json", false, "emit the structured envelope on stdout")
	flags.BoolVar(&a.noLock, "no-lock", false, "skip the per-session write lock (for callers that serialize their own writes)")
	flags.BoolVarP(&a.quiet, "quiet", "q", false, "suppress non-essential human output")

	root.AddCommand(
		a.startCmd(),
		a.newCmd(),
		a.lsCmd(),
		a.stopCmd(),
		a.infoCmd(),
		a.selfCmd(),
		a.statusCmd(),
		a.panesCmd(),
		a.typeCmd(),
		a.keyCmd(),
		a.pasteCmd(),
		a.sendCmd(),
		a.screenCmd(),
		a.waitCmd(),
		a.watchCmd(),
		a.runCmd(),
		a.pollCmd(),
		a.exitStatusCmd(),
		a.attachCmd(),
		a.viewCmd(),
		a.serverEnvCmd(),
		a.mcpCmd(),
		a.capabilitiesCmd(),
		a.doctorCmd(),
		a.versionCmd(),
	)
	return root
}

// open builds a handle with the global options applied.
func (a *App) open() (*olympus.Olympus, error) {
	return a.openAt(olympus.Identity{})
}

// openAt opens a handle, letting a discovered location fill in what no flag
// supplied.
//
// Flags still win: an operator who names a backend or a socket has said where
// they mean, and a process discovering its own surroundings must not overrule
// that. The discovery only fills the gap that would otherwise be filled by a
// default pointing somewhere else entirely.
func (a *App) openAt(here olympus.Identity) (*olympus.Olympus, error) {
	opts := []olympus.Option{}
	if a.backendName == "" && here.Backend != "" {
		opts = append(opts, olympus.WithBackend(string(here.Backend)))
		if here.Scope != "" {
			switch here.Backend {
			case backend.Tmux:
				// Self reports tmux's scope as the socket PATH, which is what
				// tmux itself puts in the environment.
				opts = append(opts, olympus.WithSocketPath(here.Scope))
			case backend.Zmx:
				opts = append(opts, olympus.WithZmxDir(here.Scope))
			}
		}
	}
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
	if a.noLock {
		opts = append(opts, olympus.WithoutLock())
	}

	ol, err := olympus.Open(opts...)
	if err != nil {
		return nil, err
	}
	a.resolved = ol.Backend()
	return ol, nil
}

// emit writes a successful result in whichever mode was asked for.
//
// human is a function rather than a string so the formatting work is skipped
// entirely under --json, and so a command cannot accidentally write human
// output to the data channel.
func (a *App) emit(data any, warnings []olympus.Warning, human func(io.Writer)) error {
	if a.json {
		return writeJSON(a.Out, successEnvelope(a.resolved, data, warnings))
	}
	if !a.quiet {
		// Narration on stderr, always: stdout is the data channel, and a
		// caller piping it must never have to filter warnings out.
		writeWarnings(a.Err, warnings)
	}
	if human != nil {
		human(a.Out)
	}
	return nil
}

// Colour is deliberately not used yet. api §2.2 permits it on a TTY and forbids
// it otherwise; emitting none satisfies the half that matters for correctness,
// and a helper that is never called would be a promise the code does not keep.
