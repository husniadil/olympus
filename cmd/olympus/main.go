// Command olympus drives real terminal sessions from the command line.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/husniadil/olympus/internal/cli"
)

func main() {
	// An interrupt CANCELS the context rather than killing the process, so the
	// cleanup an operation registered actually runs.
	//
	// Without this, Go's default handler exits immediately and no deferred work
	// happens — which is invisible for a verb that only reads, and costly for
	// one that installed something first. `watch` turns on a tap: measured,
	// Ctrl-C left tmux piping that pane's output into a file for as long as the
	// session lived, growing on the operator's disk, with the command that
	// started it gone and nothing to point at.
	//
	// SIGTERM is included for the same reason: a supervisor stopping Olympus
	// should leave a session no different from one Olympus never touched.
	//
	// This does not disturb attach. An attached terminal is in raw mode, so
	// Ctrl-C is delivered to the pane as a byte and never becomes a signal
	// here; a signal that does arrive is somebody deliberately stopping the
	// client, and cancelling is what should happen then.
	//
	// stop() is called before exiting so the handler is unregistered and a
	// SECOND interrupt kills the way it always did — a cleanup that hangs must
	// never leave a caller unable to quit.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Execute(ctx, os.Args[1:], os.Stdout, os.Stderr, os.Stdin)
	stop()
	os.Exit(code)
}
