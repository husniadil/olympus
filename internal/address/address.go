// Package address turns where a process found itself into the options that
// address that place, for the two doors that fall back to the calling session
// when no target is named: the CLI's `status` and MCP's `session_status`.
//
// The session's server matters as much as its name. A reporter that resolved
// its name but not its server would write onto whichever server the defaults
// point at, which on any isolated setup is a different one (api §5, "With no
// target").
package address

import (
	"github.com/husniadil/olympus"
	"github.com/husniadil/olympus/backend"
)

// Here returns the options that address the backend and server here names.
// It is empty for an identity that names no backend.
func Here(here olympus.Identity) []olympus.Option {
	if here.Backend == "" {
		return nil
	}
	opts := []olympus.Option{olympus.WithBackend(string(here.Backend))}
	if here.Scope == "" {
		return opts
	}
	switch here.Backend {
	case backend.Tmux:
		// Self reports tmux's scope as the socket PATH, which is what tmux
		// itself puts in the environment.
		opts = append(opts, olympus.WithSocketPath(here.Scope))
	case backend.Zmx:
		opts = append(opts, olympus.WithZmxDir(here.Scope))
	case backend.Meja:
		// Self reports meja's scope as the socket path meja itself publishes
		// into the session.
		opts = append(opts, olympus.WithSocketPath(here.Scope))
	case backend.Herdr:
		// Passed explicitly rather than left to the environment. The herdr
		// binary does read HERDR_SOCKET_PATH itself, but the backend strips it
		// from every invocation and substitutes its own — so a reporter inside
		// a session would otherwise be sent to Olympus's default server
		// instead of the one it is in.
		opts = append(opts, olympus.WithSocketPath(here.Scope))
	}
	return opts
}
