package herdr

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/husniadil/olympus/backend"
)

// ensureServer makes a server answer on this backend's socket.
//
// Creation is the only operation that starts one, mirroring tmux, where the
// server comes up as a side effect of the first new-session and every read
// against an empty socket is simply empty (§3.3). A listing or a probe must
// never boot a server: a caller asking what exists would then create the thing
// it was asking about.
//
// A server that is ALREADY answering is left entirely alone: not restarted, not
// reconfigured, and not marked as ours. That is the existing-server mode this
// backend exists for — a box's own headless herdr, or an operator's, driven by
// a caller who pointed a socket path at it (§2.9).
func (h *Herdr) ensureServer(ctx context.Context) error {
	if err := h.validateSocketPath(); err != nil {
		return err
	}
	if h.serverAnswers(ctx) {
		// Deliberately NOT recorded as ours. Whoever started it keeps the
		// right to stop it, and its configuration is whatever it was booted
		// with — writing pins into a directory it never read would be a claim
		// rather than a change (§17.5).
		return nil
	}
	return h.startServer(ctx)
}

// startServer boots a server on this backend's socket and waits for it to
// answer.
//
// Ownership is recorded only once the server answers, and withdrawn again if
// the child exits with an error: two handles can each see an empty socket and
// each spawn, and herdr refuses the second with "already running". A server
// this handle started never exits with an error on its own, so a child that
// does was never the one answering — and the server that IS answering belongs
// to whoever won. There is nothing in herdr's API that names the answering
// server's process, so this is the closest an honest record can get; a Stop
// issued before the loser has exited still reaches the winner, and the window
// is the child's own startup.
func (h *Herdr) startServer(ctx context.Context) error {
	if err := h.writeManagedConfig(); err != nil {
		return err
	}

	cmd := exec.Command("herdr", "server")
	cmd.Env = h.env(invocationEnv())
	// Its own session, so the server outlives the Olympus process that started
	// it and does not take a terminal's SIGINT with the foreground group. A
	// server that died when the caller pressed Ctrl-C would take every session
	// on it down too.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	h.noteSpawning()
	if err := cmd.Start(); err != nil {
		return backend.Wrapf(backend.CodeBackendUnavailable, err, "starting a herdr server at %s", h.socketPath)
	}
	// Reaped rather than waited on: the server is meant to outlive this
	// process, and leaving it unwaited would leave a zombie behind for as long
	// as the caller runs.
	go func() {
		if err := cmd.Wait(); err != nil {
			h.noteServerExited()
		}
	}()

	deadline := time.Now().Add(serverStartBudget())
	for {
		if h.serverAnswers(ctx) {
			h.noteStarted()
			return nil
		}
		if time.Now().After(deadline) {
			return backend.Errorf(backend.CodeBackendUnavailable,
				"the herdr server at %s did not start answering within %s", h.socketPath, serverStartBudget())
		}
		select {
		case <-ctx.Done():
			return backend.Wrapf(backend.CodeTimeout, ctx.Err(), "waiting for a herdr server at %s", h.socketPath)
		case <-time.After(serverStartPoll):
		}
	}
}

// serverAnswers asks the question the caller actually needs answered.
//
// It runs a real request rather than reading a status line: a socket file that
// exists proves nothing — herdr leaves one behind and reclaims it on the next
// bind — and a server mid-boot accepts a connection before it can serve. The
// cheapest request that fails only when the server cannot serve is the listing
// this backend already parses.
func (h *Herdr) serverAnswers(ctx context.Context) bool {
	_, err := h.run(ctx, "pane", "list")
	return err == nil
}

// serverStartBudget is read at call time, never cached at process start, so an
// operator can raise it for a loaded host without restarting anything (§17.3).
func serverStartBudget() time.Duration {
	if v := os.Getenv(serverStartEnv); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return serverStartTimeout
}

// managedConfig is what Olympus pins on a server it starts (§17.5).
//
// Both entries turn off a background NETWORK check the server would otherwise
// run at boot — for its own updates and for remote agent-detection manifests.
// Neither has anything to do with driving a terminal, and both make a server
// Olympus started reach the network on a host where nobody asked it to.
//
// This is the one thing herdr can do that tmux cannot: its configuration
// follows its configuration DIRECTORY, which this backend has already moved
// alongside the socket, so pinning here changes a file Olympus owns rather
// than reaching into the operator's. §17.5's rule that Olympus configures only
// servers it starts still holds, and for the stronger reason that it is not
// their configuration being written.
var managedConfig = "" +
	"# Written by Olympus for the server on this socket. Do not edit.\n" +
	"[update]\n" +
	"version_check = false\n" +
	"manifest_check = false\n"

// ManagedConfig reports what Olympus pins, for the diagnostic to disclose
// (§0.6). Pinning silently would turn "why did this reach the network" into an
// unanswerable question.
func ManagedConfig() map[string]string {
	return map[string]string{
		"update.version_check":  "false",
		"update.manifest_check": "false",
	}
}

