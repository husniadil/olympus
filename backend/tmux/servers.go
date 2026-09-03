package tmux

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/husniadil/olympus/backend"
)

// SocketDir is the directory tmux resolves a socket NAME inside: `-L <name>`
// is `<dir>/<name>`, where the directory is `$TMUX_TMPDIR/tmux-<uid>`, falling
// back to `/tmp/tmux-<uid>`. It mirrors tmux's own resolution rather than
// asking tmux, which has no verb that reports it — and TMUX_TMPDIR travels
// into every tmux invocation unstripped (clientEnv), so the directory Olympus
// scans is the one tmux itself uses.
func SocketDir() string {
	base := os.Getenv("TMUX_TMPDIR")
	if base == "" {
		base = "/tmp"
	}
	return filepath.Join(base, fmt.Sprintf("tmux-%d", os.Getuid()))
}

// Servers lists the tmux servers reachable by socket NAME: one per socket
// file in tmux's per-user directory (§13.2).
//
// Only named sockets are discoverable. A server started with `-S <path>` put
// its socket wherever the caller chose, and there is no registry of those to
// consult — so a socket-path server is addressed by knowing its path, and is
// absent from this listing by construction.
//
// Running is measured, not inferred from the file: killing a server does not
// unlink its socket, so a file with nothing behind it is a known server that
// is not running. A tmux server with no sessions exits, so "answers" and "has
// a session" coincide here.
func (t *Tmux) Servers(ctx context.Context) ([]backend.Server, error) {
	dir := SocketDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			// tmux has never run for this user under this TMUX_TMPDIR: an
			// empty answer, nothing went wrong asking (§3.3).
			return []backend.Server{}, nil
		}
		return nil, backend.Wrapf(backend.CodeUnexpected, err, "reading tmux's socket directory %s", dir)
	}

	servers := []backend.Server{}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || info.Mode()&os.ModeSocket == 0 {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		running, err := t.serverAnswers(ctx, path)
		if err != nil {
			return nil, err
		}
		servers = append(servers, backend.Server{
			Name:       entry.Name(),
			SocketPath: path,
			Running:    running,
			Default:    entry.Name() == "default",
			Dir:        dir,
		})
	}
	sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })
	return servers, nil
}

// serverAnswers asks the server behind one socket path whether it is up. A
// "no server" answer is false, not an error; anything else is surfaced, so a
// missing tmux binary reports unavailable rather than a directory of stopped
// servers.
func (t *Tmux) serverAnswers(ctx context.Context, path string) (bool, error) {
	at := &Tmux{socketPath: path}
	_, err := at.run(ctx, nil, "list-sessions")
	if err == nil {
		return true, nil
	}
	if isNoServer(err) {
		return false, nil
	}
	return false, err
}

// StopServer kills the server behind a socket name, with every session on it
// (§13.2). A name that is not a socket in the directory is not found; a
// socket with nothing behind it is already in the desired state.
func (t *Tmux) StopServer(ctx context.Context, name string) error {
	if name == "" || strings.ContainsRune(name, '/') {
		return backend.Errorf(backend.CodeUsage, "a tmux server name is a socket NAME, not a path: %q", name)
	}
	path := filepath.Join(SocketDir(), name)
	if _, err := os.Lstat(path); err != nil {
		if os.IsNotExist(err) {
			return backend.Errorf(backend.CodeSessionNotFound,
				"no tmux server named %s; `olympus servers` lists the ones there are", name)
		}
		return backend.Wrapf(backend.CodeUnexpected, err, "reading %s", path)
	}
	at := &Tmux{socketPath: path}
	_, err := at.run(ctx, nil, "kill-server")
	if err != nil && isNoServer(err) {
		return nil
	}
	return err
}
