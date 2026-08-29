# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project uses
[semantic versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- A fourth backend, **herdr**. It resolves last, after meja, so no host changes
  which backend answers, and `--backend herdr` selects it explicitly. An Olympus
  session is one named herdr pane; `--socket-path` addresses the server, and the
  configuration and state directories move with it because herdr keeps a saved
  layout in its configuration directory rather than beside its socket.
- `spawn_command` in `capabilities`: whether a session can be started ON a
  command. It is true on tmux, zmx and meja and false on herdr, whose panes run
  the shell its own configuration names — a command there is refused rather than
  typed into a shell (behavior §2.3.1).

### Changed

- On herdr every pane is a session, named by its pane label where it has one and
  by its pane id (`w25:p8`) where it has not. Only panes carrying a label were
  listed before, which hid every pane another tool created — the case the
  backend exists for (behavior §3.4).
- Pointing `--socket-path` at a herdr server that is already running is a
  supported mode: Olympus drives it and never starts, reconfigures or stops it,
  and refuses a stop it did not start with `CONFLICT` (behavior §2.9.1).
- Every backend now strips herdr's session, socket and pane-identity variables
  on its spawn paths, alongside tmux's and zmx's (behavior §1.1). Without it a
  session created on one backend from inside a herdr pane inherited that pane's
  identity and reported it as its own address.
- A backend that honours a history or poll-window request up to a ceiling
  discloses the clamp only when the request exceeded it. A backend that ignores
  the request entirely still discloses on every call (behavior §0.8).

## [0.1.2]

### Changed

- `CreateSpec.Env` is gone; it was never set or read. Each backend builds its
  own spawn environment.
- `Session.TypeAndSubmit` is the one-call form of type-then-Enter for doors.

### Fixed

- meja sessions are spawned with the sanitized environment §1.1 requires:
  `TERM` forced, `LANG` defaulted, `TMUX`/`TMUX_PANE`/`ZMX_SESSION`/
  `ZMX_SESSION_PREFIX` stripped. They inherited the caller's environment.
- Every composed inject-then-submit path retries the Enter once (§4.4):
  verified sends, runs, and `type --submit` / `type_text` with `submit`. Only
  paste-and-submit did before.
- A single-target `screen` (and `wait_for`, `exit_status`) drops a history
  request on an alternate-screen target with a warning, as the multi-target
  read already did (§5.3).
- `scroll_view` resolves its target like every other target-taking verb, so a
  pane id works there too (§10).

## [0.1.1]

### Added

- `skills/olympus/SKILL.md`: a skill an agent harness can load, covering when
  Olympus is the right tool over a plain shell, which verb fits which situation,
  the traps that only appear when verbs are combined, and the MCP tool names as
  the same vocabulary under a different spelling.

### Changed

- meja's two client failures are told apart. A refusal (`command requires an
  attached client`) means the command did not run and is retried; a loss
  mid-flight (`target client disconnected`) means it may have run and is
  reported rather than retried, since resending could deliver an injection
  twice. Both now carry what was observable about the client — whether it was
  alive, and what it last wrote — instead of only meja's own words.
- Backend versions in CI and in the release gate are pinned rather than
  installed as `@latest`.

### Fixed

- Throwaway runs ignored `--timeout` and `timeout_seconds`: `RunOnce` took only
  session options, so both doors built the run timeout and then dropped it on
  the throwaway branch, and every throwaway run used the 60s default.
- A binary installed with `go install ...@v0.1.1` reported the development
  placeholder version. When the release did not stamp `Version`, it is now read
  from the module version the Go toolchain recorded at build time.

- Documented that meja 0.0.26 no longer routes ordinary input through an
  attached client (behavior §2.10); the injection path already attempted the
  operation first, so it works unchanged on both, and the supported floor
  stays 0.0.25.

## [0.1.0]

The first release. Everything below is the initial surface rather than a change
against a previous version, and the fixes are recorded because the specification
and the tests that found them are part of what ships.

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
- A third backend, **meja**, green on the full conformance suite and reachable
  from all three doors. It is last in the fallback order, so it answers only
  when neither zmx nor tmux is installed. Its injections run under a transient
  headless client — meja refuses input on a session with none — sized to the
  session's current geometry, because meja shrinks a session to its smallest
  client and never restores it. Views, server environment, session status and
  remain-on-exit are capability-gated rather than faked.
- Session status: an opaque label a process inside a session leaves for whoever
  drives it from outside, as `status`, the `session_status` tool, and
  `SetStatus`/`Status`/`WaitForStatus`. It answers what a capture cannot — a
  program at a prompt and one mid-work render identically. Olympus defines no
  vocabulary of states; backends that cannot carry one refuse both directions.
  With no target it uses the session the caller is in, resolving that session's
  server along with its name.
- Views: the read-only posture and wheel bindings, with the base probed before
  anything is created. OSC 8 hyperlinks are declared per attach client with
  tmux's `-T`, so no server option is rewritten.

- A second gate. `make test` is the loop — seconds, and nothing that drives a
  terminal — while `make test-full` is what CI runs and what a commit needs. The
  split is not a reduction: `test-full` still runs everything.
- `docs/adding-a-backend.md`, the contributor route for a fourth backend: spike
  first, the isolation rules, the conformance suite as the definition of
  correct, and the lessons that each cost a day here.
- `doctor` reports `problem` for a backend that is on PATH but cannot be run —
  what a version-manager shim left by an uninstalled tool looks like — instead
  of leaving a reader to infer it from a missing version.
- `poll_run` accepts `command_id` as well as `id`, the name `start_run` hands
  back.
- A named compile error on unsupported platforms. Olympus is macOS and Linux
  only, and a Windows build said so with six `undefined: syscall.*` errors.

### Changed

- **`key` is now `press`**, and the MCP tool `send_keys` is now `press_keys`.
  One operation had three names, and api §1.1 decides which wins: verbs are named
  for intent, not mechanism.
- **The MCP tool `capture` is now `screen`**, and `Olympus.Capture` is now
  `Olympus.Screens`, matching the `Screens` type it has always returned. What
  looked like one inconsistently-named operation was two: one target and many.
- **`attach --json` and `watch --json` are now `USAGE` errors.** Both write the
  terminal rather than a description of it — attach hands stdout to the
  multiplexer's client, watch streams raw output — so no envelope can hold them,
  and api §2.3's promise is kept by refusing rather than by pretending.

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
- **`doctor` could hang forever.** Its version probes inherited the caller's
  context, which from the CLI is `Background`, so one hanging backend binary hung
  the command whose entire job is explaining a broken environment. Probes are
  bounded now — and bounding them was not enough on its own: cancelling a context
  kills the child, while a grandchild keeps the output pipe open and the read
  keeps waiting, measured at 30s against 3s until `WaitDelay` was set on every
  backend subprocess.
- **`doctor`, `version` and `self` failed over MCP when no multiplexer was
  installed**, because every tool opened a backend handle before dispatch. Those
  three answer about Olympus and this process, and are most needed exactly then.
- **`info` dropped the `panes` key** whenever the list came back empty, including
  for a session that is present — a listing racing a kill. Iterating it without a
  guard is the normal way to consume it.
- **A verified send failed against the most ordinary shell there is.** The
  normalization cap was applied to the line being SEARCHED as well as to the
  needle, so a default bash prompt on the same line consumed the whole budget
  before the typed text began.
- **tmux 3.5a returned empty listings.** It escapes the field separator into the
  four characters `\037`, where 3.7b passes the byte through — so every row
  parsed as one field and was discarded, on a version well inside the supported
  range.

[Unreleased]: https://github.com/husniadil/olympus/compare/v0.1.2...HEAD
[0.1.2]: https://github.com/husniadil/olympus/releases/tag/v0.1.2
[0.1.1]: https://github.com/husniadil/olympus/releases/tag/v0.1.1
[0.1.0]: https://github.com/husniadil/olympus/releases/tag/v0.1.0
