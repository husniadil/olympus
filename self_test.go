package olympus_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/husniadil/olympus"
	"github.com/husniadil/olympus/backend"
)

// Self answers "which session am I running in?", asked from INSIDE one.
//
// It is a package-level function rather than a method on purpose: the answer
// does not depend on how a caller configured a handle, it depends on where the
// process actually is. A handle pointed at one socket cannot change which
// session its own process is sitting in.

// A backend that puts the session name in the environment can be answered
// without asking anything.
func TestSelfNamesTheSessionOnZmx(t *testing.T) {
	t.Setenv("ZMX_SESSION", "build")
	t.Setenv("ZMX_DIR", "/tmp/zmx-private")
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")

	got, err := olympus.Self(context.Background())
	if err != nil {
		t.Fatalf("Self: %v", err)
	}
	if !got.Inside {
		t.Fatal("Self reports it is not inside a session, but the environment says otherwise")
	}
	if got.Backend != backend.Zmx {
		t.Errorf("backend %q, want %q", got.Backend, backend.Zmx)
	}
	if got.Session != "build" {
		t.Errorf("session %q, want %q", got.Session, "build")
	}
	if got.Scope != "/tmp/zmx-private" {
		t.Errorf("scope %q, want the socket directory", got.Scope)
	}
}

// Not being inside a session is a real answer, not a failure. A program that
// asks and is told "nowhere" can act on that; one that gets an error has to
// guess whether the error meant "nowhere" or "could not tell".
func TestSelfOutsideAnySessionIsNotAnError(t *testing.T) {
	t.Setenv("ZMX_SESSION", "")
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")

	got, err := olympus.Self(context.Background())
	if err != nil {
		t.Fatalf("Self outside a session errored: %v", err)
	}
	if got.Inside {
		t.Error("Self claims to be inside a session with nothing in the environment saying so")
	}
	// Nothing is invented to fill the gap.
	if got.Backend != "" || got.Session != "" {
		t.Errorf("Self named a backend %q and session %q with no evidence for either", got.Backend, got.Session)
	}
}

// On tmux the environment carries the socket and the PANE, but not the session
// name — so answering means asking that socket, and asking the RIGHT one: the
// socket the process is actually inside, not whichever one a handle was
// configured with.
func TestSelfNamesTheSessionOnTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}

	dir, err := os.MkdirTemp(os.TempDir(), "olyself")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")

	if err := exec.Command("tmux", "-S", socket, "new-session", "-d", "-s", "inhabited").Run(); err != nil {
		t.Fatalf("creating a session to be inside of: %v", err)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", socket, "kill-server").Run() })

	pane, err := exec.Command("tmux", "-S", socket, "display-message", "-p", "-t", "=inhabited:", "#{pane_id}").Output()
	if err != nil {
		t.Fatalf("reading the pane id: %v", err)
	}

	// What tmux itself puts in a pane's environment.
	t.Setenv("ZMX_SESSION", "")
	t.Setenv("TMUX", socket+",1234,0")
	t.Setenv("TMUX_PANE", strings.TrimSpace(string(pane)))

	got, err := olympus.Self(context.Background())
	if err != nil {
		t.Fatalf("Self: %v", err)
	}
	if !got.Inside {
		t.Fatal("Self reports it is not inside a session")
	}
	if got.Backend != backend.Tmux {
		t.Errorf("backend %q, want %q", got.Backend, backend.Tmux)
	}
	if got.Session != "inhabited" {
		t.Errorf("session %q, want %q — the name has to be asked for, not read", got.Session, "inhabited")
	}
	if got.Scope != socket {
		t.Errorf("scope %q, want the socket the process is inside: %q", got.Scope, socket)
	}
}

// Sessions nest: a session of one backend can be running inside a pane of the
// other. The environment cannot say which is INNER — both sets of variables are
// present and inheritance looks identical either way.
//
// Guessing is the one thing that must not happen here. The whole use of this is
// telling another program where to reply, and a confident wrong address sends
// the reply to somebody else's terminal, silently.
func TestSelfReportsNestingRatherThanGuessing(t *testing.T) {
	t.Setenv("ZMX_SESSION", "inner-or-outer")
	t.Setenv("ZMX_DIR", "/tmp/zmx-private")
	t.Setenv("TMUX", "/tmp/some.sock,1234,0")
	t.Setenv("TMUX_PANE", "%3")

	got, err := olympus.Self(context.Background())
	if err != nil {
		t.Fatalf("Self: %v", err)
	}
	if !got.Inside {
		t.Error("Self reports being nowhere while two backends claim it")
	}
	if len(got.Nested) != 2 {
		t.Fatalf("Nested is %v, want both claimants named", got.Nested)
	}
	// No address is offered, because any single one would be a guess.
	if got.Session != "" || got.Backend != "" {
		t.Errorf("Self answered %q on %q despite being unable to tell which is inner",
			got.Session, got.Backend)
	}
}

// The real shape of the thing: a program running inside a session asks which
// session it is in, and gets an answer it could hand to another program.
//
// The answer is written to a FILE rather than read off the screen: an envelope
// on a terminal wraps at the pane's width, and reassembling it would be testing
// the terminal rather than the answer.
func TestSelfFromInsideARealSession(t *testing.T) {
	t.Parallel()
	binary := buildOlympus(t)

	for _, l := range legs(t) {
		t.Run(l.name, func(t *testing.T) {
			ol := l.open(t)
			s := session(t, ol)
			ctx := context.Background()

			out := filepath.Join(t.TempDir(), "self.json")
			if _, err := s.Exec(ctx, binary+" self --json > "+out); err != nil {
				t.Fatalf("asking from inside the session: %v", err)
			}

			raw, err := os.ReadFile(out)
			if err != nil {
				t.Fatalf("reading the answer back: %v", err)
			}
			var envelope struct {
				OK   bool `json:"ok"`
				Data struct {
					Inside  bool   `json:"inside"`
					Backend string `json:"backend"`
					Session string `json:"session"`
				} `json:"data"`
			}
			if err := json.Unmarshal(raw, &envelope); err != nil {
				t.Fatalf("the answer is not an envelope: %v\ngot: %s", err, raw)
			}

			if !envelope.Data.Inside {
				t.Fatalf("a process inside a session reported being nowhere: %s", raw)
			}
			if envelope.Data.Backend != l.name {
				t.Errorf("backend %q, want %q", envelope.Data.Backend, l.name)
			}
			// The point of the whole feature: the name it reports is the name
			// another program would have to use to reach it.
			if envelope.Data.Session != s.Name() {
				t.Errorf("session %q, want %q — the address it hands out would reach the wrong session",
					envelope.Data.Session, s.Name())
			}
		})
	}
}

// buildOlympus compiles the CLI so it can be run from inside a session, which
// is the only place this behaviour exists.
func buildOlympus(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "olympus")
	build := exec.Command("go", "build", "-o", binary, "./cmd/olympus")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the CLI: %v\n%s", err, out)
	}
	return binary
}
