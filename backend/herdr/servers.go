package herdr

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/husniadil/olympus/backend"
)

// WithServerSocket selects a herdr server by NAME, addressed through the
// socket of that named session — found by name through Servers or
// LookupServer.
//
// It differs from WithSocketPath in one way that matters: the configuration
// and state directories are NOT redirected. A named session's socket lives
// inside the operator's configuration tree (`~/.config/herdr/sessions/<name>/
// herdr.sock`), so the derivation StateHome performs on a socket path would
// put Olympus's own state directories inside that tree. Every invocation on a
// backend built this way carries the socket alone, the way an attach onto a
// server Olympus did not start already does — and the server is never started
// by Olympus, only driven (§2.9.1, §13.2).
//
// The name is kept as well as the socket because one client needs it: herdr's
// session client attaches a named session BY NAME (`herdr session attach
// <name>`), and that is the client a session-client attach on this handle
// spawns (§8.10).
func WithServerSocket(name, path string) Option {
	return func(h *Herdr) { h.socketPath = path; h.socketOnly = true; h.serverName = name }
}

// A serverRow is the part of `herdr session list --json` Olympus reads.
type serverRow struct {
	Name       string `json:"name"`
	Running    bool   `json:"running"`
	Default    bool   `json:"default"`
	SessionDir string `json:"session_dir"`
	SocketPath string `json:"socket_path"`
}

// Servers lists herdr's named sessions, which are its servers: each one is a
// separate server process behind its own socket (§13.2).
//
// The listing runs against the OPERATOR'S configuration directory, not this
// backend's redirected one: named sessions live in the real configuration
// tree, and a redirected listing would answer with the one unnamed session
// Olympus's own state home holds. So this is the one herdr invocation that
// carries neither the socket override nor the state redirect.
func (h *Herdr) Servers(ctx context.Context) ([]backend.Server, error) {
	out, err := runAmbient(ctx, "session", "list", "--json")
	if err != nil {
		return nil, err
	}
	return parseServers(out)
}

// parseServers reads the rows out of `herdr session list --json`.
func parseServers(out string) ([]backend.Server, error) {
	var listed struct {
		Sessions []serverRow `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		return nil, backend.Wrapf(backend.CodeUnexpected, err, "reading the herdr session listing")
	}
	servers := make([]backend.Server, 0, len(listed.Sessions))
	for _, row := range listed.Sessions {
		servers = append(servers, backend.Server{
			Name:       row.Name,
			SocketPath: row.SocketPath,
			Running:    row.Running,
			Default:    row.Default,
			Dir:        row.SessionDir,
		})
	}
	return servers, nil
}

// LookupServer finds a named session's socket, for selecting a server by name.
//
// Not found is CodeSessionNotFound: the name is one the listing could have
// answered, and the vocabulary has no closer code for "this thing does not
// exist" (§12). An over-long socket — a deep configuration directory or a long
// name — is refused here by name, before any invocation would fail on the
// derived client socket with an error naming neither (validateSocketPath).
func LookupServer(ctx context.Context, name string) (backend.Server, error) {
	out, err := runAmbient(ctx, "session", "list", "--json")
	if err != nil {
		return backend.Server{}, err
	}
	servers, err := parseServers(out)
	if err != nil {
		return backend.Server{}, err
	}
	for _, s := range servers {
		if s.Name != name {
			continue
		}
		if err := (&Herdr{socketPath: s.SocketPath}).validateSocketPath(); err != nil {
			return backend.Server{}, err
		}
		return s, nil
	}
	return backend.Server{}, backend.Errorf(backend.CodeSessionNotFound,
		"no herdr server named %s; `olympus servers` lists the ones there are", name)
}

// StopServer stops a named session's server, with every pane on it.
//
// This is herdr's own `session stop`, addressed by NAME in the operator's
// configuration tree — deliberately not Stop, which is scoped to the server
// this handle started and refuses every other (§2.9.1). A caller naming a
// server here has named the thing they mean to take down.
func (h *Herdr) StopServer(ctx context.Context, name string) error {
	if strings.TrimSpace(name) == "" {
		return backend.Errorf(backend.CodeUsage, "a server needs a name")
	}
	_, err := runAmbient(ctx, "session", "stop", name, "--json")
	return err
}

// runAmbient invokes herdr against the operator's own configuration: the
// hygiene strips of invocationEnv apply, but neither the socket override nor
// the state redirect. It is what the named-session verbs need, since those
// resolve under the real configuration directory.
func runAmbient(ctx context.Context, args ...string) (string, error) {
	h := &Herdr{socketOnly: true}
	cmd := h.command(ctx, args...)
	cmd.Env = invocationEnv()
	return runCommand(cmd, args)
}
