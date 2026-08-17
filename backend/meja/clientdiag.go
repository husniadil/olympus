package meja

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/husniadil/olympus/backend"
)

// outputKept bounds how much of a client's output is carried into an error.
//
// A client draws a whole screen, and an error is read by a human. The tail is
// kept rather than the head: what a client did LAST is what explains where it
// stopped.
const outputKept = 240

// clientEvidence is what was observable about the transient client at the
// moment its operation gave up.
//
// Every field is optional and an empty one means "not gathered", never "the
// thing did not happen" — see giveUp.
type clientEvidence struct {
	// Exited reports whether the client process had already finished.
	Exited bool
	// Wait is what waiting on the process returned, when it had exited.
	Wait string
	// Output is what the client wrote to its terminal.
	Output string
}

// meja offers NO way to ask which clients a server has. There is no verb that
// lists them, and `#{session_attached}` comes back unsubstituted from
// list-sessions — both measured here, by asking and reading the answer. So the
// server's side of this failure is not observable at all, and the evidence
// below is entirely client-side. That is a limit worth stating: a future
// reader will otherwise assume nobody thought to ask the server.
//
// giveUp wraps meja's refusal with what was observable when it was given up on.
//
// meja's own message — "command requires an attached client" — is kept verbatim
// and first: it is accurate, it is what an operator will search for, and the
// error vocabulary is semver-bound. What is added is the part that was missing
// when every meja case in this repository failed at once and then would not
// reproduce: whether the client died or hung, and what it printed on the way.
// Those separate a crashed client from a hung one — opposite faults that the
// symptom alone cannot tell apart, and the pair the burst sat between.
func giveUp(err error, target string, ev clientEvidence) error {
	var b strings.Builder
	fmt.Fprintf(&b, "driving %s", target)

	// The state comes first among the additions because it is the one that
	// chooses which fault this is.
	switch {
	case ev.Exited && ev.Wait != "":
		fmt.Fprintf(&b, "; its transient client had exited (%s)", ev.Wait)
	case ev.Exited:
		b.WriteString("; its transient client had exited")
	default:
		b.WriteString("; its transient client was still running")
	}

	// Quoted, never pasted: this is terminal output, and an error carrying raw
	// escape bytes redraws the screen of whoever reads it.
	if ev.Output != "" {
		fmt.Fprintf(&b, "; it last wrote %s", strconv.Quote(tail(ev.Output, outputKept)))
	}
	return backend.Wrapf(backend.CodeUnexpected, err, "%s", b.String())
}

// tail keeps the last n bytes, marking that it truncated.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
