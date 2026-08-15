# Security

## Reporting

Please report suspected vulnerabilities privately through GitHub's security
advisory form for this repository, rather than opening a public issue.

Include what you did, what happened, and what you expected. A reproduction
against a private tmux socket or a private `ZMX_DIR` is ideal, since that keeps
the report off your own live sessions.

## What Olympus is, security-wise

Olympus drives a terminal multiplexer as the user running it. It adds no
privilege boundary: anything it can do, the invoking user could already do by
running the multiplexer directly. Text sent to a session is executed by whatever
is reading that session's input.

Two things are worth knowing when reasoning about isolation:

- **The write lock is advisory.** It serializes other Olympus processes going
  through the same path. A human typing into a raw `tmux attach`, or any other
  writer, is unaffected and can still interleave with it.
- **Session visibility differs by backend.** On tmux, Olympus uses its own
  socket by default. On zmx there is no socket equivalent: sessions are global
  to the user's daemon, so anything Olympus creates is visible to — and killable
  by — anything else that user runs. `olympus doctor` reports which posture is
  in effect.

Lock and attach-guard files are created mode 0700/0600 under the user's
temporary directory, because their names encode session names.
