package backend

import "context"

// A Server is one multiplexer server: the level above sessions. Every backend
// can run several, each behind its own socket, and every other operation in
// this package addresses exactly one of them (behavior §13.2).
//
// What a row means is backend-local, and the door discloses it rather than
// pretending the four are alike: a tmux server is a socket file in tmux's
// per-user directory, a herdr server is one of its named sessions, and a zmx
// server is the daemon's socket directory.
type Server struct {
	// Name is what --server selects the row by.
	Name string `json:"name"`
	// SocketPath is where the server is addressed: a socket file on tmux and
	// herdr, the socket directory on zmx.
	SocketPath string `json:"socket_path"`
	// Running reports whether the server answers now. A socket file with
	// nothing behind it is a known server that is not running, not an absent
	// one: killing a server does not unlink its socket.
	Running bool `json:"running"`
	// Default marks the row the backend addresses when nothing selects one:
	// tmux's "default" socket name, herdr's unnamed session, zmx's one
	// directory. It is NOT necessarily the server Olympus itself defaults to —
	// on tmux and herdr that is a socket of Olympus's own (§17.2).
	Default bool `json:"default"`
	// Dir is the directory the server keeps its state in, where the backend
	// has one to report. Empty otherwise.
	Dir string `json:"dir,omitempty"`
}

// A ServerLister enumerates a backend's servers. It is optional: a backend
// that cannot enumerate them does not implement it, and the layer above
// reports CodeUnsupported — distinct from an empty list, which is a real
// answer, and distinct from a backend that cannot be reached.
type ServerLister interface {
	Servers(ctx context.Context) ([]Server, error)
}

// A ServerStopper stops one server by name, with every session on it. It is
// optional for the same reason ServerLister is, and independently: a backend
// can enumerate servers it has no way to stop.
type ServerStopper interface {
	StopServer(ctx context.Context, name string) error
}
