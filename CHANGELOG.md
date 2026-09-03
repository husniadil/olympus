# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project uses
[semantic versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **`focus <target>`, `focus_session`, `Olympus.Focus`.** Steer a server's
  focus onto a target without attaching. On herdr that is a workspace, tab or
  pane, the same steering a session-client attach performs first; on tmux a
  `<session>:<window>` or a pane id, since clients attached to one plain
  session share its current window and pane. Every client on a server shows
  the server's one focus, so a caller holding two clients onto two targets
  saw both render the target attached last; it now re-steers when it brings
  one to the front. `UNSUPPORTED` on zmx and meja, whose sessions are one
  pane; the new capability `focus` says which. Behavior spec §8.10, §13.

## [0.5.0]

### Added

- **A bare attach on tmux can be named and made mouse-less.** `olympus attach
  <session>[:<window>] --bare --view <name> --no-mouse`, and `BareViewName` /
  `BareWithoutMouse` in Go. An attach is interactive and has no channel to
  report a generated view name back, yet a consumer that scrolls the view
  (`view scroll`) or focuses a pane in it (`view focus`) while the attach runs
  has to address it; the name must begin with `olympus-view-` (§17.1) or it is
  `USAGE` before a view exists. `--no-mouse` is for a client that keeps its
  own text selection. Either flag on a backend whose bare attach makes no view
  is `USAGE`, not ignored. Behavior spec §8.9.
- **A tmux capture may name a window.** `screen <session>:<window>` reads that
  window's active pane, by index or by name, so the reader of a view pinned to
  a window has the same window's history. Every other verb stays
  session-scoped; `stop <session>:<window>` is not a way to close a window.
  Behavior spec §5.1.
- **Two capabilities: `session_client` and `bare`.** Whether a backend has a
  session client distinct from its raw per-pane stream (herdr) and whether an
  attach can be bare (herdr, tmux), so a consumer offering a "clean" or
  "mirror" attach branches on the capability rather than on the backend's
  name. Reported by `capabilities`, `doctor` and `info`. Behavior spec §13.

### Fixed

- **herdr's `servers` capability was reported false.** `olympus servers` has
  listed herdr's named sessions since 0.3.0, but `capabilities` said the
  backend could not enumerate them. It now says `true`.
- **`HERDR_ENV` is stripped from a herdr attach client.** herdr's session
  client refuses to start with the nesting marker set ("nested herdr is
  disabled"), so an Olympus driven from inside a herdr pane could never open
  one with `--client` or `--bare`. The marker decides nothing about which
  server is attached. Behavior spec §1.3.

## [0.4.4]

### Added

- **Focus a view's pane by cell.** `olympus view focus <view> --col N --row M`,
  the `focus_view` tool and `olympus.FocusView` select the pane of the view's
  current window that contains a 0-based cell of the client area, and report
  its id. For a view attached with mouse reporting off — a browser keeping its
  native text selection — where a click never reaches tmux and the §9.3 binding
  cannot fire. A border cell or one outside every pane selects nothing and
  reports an empty `pane`, a result rather than an error; the base follows,
  since the active pane is the shared window's. zmx, meja and herdr answer
  `UNSUPPORTED`. Behavior spec §9.6.

## [0.4.3]

### Fixed

- **A click in a view selects the pane.** The pass-through key table bound
  only the wheel, so a bare attach on tmux (`--bare`, a view) could not move
  between panes by mouse or touch. It now binds `MouseDown1Pane` to select the
  pane and forward the click, as tmux's root table does; the base follows,
  since the active pane is the shared window's. No drag binding. Behavior spec
  §9.3.

## [0.4.2]

### Fixed

- **`ls` no longer lists views.** A view (`olympus-view-<base>-<nonce>`, §17.1)
  is a tmux session to tmux, so the listing offered it as one, and a caller
  attaching what `ls` returned could open a view onto a view. The ergonomic
  layer's `Sessions` — `ls` and `list_sessions` — now leaves the reserved shape
  out; `view ls` is where views are enumerated. Behavior spec §9.5.

## [0.4.1]

### Fixed

- **A herdr session-client attach now ends with its target.** `olympus attach
  <target> --client` or `--bare` on herdr attaches herdr's own client to the
  whole session, so when the workspace, tab or pane it was steered onto closed
  — `exit` in the workspace's only pane — herdr moved the client to whatever it
  focused next and the attach never returned, leaving a caller that closes on
  exit showing a workspace it never asked for. Olympus now polls the target's
  presence while the client runs; when it is gone the attach prints `detached:
  the target is gone`, terminates the client (SIGTERM, then SIGKILL after a
  short grace) and exits `0`. The raw pane attach and the tmux bare attach
  already ended with their target and are unchanged. Behavior spec §8.10.
- **Go:** `backend.Attachment` gains `Probe`, the presence question the attach
  engine polls for exactly this; a backend whose client already ends with its
  target leaves it nil.

## [0.4.0]

### Changed — BREAKING on the herdr backend

herdr's hierarchy now maps onto Olympus's the way tmux's does: a **workspace**
is a session, a **tab** is a window, and a **pane** is a pane. Previously every
pane was a session. Olympus has no external consumers yet, so the mapping
changed in place; everything that meant something different before is listed
here. Behavior spec §3.6 is the single description; §8.10 covers attach.

- **`ls` lists workspaces, not panes.** A row's `name` is the workspace's label
  where it has one and its id (`w25`) where the label is empty — herdr labels a
  workspace from its directory when nobody names it, so `~` and `tmp` are what
  unlabelled workspaces are usually called, and the `id` beside the `name` is
  how a caller addresses one exactly. A pane label no longer names anything.
- **`panes` rows name their workspace.** `session_name` and `session_id` are the
  owning workspace's; `window_index` is the tab's number. `panes <workspace>`
  lists every pane in it, `panes <tab>` the tab's, `panes <pane>` that one.
- **Three target shapes, pane-precise.** `w5` (or a label) is a workspace,
  `w5:t2` a tab, `w5:p3` a pane. A verb aimed at a workspace or a tab acts on
  the pane it is showing; a pane target acts on exactly that pane — herdr is
  the one backend where a pane id does NOT resolve to its owning session, and
  the write lock is keyed on the target as given (spec §10.1).
- **`stop` closes the level you named.** A workspace with every tab and pane in
  it (`workspace close`), a tab with every pane in it (`tab close`), or one pane
  (`pane close`). Before, `stop` always closed one pane.
- **`start`/`new` return the workspace.** The created session's `id` is now the
  workspace id (`w5`) rather than the root pane's id; the name, the labelled root
  pane and the ensure semantics are unchanged.
- **`attach --client` and `--bare` take an ordinary target and steer the server
  onto it** — `workspace focus`, then `tab focus`, then `pane zoom --on` for a
  pane — before spawning herdr's session client. The target is no longer a herdr
  session name: the server is `--server <name>`, and the client attaches that
  named session (`herdr session attach <name>`), or plain `herdr` on the
  resolved socket when the server was addressed by path. A session-client
  attach therefore makes server calls, and is not-found before any client is
  spawned. There is deliberately no `<server>/<target>` grammar.
- **`self` inside a pane names its workspace**, not the pane.
- **`status` on a workspace is the workspace's metadata** (`workspace
  report-metadata`); on a pane it stays the pane's.
- **A session may not be named like a workspace id, a tab id or a pane id.**
  Creation rejects all three shapes as `USAGE`; before, only the pane shape was
  refused.
- **Go:** `herdr.WithServerSocket` takes the server's name as well as its
  socket, `backend.IndexedTabID` and `backend.IndexedWorkspaceID` join
  `IndexedPaneID`, and `olympus.OpenSessionName` is now used only by the bare
  attach on tmux.
- **CLI/MCP shapes are unchanged**: no field, flag, tool or code was added or
  removed; what changed is what the herdr rows and targets mean.

## [0.3.0]

### Added

- **Attach one tmux window, bare, without disturbing anyone.** `olympus attach
  <session>[:<window>] --bare` on tmux creates a throwaway view onto the
  session — a grouped session, already chrome-free: no status bar, no prefix —
  pinned to the named window (an index or a name), attaches the client to the
  view, and kills the view when the attach ends, on every exit path. A grouped
  session keeps its own current window, so the base and its other clients keep
  showing whatever they were showing; the active pane within a window is the
  window's and stays shared with the base. `--viewer` makes the view read-only;
  `--keep-others` has nothing to displace and is accepted. `--bare` keeps its
  herdr meaning (the session client with its chrome hidden, implying
  `--client`), and `--client` stays herdr-only; zmx and meja refuse `--bare` as
  `UNSUPPORTED`. Behavior spec §8.9.
- **Views can be pinned to a window.** `olympus view create <base> --window
  <w>`, the `window` argument of `create_view`, and `olympus.WithViewWindow`
  open the view on one of the base's windows instead of the one it is showing,
  leaving the base where it was. The window is matched exactly, by index or by
  whole name, against the base's own window list before the view exists; a
  window the base does not have is `SESSION_NOT_FOUND` with nothing created. A
  pinned view selects no pane, since the pane is shared with the base.
  `backend.ViewSpec` gains `Window`. Behavior spec §9.4.
- **Servers: the level above sessions, uniform across backends.** `olympus
  servers` lists the servers the resolved backend can see — tmux's named
  sockets in its per-user directory, herdr's named sessions from `herdr session
  list`, zmx's one socket directory — each with its socket, whether it is
  running, and whether it is the backend's default. `olympus servers stop
  <name>` stops one with every session on it, reporting `gone` or `killed`. A
  new global `--server <name>` selects a server by name for any verb, resolved
  into the backend's own address; it is `USAGE` together with `--socket`,
  `--socket-path` or `--zmx-dir`, `SESSION_NOT_FOUND` for an unknown name, and
  `UNSUPPORTED` on meja, which cannot enumerate its profiles. On herdr a server
  selected by name is addressed by its socket alone — the configuration and
  state redirect that `--socket-path` performs is deliberately not applied,
  since the socket lives inside the operator's configuration tree — and is
  never started by Olympus. The MCP door gains `list_servers` and `stop_server`
  (27 tools) and reads `OLYMPUS_SERVER`; the Go package gains `Servers`,
  `StopServer`, `WithServer` and the optional `backend.ServerLister` and
  `backend.ServerStopper` interfaces. Capabilities gain `servers`. Behavior
  spec §13.2.

## [0.2.5]

### Added

- **A session-client attach mode for herdr.** `olympus attach --client` attaches
  herdr's own session client — the one with mouse selection, wheel scroll and
  copy-on-select — instead of the raw per-pane stream `terminal attach` gives;
  the target is then a herdr SESSION name. `--bare` adds that client with its
  chrome hidden (an embedded stripped config), so it renders as a plain pane, and
  implies `--client`. Both are herdr-only and refused with a clear error on the
  other backends. The default attach is unchanged (raw per-pane), so existing
  callers are untouched. `AttachSpec` gains additive `SessionClient`/`Bare` fields
  that the other backends ignore. Herdr's `session attach` addresses a config-dir
  session name and ignores `HERDR_SOCKET_PATH`, so this path is a self-contained
  client launch (`Olympus.OpenSessionName`) rather than a re-pointing of the
  socket-addressed pane API.

## [0.2.4]

### Fixed

- **A herdr pane id past the ninth allocation was not recognised as one.**
  herdr spells every public id — workspace, tab and pane — as a base-32 number
  over the alphabet `123456789ABCDEFGHJKMNPQRSTVWXYZ0`: digits for the first
  nine, letters from the tenth, so the tenth workspace is `wA` and the tenth
  pane of a workspace is `w1:pA`. The workspace counter also survives a server
  restart. The pane-id predicate read digits alone, so on any server that had
  lived long enough a real id such as `w4Y:p1` (workspace 158) was taken for a
  session name: `start` accepted it as one, and the pane-id branch of target
  resolution never ran for it. Both segments now accept the alphabet, and the
  spelling is recorded in the behavior specification, §10.

- The window index of a herdr pane was 0 from the tenth tab onward, for the
  same reason: the tab segment was parsed as decimal. It is now decoded with
  the same alphabet, exported as `backend.PublicNumber`, so `w1:tA` reports
  window 10. Measured on a private server by creating nine tabs.

- The herdr package comment still described listing as reporting only the
  panes that carry a label; every pane is listed, under its label or its id
  (§3.4).

## [0.2.3]

### Fixed

- **A history depth on herdr returned fewer lines than no history at all.**
  herdr's `--lines` counts up from the bottom of the grid, visible screen
  included, while a depth is scrollback above the screen — so a request
  shorter than the viewport came back shorter than a plain capture. The
  viewport height is now added before asking. The server's cap of 1,000 lines
  includes the screen, so the history behind it is 1,000 less the viewport;
  recorded in the behavior specification, §6.4.

- A follow on herdr that the server ended — the terminal closed, or the server
  shutting down — reached the caller as a clean end of stream. It now ends
  with the server's own reason, so "no more output" and "no longer watching"
  are different answers.

- `self` inside an unlabelled herdr pane reported no session name, while a
  listing outside named that pane by its id. Both now give the id.

- A herdr handle that lost the race to start a server on an empty socket still
  recorded that server as its own, so its `Stop` would have taken somebody
  else's server down with every pane on it. Ownership is now recorded once the
  server answers and withdrawn when the spawned child exits with an error.

- A detached poll never disclosed a backend's read-depth cap on its own default
  window. The disclosure was computed from the raw `--lines` option, which is
  zero until the engine substitutes its default of 10,000 — so on herdr, whose
  server caps a read at 1,000 lines, every poll that did not pass `--lines`
  searched a tenth of the depth it reported nothing about. The window is now
  resolved in one place (`engine.Runner.PollWindow`) and both the search and the
  disclosure read it from there.

- A run whose output outgrew the backend's read depth timed out while the
  completion marker — and the exit code it carries — was legible on the screen
  it had just captured. Parsing required both markers at every window, a rule
  that is right while the window can still grow and wrong once it cannot: on
  herdr, whose server caps a read at 1,000 lines, any command producing more
  than that failed after the full timeout, and its detached form reported
  pending forever. A run at its maximum window, and every poll, now take the
  exit code the completion carries, report the output that remained above it,
  and disclose that the output begins partway through. The exit code is exact.
  Recorded in the behavior specification, §6.2.

### Added

- A run that times out with no start marker on its deepest capture now says so
  instead of reporting a bare timeout indistinguishable from a slow command.
  The message reports what was observed and names both causes it cannot tell
  apart: output scrolled past the backend's read depth, and a full-screen
  program covering the screen. Measured on herdr, those produce an identical
  capture and that backend does not track the alternate screen, so asserting
  either one would be a guess. A timeout whose start marker is on screen is
  left alone. Recorded in the behavior specification, §6.4.

## [0.2.2]

### Changed

- The conformance suite's shell-warming probe (`backendtest.Env.Warm`) now goes
  through the backend's atomic submit instead of composing an injection with a
  separate terminator. No production caller composes those two — every
  inject-then-submit path goes through one verb that owns its terminator and
  retries it — so the harness was proving a path nothing ships. Atomic delivery
  is also the only shape that is safe to re-send, which is what the warming loop
  does. Recorded in the behavior specification, §16.

## [0.2.1]

### Fixed

- The two herdr tests added in 0.2.0 assumed a headless server boots with a
  pane of its own. It does not in a clean environment (measured: zero panes),
  so the 0.2.0 release failed its own gate on every CI leg and published
  nothing. The tests now find the pane they created by the id herdr answered
  with. No behaviour change.

## [0.2.0]

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

[Unreleased]: https://github.com/husniadil/olympus/compare/v0.5.0...HEAD
[0.5.0]: https://github.com/husniadil/olympus/compare/v0.4.4...v0.5.0
[0.4.4]: https://github.com/husniadil/olympus/compare/v0.4.3...v0.4.4
[0.4.3]: https://github.com/husniadil/olympus/compare/v0.4.2...v0.4.3
[0.4.2]: https://github.com/husniadil/olympus/compare/v0.4.1...v0.4.2
[0.4.1]: https://github.com/husniadil/olympus/compare/v0.4.0...v0.4.1
[0.4.0]: https://github.com/husniadil/olympus/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/husniadil/olympus/compare/v0.2.5...v0.3.0
[0.2.5]: https://github.com/husniadil/olympus/releases/tag/v0.2.5
[0.2.4]: https://github.com/husniadil/olympus/releases/tag/v0.2.4
[0.2.3]: https://github.com/husniadil/olympus/releases/tag/v0.2.3
[0.2.2]: https://github.com/husniadil/olympus/releases/tag/v0.2.2
[0.2.1]: https://github.com/husniadil/olympus/releases/tag/v0.2.1
[0.2.0]: https://github.com/husniadil/olympus/releases/tag/v0.2.0
[0.1.2]: https://github.com/husniadil/olympus/releases/tag/v0.1.2
[0.1.1]: https://github.com/husniadil/olympus/releases/tag/v0.1.1
[0.1.0]: https://github.com/husniadil/olympus/releases/tag/v0.1.0
