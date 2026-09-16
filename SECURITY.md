# Security

## Reporting

Report suspected vulnerabilities privately through GitHub's security advisory
form for this repository, rather than opening a public issue.

Include what you did, what happened, and what you expected. A reproduction on a
private server keeps the report off your own live sessions: a private
`--socket-path` for tmux, meja or herdr, or a private `ZMX_DIR` for zmx.

## What Olympus is, security-wise

Olympus drives a terminal multiplexer as the user running it. It adds no
privilege boundary: anything it can do, the invoking user could already do by
running the multiplexer directly. Text sent to a session is executed by whatever
reads that session's input.

### The write lock is advisory

It serializes Olympus processes going through the same path. A human typing
into a raw `tmux attach`, or any other writer, is unaffected and can interleave
with it.

### Session visibility differs by backend

| Backend | Default posture |
|---|---|
| tmux | Olympus's own socket, so its sessions are not on the server your own `tmux` command talks to |
| zmx | no socket equivalent: sessions are global to the user's daemon, visible to and killable by anything else that user runs |
| meja | your default meja profile, so its sessions appear in your own `meja ls` and are saved into your own store; with `--socket-path`, a private server whose saved sessions live beside that socket |
| herdr | Olympus's own socket, with its configuration and saved layout moved beside it, invisible to your own herdr |

`olympus doctor` reports which posture is in effect.

#### Why state follows the socket

- **meja** keeps session recovery files beside its socket. Sessions on the
  default profile are persisted in the user's own store and restored later; a
  `--socket-path` keeps them out of it.
- **herdr** keeps a session's saved layout in its configuration directory, so a
  private socket alone would still overwrite the user's saved workspaces.

### Lock files are private

Lock and attach-guard files are created mode 0600, in directories created mode
0700 under the user's temporary directory, because the file names encode
session names.
