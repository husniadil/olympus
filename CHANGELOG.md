# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project uses
[semantic versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- The mechanical layer: `backend` types, the error vocabulary with its exit
  mapping, target resolution, and the `Backend` interface.
- `backend/backendtest`, the conformance suite, exported so a third-party
  backend can prove itself against the same one the shipped backends run.
- The tmux and zmx backends, both green on the full suite.
- Shared engines: the per-session write lock, verified delivery, idempotent
  ensure, the sentinel run protocol, graceful kill, and exit-marker inspection.
- The ergonomic Go surface: `Open`, `Session`, options, typed errors.
- The CLI, with the structured envelope and the documented exit codes.
- The MCP door, dual-era on the official Go SDK, targeting revision
  `2026-07-28`.
- Attach: PTY ownership, raw mode, two-layer terminal restore, the resize
  protocol, and the attach guard.
- Multi-target capture: several sessions read in one call.
- `panes`, `new` and `capabilities` as verbs and tools in their own right.
- Throwaway runs: `run` with no target creates a session for the run and kills
  it afterwards.
- `paste --enter`, `send --no-enter`, and tunables on every operation that has a
  budget or a window.
- `watch`, following a session's output live, built on each backend's own
  streaming primitive rather than on polling a capture.
- An open key vocabulary: the whole control range (`c-a`…`c-z`) and `f1`…`f12`,
  derived rather than enumerated.
- `control_keys` capability, reporting whether a backend actually delivers
  control keys — which decides whether a full-screen program can be driven.
- `--socket-path`, addressing the tmux socket by path rather than by name, so it
  can live in a directory the caller controls.
- Self-identification: `self` as a verb, a tool and `olympus.Self`, so a process
  inside a session can name it — the address it hands another program to be
  replied to. Being outside one is an answer, not a failure, and nested sessions
  report the ambiguity rather than guessing which is inner.
- Managed tmux options: `default-command` and `history-limit` are pinned on
  servers Olympus **starts**, ahead of the pane that reads them, and disclosed by
  `doctor` in both output modes. A server that was already running is left
  exactly as it is — `set-option -g` reaches every session on it — and `doctor`
  reports which case applies and what is actually in effect. A private socket is not a private
  configuration, and an operator's `default-command` was measurably corrupting
  the run protocol's exit marker.
- Views: the read-only posture and wheel bindings, with the base probed before
  anything is created. OSC 8 hyperlinks are declared per attach client with
  tmux's `-T`, so no server option is rewritten.

### Fixed

Nothing here has shipped yet, so these are corrections made before a first
release rather than changes to released behaviour. They are listed because each
changed what the code does, not just how it is written.

- **`wait` matched patterns against the whole screen** instead of per line, so
  every anchored pattern (`^42$`, `^>>>\s*$`) silently never matched while plain
  substrings kept working. Patterns are line-oriented now, and each line is
  tried both as captured and with trailing padding trimmed.
- **A pane on the alternate screen came back empty.** A full-screen program
  could be started and never observed. Such a pane is captured now; only a
  history request against it is refused, since the alternate screen genuinely
  has no scrollback.
- **Views reported the base and the view swapped**, because the base was taken
  to be the first row listed and tmux lists by name.
- **The zmx supersession sweep was a no-op**, running with the very environment
  variable it needs in order to aim.
- **tmux `Create` reported an infrastructure failure** when a session's command
  finished before creation returned, which is ordinary for a short command.
