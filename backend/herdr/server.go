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
func (h *Herdr) ensureServer(ctx context.Context) error {
	if err := h.validateSocketPath(); err != nil {
		return err
	}
	if h.serverAnswers(ctx) {
		return nil
	}
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
	if err := cmd.Start(); err != nil {
		return backend.Wrapf(backend.CodeBackendUnavailable, err, "starting a herdr server at %s", h.socketPath)
	}
	// Reaped rather than waited on: the server is meant to outlive this
	// process, and leaving it unwaited would leave a zombie behind for as long
	// as the caller runs.
	go func() { _ = cmd.Wait() }()

	deadline := time.Now().Add(serverStartBudget())
	for {
		if h.serverAnswers(ctx) {
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

// writeManagedConfig lays the pins down before the server that reads them boots.
//
// Configuration is read at boot, so writing it afterwards would configure the
// NEXT server and leave this one exactly as unpinned as before — the same trap
// §17.5 records for tmux's option ordering.
//
// An existing file is left alone. A caller who put their own configuration in
// this directory chose it deliberately, and overwriting it would be Olympus
// deciding it knows better about a file it does not own.
func (h *Herdr) writeManagedConfig() error {
	dir := filepath.Join(h.StateHome(), "config", "herdr")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return backend.Wrapf(backend.CodeUnexpected, err, "preparing the herdr configuration directory")
	}
	path := filepath.Join(dir, "config.toml")
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
func (h *Herdr) Stop(ctx context.Context) error {
	// Asked before it is told, because `server stop` against a socket with
	// nothing behind it fails in prose rather than through the error envelope
	// this backend classifies — so an already-stopped server would report an
	// unexpected failure for having been in the desired state already.
	if !h.serverAnswers(ctx) {
		return nil
	}
	_, err := h.run(ctx, "server", "stop")
	return err
}
