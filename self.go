package olympus

import (
	"context"
	"os"
	"strings"

	"github.com/husniadil/olympus/backend"
	"github.com/husniadil/olympus/backend/meja"
	"github.com/husniadil/olympus/backend/tmux"
)

// An Identity is what a process running inside a session can learn about
// where it is.
type Identity struct {
	// Inside is false when nothing in the environment claims this process.
	Inside bool `json:"inside"`
	// Backend is which multiplexer owns the session.
	Backend backend.Name `json:"backend,omitempty"`
	// Session is the session's name.
	Session string `json:"session,omitempty"`
	// Scope is the socket or directory the session lives on, which is what a
	// caller needs in order to address it from outside.
	Scope string `json:"scope,omitempty"`
	// Nested names every backend whose environment claims this process, and is
	// set only when more than one does. Backend, Session and Scope are then
	// left empty: any single answer would be a guess.
	Nested []backend.Name `json:"nested,omitempty"`
}

// Self reports which session the CURRENT PROCESS is running inside.
//
// It is a package-level function rather than a method because the answer does
// not depend on how a caller configured a handle: a handle pointed at one
// socket cannot change which session its own process is sitting in. Asking
// through a handle would invite exactly that confusion.
//
// The use it exists for is a program telling another program where to reach it
// — "reply into this session" — which is impossible if a process cannot name
// its own.
func Self(ctx context.Context) (Identity, error) {
	// Every backend that claims this process, gathered before any of them is
	// believed. Deciding on the first match found would make the answer depend
	// on the order they happen to be checked in, which is exactly the guess
	// this function refuses to make.
	var claimants []backend.Name
	zmxSession := os.Getenv("ZMX_SESSION")
	if zmxSession != "" {
		claimants = append(claimants, backend.Zmx)
	}
	pane := os.Getenv("TMUX_PANE")
	socket := tmuxSocket(os.Getenv("TMUX"))
	if pane != "" && socket != "" {
		claimants = append(claimants, backend.Tmux)
	}
	// meja names the session directly, so unlike tmux nothing has to be asked
	// of the server to learn it.
	mejaSession := os.Getenv("MEJA_SESSION_TARGET")
	mejaSocket := os.Getenv("MEJA_SOCKET")
	if mejaSession != "" {
		claimants = append(claimants, backend.Meja)
	}

	// Sessions nest, and the environment cannot say which is inner: every set
	// of variables is present and inheritance looks the same either way.
	// Reporting the ambiguity is the only honest answer — a confident wrong
	// address would send another program's reply to somebody else's terminal.
	if len(claimants) > 1 {
		return Identity{Inside: true, Nested: claimants}, nil
	}
	if len(claimants) == 0 {
		return Identity{}, nil
	}

	switch claimants[0] {
	case backend.Zmx:
		return Identity{
			Inside:  true,
			Backend: backend.Zmx,
			Session: zmxSession,
			Scope:   os.Getenv("ZMX_DIR"),
		}, nil
	case backend.Meja:
		// meja identifies a pane's own session by ID (MEJA_SESSION_TARGET=@1),
		// and Olympus addresses sessions by name, so the name has to be asked
		// for — of the server this process is actually inside, which is the
		// point of meja also publishing its socket.
		here := Identity{Inside: true, Backend: backend.Meja, Scope: mejaSocket}
		name, err := meja.New(meja.WithSocketPath(mejaSocket)).SessionOf(ctx, mejaSession)
		if err != nil {
			return here, err
		}
		here.Session = name
		return here, nil
	}

	// tmux puts the socket and the PANE in the environment, but not the
	// session's name — so the name has to be asked for, and asked of the
	// socket this process is actually inside rather than whichever one a
	// handle happens to be configured with. Getting that wrong would name a
	// session on the wrong server, which is worse than naming none.
	here := Identity{Inside: true, Backend: backend.Tmux, Scope: socket}
	name, err := tmux.New(tmux.WithSocketPath(socket)).SessionOf(ctx, pane)
	if err != nil {
		// Inside something, unable to name it. Both facts are returned: the
		// caller learns it is in a session even when the server could not be
		// consulted, which is more than an error alone would tell it.
		return here, err
	}
	here.Session = name
	return here, nil
}

// tmuxSocket reads the socket path out of tmux's own environment variable,
// whose shape is "<socket>,<pid>,<session index>".
func tmuxSocket(value string) string {
	socket, _, found := strings.Cut(value, ",")
	if !found {
		return ""
	}
	return socket
}
