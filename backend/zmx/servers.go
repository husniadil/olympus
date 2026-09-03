package zmx

import (
	"context"
	"os"

	"github.com/husniadil/olympus/backend"
)

// Servers reports the one server a zmx backend can address: its socket
// directory (§13.2).
//
// zmx has no socket flag and no named servers. One daemon per directory is the
// whole model, and the directory is chosen by environment (§2.9) — so there is
// exactly one row, named "default", whose socket path IS the directory. It is
// running when the directory exists: sessions are the sockets inside it, and
// a directory that is not there holds none. There is no StopServer, because
// zmx has no server process to stop apart from its sessions.
func (z *Zmx) Servers(ctx context.Context) ([]backend.Server, error) {
	dir := z.validationDir()
	_, err := os.Stat(dir)
	running := err == nil
	if err != nil && !os.IsNotExist(err) {
		return nil, backend.Wrapf(backend.CodeUnexpected, err, "reading the zmx directory %s", dir)
	}
	return []backend.Server{{
		Name:       DefaultServer,
		SocketPath: dir,
		Running:    running,
		Default:    true,
		Dir:        dir,
	}}, nil
}

// DefaultServer is the only server name zmx answers to.
const DefaultServer = "default"