// EffectiveOptions reports what the pins are actually set to on the server
// answering right now, and whether Olympus put them there (§17.5).
//
// It answers from the FILE this backend writes rather than from the values a
// server reports, because herdr publishes no configuration through its API and
// because §17.5 requires ownership to be recorded rather than inferred: a
// server started somewhere else never had this file written for it, so its
// absence is evidence and its presence is a record.
//
// The one case it reads wrong is a socket where an Olympus server was replaced
// by somebody else's. Recorded rather than fixed: there is no server-side mark
// to carry the fact, and the alternative — comparing values — fails on the
// likelier case of an operator who set the same options themselves.
func (h *Herdr) EffectiveOptions(ctx context.Context) (map[string]string, bool, error) {
	if !h.serverAnswers(ctx) {
		return nil, false, nil
	}
	if _, err := os.Stat(h.managedConfigPath()); err != nil {
		return nil, false, nil
	}
	return ManagedConfig(), true, nil
}

func (h *Herdr) managedConfigPath() string {
	return filepath.Join(h.StateHome(), "config", "herdr", "config.toml")
}

// writeManagedConfig lays the pins down before the server that reads them boots.
//
// Configuration is read at boot, so writing it afterwards would configure the
// NEXT server and leave this one exactly as unpinned as before — the same trap
// §17.5 records for tmux's option ordering.
//
// An existing file is left alone. A caller who put their own configuration in
// this directory chose it deliberately, and overwriting it would be Olympus
// deciding it knows better about a file it does not own.
// The "herdr" path component is the release build's application directory name;
// a debug build of herdr uses "herdr-dev" instead, and would not read this file.
// The cost of that mismatch is bounded to the pins not applying — a background
// check nobody wanted still runs — rather than to anything a session depends on,
// which is why it is recorded here rather than probed for.
func (h *Herdr) writeManagedConfig() error {
	dir := filepath.Join(h.StateHome(), "config", "herdr")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return backend.Wrapf(backend.CodeUnexpected, err, "preparing the herdr configuration directory")
	}
	path := h.managedConfigPath()
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return backend.Wrapf(backend.CodeUnexpected, err, "reading %s", path)
	}
	if err := os.WriteFile(path, []byte(managedConfig), 0o600); err != nil {
		return backend.Wrapf(backend.CodeUnexpected, err, "writing %s", path)
	}
	return nil
}

// Stop ends the server on this backend's socket, with every session on it.
//
// It exists for tests, which must not leave a server behind, and for a caller
// tearing an isolated server down deliberately. It is NOT part of the Backend
// interface: killing a whole server is a different operation from killing a
// session, and every other backend leaves it to the multiplexer's own command.
//
// It refuses a server this handle did not start, and the refusal is the point:
// the whole reason this backend can be pointed at an existing server is that
// something else owns it — a box's headless herdr with a fleet of agents in it,
// or the operator's own. Stopping that would take every pane on it down,
// including every one this caller never mentioned. The fact is recorded when
// the server is started rather than inferred afterwards, because there is
// nothing observable that distinguishes a server Olympus booted from one it
// found (§2.9, and §17.5 on recording ownership rather than guessing it).
func (h *Herdr) Stop(ctx context.Context) error {
	// Asked before it is told, because `server stop` against a socket with
	// nothing behind it fails in prose rather than through the error envelope
	// this backend classifies — so an already-stopped server would report an
	// unexpected failure for having been in the desired state already.
	if !h.serverAnswers(ctx) {
		return nil
	}
	if !h.startedTheServer() {
		return backend.Errorf(backend.CodeConflict,
			"the herdr server at %s was not started by this handle, so it is not this handle's to stop: every pane on it would go with it. Close the sessions you own instead",
			h.socketPath)
	}
	if _, err := h.run(ctx, "server", "stop"); err != nil {
		return err
	}

	// Waited out rather than returned from, because the stop request is
	// acknowledged before the server has finished exiting — and a server on its
	// way out still writes its log and its saved layout back into the
	// configuration directory. A caller that removed that directory the moment
	// this returned would find it recreated underneath them, which is measured:
	// four of a test run's private directories came back, holding a session
	// snapshot written after the tree was deleted.
	deadline := time.Now().Add(serverStartBudget())
	for {
		if !h.serverAnswers(ctx) {
			return nil
		}
		if time.Now().After(deadline) {
			return backend.Errorf(backend.CodeTimeout,
				"the herdr server at %s was told to stop and is still answering after %s", h.socketPath, serverStartBudget())
		}
		select {
		case <-ctx.Done():
			return backend.Wrapf(backend.CodeTimeout, ctx.Err(), "waiting for the herdr server at %s to stop", h.socketPath)
		case <-time.After(serverStartPoll):
		}
	}
}
