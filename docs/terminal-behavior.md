# Terminal behavior specification

This document is the normative specification for how Olympus drives terminal
multiplexers. Each rule exists because the obvious implementation is wrong in a
way that stays invisible until it costs a bug.

**Read this before touching a backend.** Implementations MUST satisfy every
`MUST`/`MUST NOT` below. The conformance suite (`backend/backendtest`) enforces
the rules observable through the `Backend` interface. The rest are enforced by
each backend's own tests and marked *(backend-local)*.

| Backend | Floor | Note |
|---|---|---|
| zmx | 0.6.0 | the default; the reference version, support is best-effort |
| tmux | 3.3 | `allow-passthrough` landed there |
| meja | 0.0.25 | the oldest version measured |
| herdr | 0.8.2 | the version every measurement was taken against |

Platform: macOS and Linux only. §0 covers resolution, fallback, and the case
where no backend is installed.

## How to read it

This document is not read front to back, and its sections are not all the same
kind of thing:

| Sections | What they are | Who needs them |
|---|---|---|
| §0–§14 | The backend contract: what a backend MUST do, and why the obvious version is wrong | Anyone implementing or changing a backend |
| §15 | The MCP door | Anyone touching `internal/mcp` |
| §16–§17 | Testing requirements, reserved identifiers, isolation, defaults | Anyone writing tests, or choosing a default |

§15–§17 stay in this document because they cite §0–§14 throughout: they are the
same contract seen from the door's side.

New here and implementing a backend? [`adding-a-backend.md`](adding-a-backend.md)
is the route, and it names which sections matter at which step.

## Terminology

| Term | Meaning |
|---|---|
| backend | a multiplexer implementation: zmx, tmux, meja or herdr |
| session | a named, addressable terminal owned by a backend |
| target | the string a caller uses to address a session |
| door | a public entry point: Go API, CLI, MCP server |
| consumer | whatever is driving Olympus |

---

## 0. Backend selection and preflight

Nothing below matters until a backend has been chosen and proven to exist. Both
halves are contract, because both are the first thing a new user hits.

Four backends are supported, in this preference order: **zmx** (the default),
**tmux**, **meja**, **herdr**. The later ones answer only when every earlier one
is missing.

#### Why this order

Sessions are backend-scoped and never migrate. A backend that displaced another
in the order would move a caller's sessions to one they never chose. Each new
backend therefore goes on the END: every host that resolved to one backend
before it shipped keeps resolving to the same one after.

### 0.1 Resolution order

The backend resolves from the first of these that is set:

1. An explicit selection: CLI flag, library option, or MCP parameter.
2. The `OLYMPUS_BACKEND` environment variable.
3. **The default: `zmx`.**

An unknown backend name MUST be a usage-class error (exit 2), never
unexpected-class.

#### Why

The caller was offered a closed set of legal values, and one corrected argument
fixes it. Unexpected-class tells a machine consumer "retrying will not help",
which is the opposite of the truth.

### 0.2 Availability preflight

Before the first backend invocation, Olympus MUST verify the backend's binary is
on `PATH`. This is a single lookup, no subprocess, on every code path.

A missing binary MUST surface as a backend-unavailable error whose message names
the binary, says it was not found on `PATH`, and gives the install command for
the host platform. A raw `exec: "zmx": executable file not found in $PATH` is a
contract violation: it tells a first-time user nothing about what Olympus needs.

**Installed is not reachable.** The preflight proves only that the binary exists.
A zmx daemon that will not answer, or an unreachable tmux server, is discovered
at call time and surfaces as the same error class from there. The preflight makes
the *common* failure cheap and legible. It does not guarantee the backend works.

### 0.3 Fallback applies to the default only

| How the backend was chosen | Behavior when it is unavailable |
|---|---|
| Not selected (the default) | fall back to the next installed backend, in the order `zmx`, `tmux`, `meja`, `herdr` |
| Explicitly selected (flag, option, parameter or environment) | **no fallback, ever.** It MUST fail loudly |

#### Why

Refusing to start on a host with a working multiplexer installed is hostile for
no gain. But silently running somewhere the caller did not ask for is worse than
failing, so an explicit choice is never second-guessed.

### 0.4 A fallback MUST be disclosed

Sessions are **backend-scoped**: they never migrate and never merge, and a
session created on one backend is invisible from the others.

The **resolved** backend, not the requested one, MUST be observable:

- present in every structured output envelope;
- shown in human-readable listing output;
- reported by the diagnostic (§0.6) along with *why* it was chosen.

When a listing comes back empty, the door MUST name the resolved backend it was
empty *on*. It does not ask the other installed backends whether one of them
holds the missing sessions. The disclosure says where the answer came from, not
where the sessions went.

#### Why

A silent fallback would let a user create sessions, change their installed
tooling, and find those sessions apparently vanished with nothing explaining
why. An empty list that should not be empty is exactly when a user needs to
learn that backends are scoped.

Asking every other backend would put a subprocess per installed backend on the
cheapest read there is, against §0.2.

### 0.5 Version floors

| Backend | Floor | What the floor means |
|---|---|---|
| tmux | 3.3 | `allow-passthrough` |
| zmx | 0.6.0 | the reference version; support is best-effort |
| meja | 0.0.25 | the oldest version measured; §2.10 says why it is not raised to 0.0.26 |
| herdr | 0.8.2 | every measurement behind the backend was taken here |

For herdr those measurements are the verbs it drives, the error codes it
classifies, the raw-byte injection its key vocabulary rests on, and the
terminal-id timestamp its `created_at` is derived from (§3.4). Support below it
is best-effort because nothing was checked there.

A below-floor backend MUST be reported by name and version rather than allowed to
fail later in a way that looks like an Olympus bug.

A version probe costs a subprocess, so it is **not** part of §0.2's hot-path
preflight. It runs in the diagnostic, and at the specific call sites where a
below-floor version would misbehave silently rather than error.

### 0.6 The diagnostic is part of the contract

Olympus MUST ship a first-class diagnostic that reports, without side effects:

- which backends are installed and at what version;
- which one resolves right now, and by which rule;
- whether any is below its floor;
- the socket or directory in use (§17.2);
- install commands for whatever is missing;
- a **capability matrix** for every installed backend.

This is what turns "it does not work on my machine" into one command's output,
and it is what every error in §0.2 and §0.3 points at.

#### Why the capability matrix

The backends differ substantially (§13), and the default zmx is not the most
capable of them. A user needs one place that says so rather than discovering it
one unsupported error at a time.

### 0.7 No backend installed

The error MUST be a single complete message, not a failure per attempted backend.
It states that Olympus drives an existing terminal multiplexer and does not embed
one, gives the install command for each supported backend on the host platform,
and points at the diagnostic.

Olympus MUST NOT degrade to a non-multiplexer PTY here.

#### Why

Detach, reattach, and durable sessions are the whole product. A mode quietly
lacking them would fail later, further from the cause.

### 0.8 Degraded operations MUST announce themselves

Some operations succeed on the resolved backend while meaning materially less
than they do on another. They are not errors, since they return something real.
But a caller unaware of the difference draws a wrong conclusion from a successful
result.

A degrading operation MUST say so once: on stderr for the CLI (never stdout,
which is the data channel), and through the result for structured doors. Known
cases:

| Operation | Backends | What silently differs |
|---|---|---|
| pane listing | zmx | `current_path` is the spawn directory, frozen (§3.4) |
| pane listing | zmx | `current_command` is the spawn argv, not the live process (§3.4) |
| capture with history | zmx | the flag is accepted and changes nothing (§5.2) |
| capture | zmx | wrapped lines cannot be rejoined (§5.2) |
| capture metadata | zmx, meja | always zero, never tracked (§5.3) |
| capture metadata | herdr | the alt-screen flag is never tracked; the scroll position is real (§5.3) |
| session creation with a size | zmx, meja, herdr | the requested size is ignored: the session takes its size from the client that attaches it (§2.1, §2.10) |
| capture | herdr | wrapped lines cannot be rejoined, so they come back split (§5.2) |
| capture with history | herdr | a depth over 1,000 lines is clamped to 1,000, disclosed only when the request was above it (§6.4) |
| detached run poll | herdr | a window over 1,000 lines is clamped to 1,000, disclosed only when the request was above it (§6.7) |
| pane listing | herdr | `current_command` is reported for a targeted listing only (§3.4) |
| pane listing | herdr | `attached` is always false: no per-terminal client count exists (§3.4) |
| session listing | herdr below 0.9.0 | `focused` marks the workspace EVERY client on the server is showing; absent from 0.9.0, where clients keep their own view, and absent on every other backend, whose clients each show their own session (§3.4) |
| detached run poll | zmx | the requested window size is ignored (§6.7) |
| graceful kill | zmx | exec-spawned sessions cannot be interrupted (§2.8.1) |

Contrast with §12's `UNSUPPORTED`, which covers an operation the backend has **no
concept of** and which returns nothing at all. Degradation returns a real answer
with a narrower meaning. Failing these outright would make the default backend
refuse work it can do.

#### A warning belongs to the gap, not to the backend

Where two backends share a capability's false value they MUST both warn. A
caller reacts to the gap, and the gap is no smaller on a different backend.
Warning for one and not the other is how a second backend's identical limitation
becomes invisible.

#### Once per operation

Announce once per operation, never once per row. A warning per listed pane is
noise that trains users to ignore it.

#### A ceiling is disclosed conditionally

A backend that ignores a request warns every time, because every answer is
narrower than what was asked for. A backend that honours the request up to a
limit warns only when the request exceeded the limit.

Below the limit nothing is narrower, and announcing anyway would be noise and
untrue. The two look alike in a capability matrix and are opposite at the call.

---

## 1. Environment hygiene

A session's environment is not the environment of whatever created it. Olympus
sanitizes on every spawn path. Spawning and attaching have opposite requirements
for `TERM`, so their rules differ.

### 1.1 Spawn environment

Every session Olympus creates MUST be spawned with a sanitized environment:

| Variable | Rule |
|---|---|
| `TERM` | forced to `xterm-256color` |
| `LANG` | defaulted to `en_US.UTF-8` when unset or empty |
| `TMUX`, `TMUX_PANE` | stripped |
| `ZMX_SESSION`, `ZMX_SESSION_PREFIX` | stripped |
| `HERDR_SESSION`, `HERDR_SOCKET_PATH`, `HERDR_CLIENT_SOCKET_PATH` | stripped |
| `HERDR_PANE_ID`, `HERDR_WORKSPACE_ID`, `HERDR_TAB_ID` | stripped |

This applies to **every** spawn path: explicit creation, idempotent ensure, and
throwaway sessions.

The `LANG` default MUST be read at call time, never cached at process start.

This applies to Olympus's own TESTS as much as to a spawn. The suite is
routinely run from inside one of the sessions it describes, so a case that reads
these variables MUST clear them rather than inherit the machine's.

#### Why `TERM` is forced

A host running inside tmux or screen inherits a screen-family `TERM`. A shell
such as zsh, seeing a screen-family terminal, emits its window title as the
screen sequence `ESC k <title> ESC \`. A consumer that does not interpret that
sequence renders it as literal text, leaking every command name into the pane's
visible output.

#### Why `LANG` is defaulted

Processes started by launchd have no `LANG` at all. Output degrades to the
C/ASCII locale and every non-ASCII byte is mangled.

#### Why multiplexer identity is stripped

An inherited `TMUX` makes tmux treat the new client as a nested session,
changing its behavior including locale handling.

An inherited `ZMX_SESSION` is worse. `zmx attach <name> <argv>` with it set does
**not** create or attach `<name>`. It switches the *current* session's daemon,
yanking that session's leader client over to `<name>`. Running from inside a zmx
session without this strip hijacks a live session.

#### Why the herdr variables are stripped

They are two different hazards under one rule:

- `HERDR_SESSION` and the two socket variables RETARGET. They select which
  server a herdr command addresses, the way `ZMX_SESSION` does for zmx.
- `HERDR_PANE_ID` and its siblings IDENTIFY. They are how a process inside a
  herdr pane learns where it is. A session created on any backend from inside
  one would inherit them and answer "I am in a herdr pane" when asked its own
  address. That sends another program's reply to somebody else's terminal,
  which is what §13.1's status exists to make reliable.

### 1.2 The tmux server's global environment is a second leak

Setting `cmd.Env` on the tmux client Olympus execs is **not sufficient**. A new
tmux session's environment is seeded from the *server's* global environment,
fixed when the server booted. If another process booted the server on this
socket, sessions Olympus creates inherit that dirty environment regardless.

`new-session` MUST therefore also pass the sanitized values per-session via
`-e VAR=VAL` (tmux 3.2, below the floor). Passing `-e ZMX_SESSION=`, set to
empty rather than omitted, yields an empty value in the pane even against a
server whose global environment carries a poisoned one.

*(backend-local)* tmux re-sets `TMUX`, `TMUX_PANE` and forces `TERM` inside its
own panes regardless of what is passed. The tmux backend's *observable*
guarantees are therefore the `ZMX_*` strip and the `LANG` default only. Assert
exactly that subset: asserting the rest produces a test that passes for the
wrong reason. The full guarantee holds on zmx and on any non-multiplexer path.

### 1.3 Attach environment

The attach client builds its own environment rather than reusing §1.1's, because
an interactive attach MUST inherit the operator's real `TERM`. Forcing
`xterm-256color` would misrepresent the terminal the human is sitting at.

It MUST strip `TMUX`, `TMUX_PANE`, `ZMX_SESSION`, and `ZMX_SESSION_PREFIX`, and
MUST default `LANG` per §1.1. On herdr it MUST also strip `HERDR_ENV`.

#### Why `ZMX_SESSION`

It is worse here than on the spawn path. `zmx attach <name>` launched from inside
a zmx session **ignores `<name>` entirely** and fails with
`session "<ambient>" does not exist`, where `<ambient>` is whatever
`ZMX_SESSION` held. It does not degrade, it silently retargets. Any consumer
running inside a zmx session hits this on every attach.

#### Why `HERDR_ENV`

`HERDR_ENV` is the nesting marker herdr sets inside its own panes. The session
client refuses to start with it set ("nested herdr is disabled"), so a caller
driving Olympus from inside a herdr pane could never open one. The marker
decides nothing about which server is attached: the socket override or the
session name already does.

### 1.4 The tmux attach client needs `-u`

Without `-u`, the *client itself*, not the pane's programs, sanitizes every
non-ASCII byte to `_` before those bytes reach the consumer. The pane is fine;
the stream is not.

This is additional to §1.1's `LANG` default: `LANG` is for the programs inside
the pane, `-u` is for the client. The defect hides during manual testing from
inside tmux, because the inherited `TMUX` that §1.3 strips also flips the client
to UTF-8.

---

## 2. Session lifecycle

### 2.1 Creation

Creation takes a required, backend-unique name, plus optional working directory,
initial size, and command. An empty command means the user's default shell.

A command is not universally available. A backend whose panes run a program its
own configuration chooses refuses one outright rather than typing it (§2.3.1),
and declares `spawn_command` false so a caller can branch before asking.

Initial size on zmx is accepted for interface conformance and **ignored**. zmx
has no spawn-time sizing concept, and the PTY is sized entirely by whatever
client attaches later. Do not paper over this.

#### A session that finishes before creation returns is not a failure

Without `remain-on-exit` (§2.7) a session takes itself down when its command
exits, so a fast-exiting command is routinely gone by the time the confirming
listing runs.

Creation MUST NOT report that as an error, or an ordinary short command would
look like Olympus broke. It returns the row it can honestly give: named, outcome
`created`, liveness `gone`. The caller learns both that it was created and that
it is already over.

### 2.2 tmux option ordering: chain, never a second call

Options applying to a new tmux session MUST be chained into the *same*
`new-session` invocation using tmux's `;` in-process separator, never issued as
a separate `set-option` call afterwards.

**Chain order matters**: `remain-on-exit` first (pin the corpse), then
`allow-passthrough`. On any failure of the chained line, the session MUST be
killed best-effort so a half-configured session never leaks.

#### Why

A fast-exiting command tears its window down before a second tmux invocation can
run, which then fails with `no such window`. Symptoms of getting this wrong:

- `remain-on-exit` set separately does nothing for the fastest-failing commands,
  exactly the ones a caller most wants a corpse to inspect.
- `allow-passthrough` set separately makes successful spawns return a backend
  error, because the pane died before the second invocation ran.

The reverse chain order lets an instantly-exiting pane vanish between the two
chained commands before the corpse flag lands.

This race fails *intermittently*, so a single green test run does not prove it
fixed.

### 2.3 zmx spawn must exec, not type

Spawning on zmx MUST use `zmx attach <name> <argv>`, which execs `argv` as the
session process with nothing typed.

#### Why

`zmx run <name> <cmd>` *types* `<cmd>` into a login shell, echoing the command
text into scrollback. tmux hides this behind alt-screen redraw. zmx's native
scrollback shows it, putting the spawn command line into the session's own
output.

### 2.3.1 A backend that cannot spawn a command MUST refuse it, not type it

Not every multiplexer lets a caller choose a session's process. herdr's panes
run whatever its own configuration names: the `[terminal] default_shell` of a
server-wide config file. Neither its workspace-creation nor its pane-splitting
request carries an argv, so there is nowhere for `CreateSpec.Command` to go.

Such a backend MUST reject a non-empty command with an unsupported-class error,
before any invocation, and MUST declare `spawn_command` false (§13).

#### Why not type it

Typing the argv is the failure §2.3 exists to prevent, not a smaller version of
it. The command line lands in the session's own output, and every argument
carrying a shell metacharacter is reinterpreted by a shell that was never
supposed to see it.

#### The workaround belongs to the caller

Only the caller knows whether either cost matters: start a shell, then drive the
program from inside it. The conformance suite does that for the cases that need
a program on screen rather than the exec-versus-typed distinction itself. It
uses `exec <argv>` so the session's process still ends up being the program.

The consequence for §2.8.1 is that the two session shapes converge. A program a
shell `exec`s inherits an ordinary `SIGINT` disposition rather than `SIG_IGN`,
so where zmx's exec-spawned sessions cannot be interrupted at all, herdr's can.
Outcomes are still declared per backend and per shape.

### 2.4 zmx spawn is asynchronous, and the client's exit means nothing

**An early attach-client exit during spawn is NOT a failure signal.** With stdin
ignored, the client hits EOF and exits as soon as it has forked the daemon,
routinely *before* the session appears in `zmx list`. The daemon is a separate,
longer-lived process. The only correct check is polling `zmx list` to a
deadline, ignoring the client's exit entirely.

**The registration deadline is 15 seconds**, overridable by
`OLYMPUS_ZMX_REGISTRATION_TIMEOUT` and read at call time.

#### Why 15 seconds

Three seconds is too short. On a loaded host the daemon registers the session
*after* a tight deadline, so the caller reports failure and races a
deadline-triggered kill against the daemon's own session creation. The result is
a live but untracked orphan process.

15s makes that false negative rare while bounding a genuine failure (bad argv,
no daemon) to seconds.

### 2.5 zmx session names have a socket-path budget

The daemon places a session's socket at `<dir>/<name>`: bare name, no suffix.
`<dir>` is `ZMX_DIR` when set, otherwise `$TMPDIR/zmx-<uid>`. The rule is:

```
len(dir) + 1 + len(name) <= 103
```

(103 bytes of `sun_path` plus the NUL, i.e. `sunPathMax = 104`.)

Names exceeding this MUST be rejected up front with a usage-class error, before
any zmx invocation, naming the computed path, its length, and the budget.
Validation MUST live in the backend's `New`, so every path reaching it (create,
ensure, throwaway run session) inherits the rejection without duplication.

Resolving the socket directory for *validation* differs from resolving it for
daemon selection. The daemon-selection path returns bare `$TMPDIR`, which
under-counts the budget by the `zmx-<uid>` component, so validation needs its
own resolution.

#### Why

Without the check the failure is misleading rather than silent. zmx errors
loudly, but the spawn path deliberately ignores the spawn command's exit code
(`zmx run <name> -d` exits non-zero even on success). It falls through to the
15s registration poll and times out into a backend-unavailable error that never
mentions the real cause.

### 2.6 Idempotent ensure

Ensure makes a named session exist and be alive, reporting which of three things
happened:

| State found | Outcome | Action |
|---|---|---|
| alive | `reused` | options other than the name are ignored; the existing session is returned as-is |
| present but dead | `reaped` | kill, then recreate with the given options |
| absent | `created` | plain create |

Options apply on the create path only, and are **not retroactive** on a reused
session (§2.7).

#### The reaped branch is unreachable without a corpse

A backend that leaves no dead row makes a finished session indistinguishable
from an absent one, so it yields `created`. tmux sessions created without
`remain-on-exit` take their session with them when the pane exits, and zmx
auto-reaps immediately. The conformance suite MUST assert this explicitly, so a
backend that starts leaving dead rows surfaces there instead of silently
changing behavior.

#### Locking belongs to the caller

Ensure itself does no locking. The **caller** holds the per-session write lock,
which turns two concurrent ensures of one name into a deterministic outcome
instead of a race. With locking disabled, both can observe "absent" and both
create, and the loser's outcome is backend-defined.

### 2.7 `remain-on-exit` is tmux-only and write-only

On every backend except tmux it MUST fail with an unsupported-class error
immediately, before any backend invocation. zmx, meja and herdr have no corpse
concept: zmx's daemon reaps finished sessions itself, and a herdr pane whose
process exits is closed.

The rejection MUST happen in ensure *before* branching on session state, not only
inside create.

On tmux the flag is observable only through the corpse it eventually leaves.
There is no way to read it off a live session and no way to change it on one. A
live session created with the flag reuses like any other. The flag becomes
visible only once the tracked command exits and leaves a dead row for a *later*
ensure to reap.

#### Why ensure checks first

Otherwise the contract becomes state-dependent. A fresh name correctly rejects
via the create path, but an already-alive session takes the reuse branch, never
reaches create, and silently accepts and ignores the flag.

### 2.8 Graceful kill

Graceful kill is a decision engine with injectable operations (send interrupt,
probe, force kill, sleep), so it can be unit-tested without a backend:

1. **Probe first.** An initial "gone" means the session was *already* absent:
   outcome `gone`, zero interrupts sent.
2. Send N interrupts up front, with a gap between presses (none before the first,
   none after the last).
3. Poll presence until the session dies (`graceful`), or the timeout elapses,
   then force kill (`killed`).

The timeout bounds the **poll phase only**. Total wall time is
`presses*gap + timeout`.

| Setting | Default |
|---|---|
| presses | 1 |
| gap | 150ms |
| poll | 150ms |
| timeout | 2s |

All three outcomes are success. A transport error from any operation propagates
as an ordinary error instead. Interrupt and force-kill MUST both tolerate a
not-found error as success-shaped.

#### Why probe first

Probing first is what makes `gone` mean "was already gone" rather than "died at
some point". A gone observed only *after* interrupts is `graceful` instead, and
without the initial probe the two are indistinguishable.

#### Why not-found is success

A session dying between the probe and the call already means the desired state
holds.

### 2.8.1 Interrupting on zmx

On tmux, `C-c` reaches the foreground process group normally and none of this
applies. On zmx, writing `0x03` into the session interrupts nothing, for **two
independent reasons**. Conflating them leads to the wrong fix.

#### Cause 1: zmx's send path does not generate a terminal SIGINT

A foreground job with an ordinary default disposition (a `sleep` started by the
session's own interactive shell) survives `zmx send <target> $'\x03'`
indefinitely, yet dies immediately from `kill -INT -<foreground pgid>`. The
process was willing to die; the terminal path never produced a signal.

#### Cause 2: an exec-spawned session process inherits SIGINT as SIG_IGN

A session spawned as `zmx attach <name> sh -c 'trap "echo GOT" INT; …'` never
fires the trap, because a signal ignored on entry cannot be trapped or reset.
For such a process `kill -INT -<pgid>` returns success and the process survives,
while `kill -TERM -<pgid>` kills it instantly. Nothing can interrupt it with
`SIGINT`: not the terminal, not the OS.

#### Required behavior on zmx

- Olympus MUST NOT use the terminal `0x03` path to interrupt. It does not
  generate a signal even against a target that would happily die.
- The interrupt MUST be delivered as `SIGINT` to the session's **foreground
  process group**, derived from the leader pid zmx's listing reports and that
  process's controlling-tty `tpgid`. A `tpgid` equal to the leader's own process
  group means the session is at its prompt with no foreground job.
- **A session running a shell**, the default and common case, then behaves
  exactly as on tmux.
- **A session exec'd directly onto a non-shell argv** hits cause 2, and no
  interrupt is possible. Graceful kill MUST fall through to force-kill, which
  works. This is a property of how zmx spawns, not something Olympus can route
  around at kill time.

The conformance suite MUST assert outcomes **per backend and per session shape**
rather than papering over the difference with one expectation: shell-backed
sessions graceful on both, exec-spawned argv sessions graceful on tmux and
force-killed on zmx.

#### Not done: an exec shim

Resetting `SIGINT` to `SIG_DFL` immediately before `exec` would fix cause 2, but
requires an exec shim between zmx and the target argv. It is out of scope, and
recorded so it is not re-derived.

### 2.9 Test isolation is a hard requirement

Tests MUST NEVER touch the operator's live default server. Session-name
namespacing alone is not sufficient on any backend.

| Backend | Isolation |
|---|---|
| tmux | a private socket PATH per test process, inside a directory the test owns |
| zmx | `ZMX_DIR` set to a private temporary directory |
| meja | a socket PATH (`-S`), never a profile name (`-L`) |
| herdr | a private socket path, with the configuration and state directories moved with it |

The test process's HOME MUST be private too, with the configuration and state
homes under it. A pane's login shell reads the profile under HOME, and the
operator's can put another build of a backend on PATH ahead of the one under
test: on herdr 0.8.2 a pane's `olympus self` asked the operator's newer herdr
and named no session (measured on macOS). Go's build caches are pinned before
HOME moves, since they default to it.

#### tmux

A path is preferred over a NAME. Killing a server does not unlink its socket, so
named sockets accumulate in the directory shared with the operator's own
servers, while a path disappears with its directory.

A shared external server addressed by a bare literal name is a real collision
surface: two processes pointing at the same tmux socket name can crash the same
underlying server out from under each other.

#### zmx

zmx has no socket flag at all. Sessions are global to one daemon per user, and
the daemon's socket directory resolves from environment with priority
`ZMX_DIR` > `XDG_RUNTIME_DIR` > `TMPDIR`.

However carefully named, every test session still lands on that one shared
daemon, and test churn there destabilizes real live attach clients. Tests MUST
set `ZMX_DIR` to a private temporary directory, for the backend instance *and*
for every raw `zmx` verification or cleanup call.

#### meja

meja stores a server's session RECOVERY FILES beside its socket. A named profile
would leave persisted test sessions in the operator's own store, to reappear on
their next restore. A path takes the recovery store with it.

#### herdr

A private socket path is necessary and **not sufficient**. herdr keeps the
unnamed session's persisted layout (the workspaces and tabs a restore brings
back) in its CONFIGURATION directory rather than beside its socket. That
directory is chosen from the environment (`XDG_CONFIG_HOME`, else
`$HOME/.config/herdr`) with no reference to which socket is in use.

A second server on a private socket therefore overwrites
`~/.config/herdr/session.json` while touching none of the operator's live
sessions. That destroys their saved work in a way no care about session names
would catch.

The configuration and state directories MUST therefore move WITH the socket, and
the pairing MUST be derived rather than separately configurable. A caller who
moved one and not the other is back to the case above, and would have no way of
knowing.

Two further consequences of the same fact, both good:

- A server on a moved configuration directory reads no `config.toml` of the
  operator's. Unlike tmux (§17.5), a private socket here IS a private
  configuration.
- There is no socket-name form to get wrong, because herdr addresses a server by
  path only.

#### 2.9.1 Driving a server Olympus did not start

Pointing a socket path at a server that is ALREADY running is a first-class
mode, not a degraded one: a box's own headless herdr, or an operator's, holding
panes that other tools created. It is the case the backend exists for, and the
rules are all about restraint.

##### A server that answers is never started, restarted or reconfigured

The create path probes first and, finding one, uses it. Managed configuration
(§17.5) MUST NOT be written for it. That server was booted against somebody
else's directory and would never read the file, so writing one would be a claim
rather than a change.

##### Only the handle that started a server may stop it

Stopping takes every pane on the server down, including every one the caller
never mentioned. A request to stop a server this handle did not start MUST be
refused as `CONFLICT` rather than obeyed.

##### Ownership is recorded, never inferred

Nothing observable distinguishes a server Olympus booted from one it found
(§17.5 gives the same reason for tmux), and a server started by an earlier
Olympus process is not this handle's either. The fact is written down when the
server is started and read back from there.

It is deliberately not written for a server selected by NAME, which Olympus may
start but never owns (§13.2). The refusal above applies to such a server as it
does to one Olympus never touched.

##### The configuration directory follows ownership for the attach client only

Every other verb this backend runs is a JSON request over the socket and reads
no configuration. The attach client loads it and takes its mouse capture, scroll
lines, focus-redraw, host-cursor, sound and paste-key settings from there
(`src/client/mod.rs:1225-1234`, reached from `run_terminal_attach` at
`src/client/mod.rs:940-947`).

So an attach onto a server this handle started uses this backend's own
directory, and an attach onto anybody else's uses the ambient one. Otherwise a
human attaching to their own terminal would find it configured like a fresh
install.

### 2.10 meja routes input through a client

**How much of this applies depends on the meja version**, and both versions are
supported (§0.5 floors meja at 0.0.25). Measured on both:

| on a session with NO client attached | 0.0.25 | 0.0.26 |
| --- | --- | --- |
| `send-keys`: literal, named key, control key | refused | delivered |
| `set-buffer` | accepted | accepted |
| `paste-buffer` | refused | delivered |
| `send-keys -X` (copy mode) | refused | **refused** |
| listing, capture | accepted | accepted |

Through 0.0.25 meja refuses every input command on a clientless session.
`send-keys` and `paste-buffer` both answer `command requires an attached
client`, even when given an explicit `-t`. That is a structural difference from
tmux and zmx, which take input from any caller.

From 0.0.26 ordinary input is routed straight to the pane, and the refusal
narrows to copy mode alone, which answers `send-keys -X requires an attached
client`. Delivery was confirmed by capture, not by exit status: a zero exit says
the command was accepted, not that the keys arrived.

Observation never needed a client on either.

The rules below do not depend on the refusal and are not weakened by its
narrowing. The sizing rule governs any client Olympus attaches, and `Follow`
still attaches one on every version.

#### A transient client, only on refusal

Olympus attaches a **transient headless client** only when an injection is
refused, which on 0.0.26 is never for ordinary input. It MUST NOT hold a durable
one. Measured cost of attach-inject-detach: 68ms cold, 23ms warm.

The operation MUST be attempted first, and the client created only if it
refuses. The retry after attaching MUST poll the OPERATION rather than a status
field.

##### Why

A CLI process runs once and exits, so there is nowhere to keep a durable client.
A process that outlived the command to hold one would be the daemon §6.7 rules
out.

A session a human is already sitting in has a client, and joining it with a
second one is not free (see the size rule below). The retry polls the operation
because the question is whether the command works yet, and asking it directly
cannot disagree with itself.

#### A transient client MUST be sized to the session's current geometry

The request is the pane's current width and its height **plus one row**. meja
reserves one row for its status bar and subtracts it from every client, so this
measures as leaving the geometry untouched.

##### Why

meja sizes a session to its SMALLEST client and does NOT restore the size when
that client leaves. Measured: a human attached at 200x50 gives a 200x49 pane; a
client attaching at 80x24 shrinks it to 80x23, and it stays there after that
client exits. A driving client of the wrong size reshapes somebody else's
terminal, silently and permanently.

#### A refusal is not absence

`command requires an attached client` MUST NOT be collapsed into absence. It
shares an error class with an unreachable server but means the opposite of
missing: the session is there, and the client Olympus needed was not.

#### Following needs no output tap

meja has no `pipe-pane` equivalent, but everything a session renders is written
to every attached client. A headless client whose PTY is handed back is a copy
of the stream rather than a reconstruction of it.

Polling a capture instead would be a different thing under the same name. It
drops whatever is overwritten between two reads, and a follow that silently
loses output is worse than none.

### 2.11 Renaming

A target MAY be given a new name in place. The name is then what listings and
every client show and what the target answers to.

It is a capability, `rename` (§13), true on tmux and herdr. zmx and meja fix a
session's name at creation, so a caller has to know before asking.

The target reaches the backend as given, as §8.10's focus does, because the
point is the level below the session that §10.1's resolution would discard:

| Backend | What is renamed |
|---|---|
| herdr | the level the target names: a workspace, a tab or a pane, each of which carries its own label |
| tmux | a session, a `<session>:<window>`, or a pane's title from a pane id |

On herdr the new label is held to the same rule as a created session's name
(§10): one spelled like an id would shadow the id.

On tmux a session name carrying a colon is refused as `USAGE` rather than handed
to tmux, on rename and on create alike. Older tmux rewrites the colon to an
underscore (§8.9) and leaves the caller addressing a name that does not exist.
tmux 3.7c keeps it, and then no target can address the session: every target
splits at the colon, so a create's chained options fail, its cleanup fails too,
and a live session is left behind a not-found error (measured).

Presence is gated through the resolved session first, so a target naming nothing
is `SESSION_NOT_FOUND`. An empty name is `USAGE`.

---

## 3. Listing and liveness

### 3.1 zmx listing MUST use the long form

`zmx list --short` fails both questions listing must answer:

- It lacks the `clients` column, so attachment state cannot be computed.
- It **silently omits rows** for any session daemon that fails an internal
  1-second probe. That includes a live-but-busy daemon under heavy PTY output,
  not only a dead one. Using it for a liveness snapshot makes a merely slow
  session look gone and gets it wrongly reaped.

The long (default, tab-separated `key=value`) form keeps those rows, tagged with
an `err` field.

zmx has no separate session-id concept: the identity IS the name. Session ID MUST
equal session name, never the OS pid, which changes when a named session
restarts.

### 3.2 Liveness is tri-state, and the backend owns the classification

Every listed session and pane row MUST carry a liveness classification produced
**by the backend**, so consumers never parse backend-specific error strings to
make a reap decision:

| Liveness | Meaning |
|---|---|
| `present` | a live session the backend vouches for |
| `gone` | positive evidence of death; safe to finalize and reap |
| `unknown` | the row exists but could not be confirmed this pass |

`unknown` is indeterminate, and consumers MUST treat it as present for reap
purposes. Never finalize on doubt.

| Backend | Condition | Liveness |
|---|---|---|
| tmux | any listed row | `present` |
| zmx | no `err` field | `present` |
| zmx | `err=ConnectionRefused` | `gone` |
| zmx | any other `err` (e.g. `Timeout`) | `unknown` |
| meja | any listed row | `present` |
| herdr | any listed row | `present` |

`err=ConnectionRefused` is the *only* definitive death signal: zmx itself already
deleted the stale socket this pass.

A tmux corpse (`remain-on-exit`) stays `present` with the dead flag set. Liveness
and deadness are different questions.

#### Why the backend classifies

Leaving this classification consumer-side is how it gets lost. A listing that
synthesizes every row as alive gives consumers no `gone` signal at all, and dead
rows survive reconciliation forever.

### 3.3 "No server running" is an empty list, not an error

There is nothing to find; nothing went wrong asking.

#### A listing is eventually consistent after a kill

zmx keeps reporting a just-killed session for a fraction of a second while it
tears the socket down, and reports it with `err=Unexpected`. §3.2 classifies
that as `unknown`, not `gone`.

That is the tri-state working as designed: the row is indeterminate during that
window, and a consumer that reaped on it would be finalizing on doubt. Nothing
may require the row to have vanished the instant a kill returns. What is
required is that the listing converges.

### 3.4 Pane metadata divergences

These fields exist on every backend with different meanings, and MUST be
documented at every door rather than reported as equivalent.

#### herdr's levels

**On herdr a session is a WORKSPACE, a window is a TAB, and a pane is a pane.**
The mapping, the three target shapes and what every verb does at each level are
specified once, in §3.6, and every door cites it rather than restating it.

The field-level consequence: a pane row's `session_name` is its workspace's name
(the label, else the id), `session_id` is the workspace id, and `window_index`
is the tab's number. It is a public number, so the tenth tab is `w1:tA` and its
panes report 10.

#### `created_at`

| Backend | Source | Granularity |
|---|---|---|
| tmux | `#{session_created}` | session |
| zmx | the listing's `created` field | session |
| herdr | the pane's terminal id | pane |

tmux has no per-pane birth time. `#{pane_start_time}` and `#{pane_created}` do
not exist and expand to the empty string *with exit 0*, so trusting a wrong
format variable yields a silently zeroed column rather than an error.

herdr exposes no creation time anywhere in its API. Its terminal id is allocated
as the microseconds since the epoch in hex followed by a counter, so the leading
thirteen hex digits are the timestamp. That split holds from 2001 until well past
2300.

##### Why the terminal id on herdr

The alternative is a `ps` call per listed pane for the shell's process start
time. The id costs nothing, and if its shape ever moves it yields an implausible
epoch that fails §3.4's conformance case loudly rather than a plausible wrong
one.

#### `pid`

**`pid` is 0 where the backend does not report one.** It is the pane's own
process (tmux's `#{pane_pid}`, the `pid=` field of zmx's listing) and the root
the agent listing walks (§3.7).

meja's format has no such variable and herdr's snapshot carries no process id,
so their rows omit it. A caller on those backends gets the foreground-command
match and nothing deeper.

#### `focused`

**The session listing's `focused` flag says what every client is showing, and
only where that is one thing.**

herdr's server has one focused workspace and reports it on every version:

| herdr | What the server's focus means | `focused` |
|---|---|---|
| below 0.9.0 | the workspace every session client on that server displays | set on that row |
| 0.9.0 and later | where the NEXT client will land; each running client keeps whatever it was last steered onto | set on no row |

Below 0.9.0 the flag is what a consumer steering clients (§8.10) needs: a client
whose target is not the focused workspace is showing something the caller did
not ask for.

Measured with two clients on one server and `ui.window_title = "{workspace}"`
naming what each showed: on 0.8.2 focusing a third workspace moved both clients
onto it, on 0.9.0 it moved the foreground client alone and left the other where
it was.

A consumer reads the absence of the flag across a listing as "this backend
cannot say", never as "the focus is elsewhere". The wire shape is the same one
every backend without a shared focus produces.

##### Why from 0.9.0 the flag is absent rather than set

Set, it would answer a different question under the same name.

This is the one place a herdr version decides behavior instead of a request
being made and its refusal read (§12). herdr publishes nothing to ask: both
builds report one focus through the same field, and the per-client view lives
behind the client protocol where no API request reaches it.

#### Pane id is not unique across rows

Once a grouped view exists, a base session and its views share the same
underlying window and pane. A full pane listing reports the same pane id for
every group member.

Consumers needing one row per logical session MUST dedupe by pane id, keeping the
earliest `created_at` (the base, not a later view), and on a tie the lower
session number.

#### `current_path` and `current_command`

| Backend | `current_path` | `current_command` |
|---|---|---|
| tmux | live: `#{pane_current_path}` tracks `cd` | live: the foreground process's binary name |
| zmx | static: `start_dir`, captured at creation | static: `cmd=<spawn argv>` for an exec-spawned session, absent for a bare shell |
| herdr | live: the foreground process's own directory | live, but for a TARGETED listing only |

Reading zmx's `current_path` as a live tracker reports the original directory
forever. zmx's `current_command` reports the **spawn** command statically, as
`current_path` reports the spawn directory.

Liveness-by-command heuristics ("has a real command taken over from the shell?")
therefore work on tmux only. A consumer MUST NOT read a non-empty value on zmx as
evidence that the command is still running.

On herdr `current_command` is a second request per row, and the whole-server pane
listing is the cheapest read there is. A whole-server listing leaves it empty
rather than putting a subprocess per pane on every caller who asked what exists.
Disclosed as a degraded operation (§0.8).

#### `attached` on herdr

**`attached` is always false on herdr.** Its socket API reports no per-terminal
client count, so the field is a declaration rather than an observation. A
consumer MUST NOT read false as evidence that nobody is attached.

### 3.5 Presence probe is tri-state and fails closed

Probe answers `present` / `absent` / `error`. This is deliberately distinct from
listing's binary shape, where "no server running" flattens into an empty list
indistinguishable from "server up, session absent".

A named target that has never existed is `absent` **even with no server
running**. `tmux has-session` and `zmx list` against a truly absent name each
report a clean not-found, not a connection failure. The `error` arm is reserved
for unreachable backends.

Probe MUST NOT return a transport error; backend failure is the `error` state.

#### Why

Reconciliation safety. A caller polling across a flaky backend needs "definitely
gone" and "could not ask" to be different answers, so it neither wrongly
recreates nor wrongly gives up.

### 3.6 herdr: workspace › tab › pane

herdr's server owns workspaces, tabs and panes rather than sessions, and the
hierarchy maps onto Olympus's the way tmux's does:

| Olympus | herdr | id | name |
|---|---|---|---|
| session | workspace | `w5` | its label where it has one, else its id |
| window | tab | `w5:t2` | `window_index` is the tab's number |
| pane | pane | `w5:p3` | none |

Every number is a herdr public number (§10), so the shapes MUST accept letters:
the tenth workspace is `wA`, the tenth tab of it `wA:tA`.

#### Why not a session per pane

Making every PANE a session made `ls` on a real herdr a flat list of panes with
no workspace in sight, and `stop` on a "session" closed one pane of a workspace.
A caller who wanted the workspace (the thing a human sees in the sidebar and the
thing a fleet tool creates per worker) could not name it at all. The mapping
above is what herdr's own vocabulary means, and it is uniform with tmux's.

#### Naming

A workspace is named by its label, and by its id where the label is empty.

herdr labels a workspace from its directory when nobody names it (measured: `~`
for the home directory, `tmp` for `/tmp`). An unlabelled workspace is one
somebody emptied (`workspace rename w4 ""`). The far more common shape is several
workspaces carrying the SAME label because they were opened in one directory, so
a label is not unique.

- Resolution prefers an exact id and otherwise takes the lowest-numbered match,
  which is stable across calls.
- A caller who needs to be exact addresses by id. Every session row carries the
  id beside the name, so a listing hands out both.
- Creation refuses a name shaped like any id in the hierarchy (§10). A workspace
  with an empty label is NAMED by its id, and a label of that shape would make
  one name address two workspaces.

#### Three target shapes

A target's spelling says which level it addresses: a pane id is a pane, a tab id
is a tab, and anything else is a workspace, by id or by label.

A verb aimed at a workspace or a tab acts on the pane that level is SHOWING: the
focused pane of the tab, and for a workspace the focused pane of its active tab.
Which pane that is comes from the server's own layout rows. It does not come from
a pane row's `focused` flag, which is focus within the tab and stays set on a tab
the workspace is no longer showing (measured).

Every read that resolves a target takes ONE request, `herdr api snapshot`, which
carries all three levels at one moment. Walking `workspace list`, `tab list` and
`pane list` would read three moments of a server that changes between them.

| verb | workspace (`w5`, label) | tab (`w5:t2`) | pane (`w5:p3`) |
|---|---|---|---|
| type, paste, press, submit, send, interrupt | the pane the active tab shows | the pane the tab shows | that pane |
| screen, screen metadata, follow | the pane the active tab shows | the pane the tab shows | that pane |
| attach (raw stream) | the pane the active tab shows | the pane the tab shows | that pane |
| attach (session client) | steered onto the workspace (§8.10) | onto the tab | onto the pane, zoomed |
| panes | every pane of the workspace | the tab's panes | that pane |
| probe | present if the workspace exists | if the tab exists | if the pane exists |
| stop, kill | `workspace close`: every tab and pane | `tab close`: every pane in it | `pane close` |
| status | the workspace's own metadata | the pane the tab shows | the pane's own metadata |
| self | the workspace's name | none | none |

#### Stopping closes at the level named

Closing the resolved pane instead would be wrong: closing the focused pane of a
workspace that has two would leave the workspace standing with the other, a
session told to stop that did not.

Closing the only pane of a workspace closes the tab and the workspace with it
(measured), so a pane-addressed stop of a single-pane session still leaves
nothing behind.

#### A workspace with linked worktree workspaces takes the group with it

herdr refuses a plain `workspace close` on such a workspace with
`workspace_group_close_required`, naming the `--group` flag that closes the
group. It offers no close that takes the parent alone (measured on 0.8.2 and
0.9.0 alike).

Olympus asks for the narrow close first and widens only on that refusal, rather
than passing the flag always or reading the server's version.

##### Why

The refusal IS the answer, and it is the same on every build that has the flag.
Widening keeps a stop a stop: a workspace whose group stays open is a session
told to stop that did not.

It is also the one place a verb ends more than the target names, since the other
workspaces in the group are Olympus sessions of their own. It is recorded here
for that reason.

#### Creation

Creation makes a workspace with one root pane, labels both with the name, and
returns the WORKSPACE: `id` is `w5`, `name` is the label. The size is accepted
and ignored (§2.1), and a command is refused (§2.3.1).

**A pane target is pane-precise here**, which is the one deliberate exception to
§10.1 and is recorded there.

### 3.7 Agents in panes

The agent listing answers which panes a coding agent is running in (one of the
canonical names in the vocabulary below) and, where the backend can tell, what
it is doing.

It is a listing over panes, not a new level of the hierarchy: every row names its
pane and its session the way a pane row does (§3.4, §3.6). Which names those are
is not restated here in prose that would go stale as agents appear. The
vocabulary is a table, and the `kinds` verb (api §1) reads it back, so the answer
and what detection matches on are the same thing.

#### Every backend answers

The verb MUST answer on every backend, and MUST NOT be `UNSUPPORTED`. The answer
is an array, empty when there is no agent, never null.

##### Why

A backend with no way to see an agent still has panes. A pane whose processes
include one of the vocabulary's agents IS an agent, whatever else the backend
cannot say about it.

#### How a row was found

How a row was found MUST be disclosed on the row, in `detected_by`, because the
two ways differ in what the row can carry:

| `detected_by` | Method | What the row carries |
|---|---|---|
| `"herdr"` | native detection | name, `status`, `title`, `usage` where reported |
| `"command"` | the command heuristic | name; `status` read off the screen; no title, no usage |

##### Native detection

The backend watches its panes for agents itself and reports each one's state.

- A backend with native detection MUST report `status` (`working`, `idle` or
  `blocked`) and `title` on every row, marked `status_source: "native"`.
- It MUST set the `agent_status` capability, so a caller can learn before asking
  that the rows will carry them.
- A state the backend spells that is outside the vocabulary is reported as
  `unknown`, not passed through: the vocabulary is semver-bound (api §7).
- A backend MAY spell one of the vocabulary's states more than one way, and every
  spelling of it MUST be folded onto that state. herdr says `done` for an idle
  agent nobody has looked at since it stopped and `idle` for one somebody has,
  and both are `idle` here.

That `done`/`idle` difference is a fact about the OPERATOR rather than about the
agent. Only a backend drawing the panes can know it, and a status read off a
capture never can, so it MUST NOT enter a vocabulary every backend shares.

##### The command heuristic

The backend has no detection, so the ergonomic layer derives rows from the
whole-server pane listing.

- Where the pane's `pid` is known (§3.4), detection MUST inspect the pane's
  process subtree.
- Where it is not, detection MUST fall back to the pane's foreground command,
  read as an argv.

A process is named by its argv the same way in both:

- By argv0's base name when that is an agent's.
- When argv0 is a runtime or shell (sh, bash, zsh, fish, tmux, node, bun, python
  in any versioned spelling), by the script argument it runs, found by skipping
  options. An eval flag (`-e`, `-p`, `-c`, `-m`) means it runs no script and
  names nothing.
- Names are compared lowercased, with a wrapper suffix (`.js`, `.cmd`) removed.

In the subtree the order is:

1. The pane's own process. A pane spawned onto an agent IS the agent.
2. Its direct children. A pane spawned onto a shell runs the agent as the shell's
   child.
3. Every process below, where the best-scoring one wins. A name unwrapped from a
   runtime's argv outranks a plain binary, so `node …/bin/codex` outranks the
   `codex` helper it forks. The first found wins among equals.

A pane is listed once, whatever else below it carries the name. The heuristic
knows the agent's name and nothing else: the status is read off the pane's
screen (below), and the row carries no title and no usage.

The process table is read once per listing. Where it cannot be read the listing
does not fail: it falls back to the foreground command for every pane.

##### Why walk the subtree

The foreground command a backend reports is the pane's process-group leader by
its executable name. An interactive shell running an agent reports the shell, or
`node` for an agent that is a script. A pane spawned onto a shell on zmx reports
the shell's argv forever (§3.4).

#### `pid` on an agent row

A row MUST name the agent's own process in `pid` where one is known, and MUST
omit it where none is:

- On a command-detected row it is the process whose argv named the agent, so a
  match on the foreground command alone carries none.
- On native detection it is the pane's foreground process group leader, which is
  the agent while it holds the terminal. It is read per row from the backend
  where the listing itself does not say.

The pid is a handle, not a claim. What the process was started with, and by
whom, is the caller's to read, and the row says nothing about it.

#### The vocabulary

The name reported is the vocabulary's canonical one, not the token matched. The
`kinds` verb reports every canonical name with the executables and package
directories that identify it, derived from these tables so the two cannot
disagree (api §5).

It maps every alias a running agent's binary may carry to one name each:

| Aliases | Canonical name |
|---|---|
| `claude-code`, `claude` | claude |
| `cursor-agent`, `cursor` | cursor |
| `agy`, `antigravity`, `antigravity-cli` | agy |
| `muse-bin-<version>` (what muse's launcher execs) | muse |

An agent installed through npm runs as `node …/@anthropic-ai/claude-code/cli.js`,
where no token is called claude. The vocabulary also names the package
directories that identify one, and a token whose path holds one counts:

| Package directory | Canonical name |
|---|---|
| `claude-code` | claude |
| `@openai/codex` | codex |
| `@google/gemini-cli` | gemini |

#### Status is a real state or nothing

`status` is `working`, `idle`, `blocked` or `unknown`.

- `blocked` is the agent waiting on a person: a permission prompt, a question, a
  trust dialog. It is a state of its own and MUST be reported wherever it is
  known, never folded into `unknown` or `idle`. It is the state a caller most
  needs to act on.
- `unknown` means no evidence. A row MUST NOT carry any other status without
  evidence. A manifest with no rule matching the screen, an agent with no
  manifest, a capture that failed: all are `unknown`, never a guess, and never a
  fallback to `idle`.

#### Where the status came from MUST be disclosed

`status_source` is `native` for a state the backend reported itself, and `screen`
for one read off a capture of the pane. It is omitted when the status is
`unknown`, because there is nothing to attribute.

A screen-derived status MUST be marked `screen`. It was read from a snapshot, is
only as current as the capture, and can be fooled by text on the screen that
looks like the agent's own.

#### Screen-derived status

This is how a command-detected row gets a status. For each such row the
ergonomic layer:

1. Captures the pane's session: the visible screen, no scrollback, which is the
   viewport the rules were written against. On a backend whose only capture is
   the whole scrollback, the last 24 lines stand in for it.
2. Trims trailing blanks off every row.
3. Evaluates the agent's manifest over it: the agent's own rules for what its
   screen shows while working, idle or blocked, plus the pane's title where the
   backend reports one (the title the agent set through OSC 0/2).

A pane title that is only the terminal's default (tmux's host name) is not the
agent's word and is not passed.

The capture addresses the session, whose screen is its active pane's (§10). A
session holding more than one pane therefore has its agent rows left `unknown`
rather than read off a screen that may be another pane's.

The cost is one capture per command-detected row per call, and only for agents
that have a manifest. The `agent_status` capability is true on every backend
whose panes can be captured, which is all of them. `status_source` says which
kind of status a row carries.

#### The manifests

The manifests are herdr v0.9.1's, vendored (`internal/agentstate/manifests/`,
Apache 2.0) and evaluated by a port of herdr's engine:

- regions of the screen: the bottom N non-blank lines, what follows the last
  horizontal rule, the composer box, the title;
- matchers over them: substrings (case-insensitive), regular expressions,
  per-line regular expressions, and `any`, `all` and `not` gates;
- a priority per rule: the highest matching rule wins, the first in the manifest
  among equals.

A rule marked `skip_state_update` recognises an overlay through which the state
cannot be read (a transcript viewer, a model picker) and yields `unknown`.

Agents with no manifest upstream (aider, goose, omp, mastracode) are listed with
`unknown` and their screens are not captured.

##### One departure from herdr

herdr watches a pane continuously and falls back to `idle` when nothing matches,
because a screen with no evidence there is a settled screen. A listing reads one
snapshot, so here nothing matching is `unknown`.

##### Two Codex dialogs upstream's manifest does not read

Codex asks whether to trust a directory, and then whether to trust its hooks.
Both wait on a person, and both MUST read as `blocked`.

- The trust dialog's first line may follow the shell lines above it, so it is
  read from the bottom twenty non-blank lines and matched at the start of any
  line rather than of the region. herdr's detection snapshot holds those shell
  lines when the pane's bottom rows are blank (measured on herdr 0.9.0), and a
  tall pane can hold more than twenty of them.
- The hooks dialog ends "press enter to confirm or esc to go back", which the
  confirmation rule did not list. Text typed into it moved its choice from
  Review hooks to Trust all (measured on codex-cli 0.154.0). It is read by its
  title and that footer together, since Codex's pickers may end the same way.

Each changed line in `codex.toml` carries a comment with upstream's.

#### What the agent said is asked for, never assumed

`last` carries one line of the agent's own output: what it last said, or, where
the row is `blocked`, the question it is waiting on.

It MUST be filled only where the caller asked for it, and MUST be omitted
otherwise. Empty is the honest answer and MUST NOT be replaced by a guess: a
screen with nothing to say, an agent with no manifest, a capture that failed all
leave it empty, and the row is still an agent.

##### Why

`status` says somebody is needed. It does not say what for, and a caller that
has to act on a blocked row needs the question.

A row whose status came from the backend was never captured, and filling `last`
captures it, one call per row. A caller that only wants to know what is running
MUST NOT pay for one that wants to know what it said.

##### How the line is read

The line is read with the manifests' own regions rather than rules invented for
it:

| Row status | Line |
|---|---|
| not `blocked` | the last non-blank line above the composer box, which also drops the status area the box sits on |
| `blocked` | the last line ending in a question mark within the bottom 30, else the last line after the final horizontal rule, since a dialog states its case where it does not ask |

Box borders, a TUI's own key hints and the client's unread-message overlay are
not the agent's words and are dropped.

#### `usage`

`usage`, where a natively detecting backend reports it, is the agent's own quota
readout: an ordered list of `{label, percent}` bars. The label is as the agent
spells it (`5h`, `7d`, a model name) and the percent is an integer 0–100.

A bar the backend renders in a shape Olympus cannot read is skipped, not an
error. The bars are display text the backend owns, and a row is an agent with or
without them.

---

## 4. Input injection

The most expensive rules in this document. Read all of them.

### 4.1 tmux injection is buffer-based with per-call unique buffer names

Literal text MUST be injected via `load-buffer` (stdin to a named buffer)
followed by `paste-buffer -d`. It MUST NOT use `send-keys -l`, which mangles
special characters and cannot carry arbitrary bytes via stdin.

The buffer name MUST be unique per call: process id plus a monotonic counter.

#### Why

Two concurrent injections sharing a name race. One call's `load-buffer`
clobbers the other's text before `paste-buffer` consumes it.

### 4.2 `paste-buffer -d` deletes only on success

`-d` is not unconditional cleanup. tmux deletes the buffer only when the paste
succeeds. If the target pane vanished between `load-buffer` and `paste-buffer`,
a real race window, the buffer leaks forever unless the caller issues an
explicit best-effort `delete-buffer` on the failure path.

That cleanup call's own failure MUST be swallowed so it never masks the real
error.

### 4.3 Literal injection never submits, on any backend

Placing text in the input line and submitting it are separate operations. The
injection primitive MUST NOT press Enter. Submission is an explicit, separate
call made by the consumer.

#### Why

It keeps injection symmetric across backends and composable, and it means
§4.4's retry discipline belongs to whoever issues the Enter.

### 4.4 A failed Enter after injection MUST be retried once

Any composed operation that injects then submits MUST retry the Enter exactly
once before surfacing an error.

#### Why

Once text sits in the input line, a failed Enter does not merely fail visibly.
It leaves unsubmitted text there, and the next injection silently concatenates
onto it, corrupting both.

### 4.5 The submit terminator MUST be a separate, delayed, lone write

The submitting terminator MUST register as a keypress on paste-detecting
consumers, never as part of a paste.

#### Why

A single write containing both text and a trailing `\r` is treated as a paste
by an Ink-based REPL. The terminator becomes a literal newline inside the input
box and nothing is submitted. Submitting requires a separate write containing
only `\r`, after a 150ms settle gap.

#### Per backend

| Backend | How the terminator stays a keypress |
|---|---|
| tmux | two `send-keys` subcommands chained by a literal `";"` argv element into one client invocation |
| zmx | text write, 150ms settle, then a lone `\r` write. zmx has no subcommand chaining, so backend-level single-operation atomicity is unachievable |
| meja | `send-keys -l` for the text, then a separate `send-keys Enter`, both on one held client |
| herdr | one `pane run` request. The server frames the text as a paste and encodes the Enter after it as a keypress, in one write |

### 4.6 Paste is normalized, multi-line, and never auto-submitted

Paste lands multi-line text in the input line without submitting it. The final
line is never submitted without an explicit separate Enter. That is the
cross-backend guarantee.

#### Intermediate lines differ by backend

Whether intermediate lines execute depends on the consumer and the backend:

- A canonical-mode, non-line-editing consumer (a raw pipe, `cat`) has no
  bracketed-paste awareness, so embedded newlines execute one line at a time on
  every backend.
- A bracketed-paste-aware line-editing consumer (zsh/ZLE, bash 5.1+ readline,
  TUIs) diverges:
  - tmux's `paste-buffer -p` emits DECSET-2004 framing (`ESC[200~` /
    `ESC[201~`) around the payload. Such a consumer receives one un-executed
    paste event, and intermediate lines do not execute.
  - zmx and herdr paste with no framing. The same shell sees plain newlines
    and executes each intermediate line as it arrives. herdr has a framed
    injection, but only in the form that appends an Enter.

Against a spawned zsh session, a two-line tmux paste leaves both lines
unexecuted while the same zmx paste executes the first.

#### Per backend

| Backend | Paste is |
|---|---|
| tmux | literal injection with `-p` added to the `paste-buffer` argv. Unique buffer, delete-on-failure and error mapping are as §4.1, so no buffer leaks |
| zmx | a presence check followed by a raw send with no terminator |
| meja | `set-buffer` then `paste-buffer -d` |
| herdr | the same raw write as literal injection |

### 4.7 Atomic submit trades verification for atomicity

An atomic operation delivers text and submits it as one caller-visible unit,
so the retry unit is the whole delivery-plus-submit. A caller retrying a failed
invocation can never leave a typed-but-unsubmitted line behind to double.

Atomic submit MUST NOT verify. Callers needing both properties do not get them
from one call, and the doors MUST reject the combination.

Atomic submit is single-line only, since multi-line text has no unambiguous
submit point. The door layer validates and rejects `\n`/`\r`. The backend does
not re-check.

On zmx, caller-visible atomicity comes from holding the per-session write lock
across both writes. A failed submit write MUST return a timeout-class error
("text delivered but not submitted"), never silent success.

#### Why

Verify-then-submit cannot provide atomicity. Its Enter is a separate call, and
any cross-invocation retry re-types the text before checking, doubling it.

### 4.8 tmux eats an unescaped trailing semicolon

Every argument a caller supplies to tmux MUST have a trailing `;` escaped to
`\;`: the chained `send-keys` text, and also a created session's name and
command, a rename's name and a status value. The escape is unconditional, since
tmux also strips one backslash from a trailing `\;`. `-l --` is also required
on `send-keys`, guarding against text beginning with `-`.

#### Why

tmux's `;` chaining separator treats an unescaped trailing `;` byte in a text
argv element as a command separator. `-l -- "echo A; echo B;"` lands
`echo A; echo B` with the final `;` dropped, and text that is just `;` lands
nothing. Interior semicolons are untouched. tmux parses every argument of a
command line this way, chained or not: a session renamed to `r;` becomes `r`,
and `x\;` lands as `x;` unless it is sent as `x\\;` (measured on tmux 3.7c).

### 4.9 Control keys are not deliverable on every backend

A backend may accept a control key and silently not deliver it. That is worse
than refusing it: the caller sees success and waits for an effect that never
comes.

Measured by sending each byte to `cat -v` in a live session and reading back
what arrived:

| | tmux | zmx | herdr |
|---|---|---|---|
| printable text | delivered | delivered | delivered |
| tab, terminator | delivered | delivered | delivered |
| control letters (`c-a`, `c-x`, ...) | delivered | **dropped** | delivered |
| lone escape | delivered | **dropped** | delivered |
| arrows, home | delivered | **dropped** | delivered |
| page-up, function keys | delivered | delivered | delivered |
| backspace, end, page-down | delivered | not measured | delivered |
| delete, s-tab, c-up/down/left/right | delivered | not measured | delivered |
| m-<letter>, m-enter | delivered | not measured | delivered |
| s-up/down/left/right, m-up/down/left/right | delivered | not measured | delivered |
| m-<symbol> (`m-0`, `m-/`, `m-;`, ...) | delivered | not measured | delivered |

A backend whose key names cannot reach a key MAY spell that key as its bytes
through a literal path, but only where the bytes are measured arriving.

#### herdr spells every key as bytes

herdr's text-injection request writes the bytes it is given straight into the
pane's PTY with no interpretation, so Olympus spells every key itself.

The backend's own key vocabulary is narrower than Olympus's. It has no home,
end, page-up or page-down, and spells the control range `ctrl+a` rather than
`c-a`. The naming path would have refused four of Olympus's named keys. The
byte path delivers them.

#### meja takes tmux's names, with two exceptions

meja delivers the control range and most keys under tmux's names. Two differ:

- Forward delete is `Delete`. tmux's `DC` is typed as two letters.
- Back-tab has no name meja delivers. `BTab` is typed as four letters, and
  `S-Tab` loses its shift, so the pane reads `^I` where `^[[Z` was meant. meja's
  `send-keys -l` writes its argument unencoded, so back-tab is sent that way as
  the bytes `ESC [ Z`, and arrives.

#### Modified arrows and alt symbols

On tmux and meja these arrive under their names: `S-Up` to `S-Left`, `M-Up` to
`M-Left`, and `M-` with the character itself. All 42 printable non-letters read
back as `ESC` and the character.

tmux needs one spelling of its own. `M-;` ends in the `;` §4.8 is about, and
tmux splits it off even inside a key name: in `send-keys M-; C-j` the `C-j` is
run as a command and refused. It is sent as `M-\;`, and arrives. meja does not
split its arguments on `;` and takes `M-;` as it is.

#### zmx is not mapped further

The zmx boundary is irregular and is deliberately not specified further. What a
caller needs is that control keys cannot be relied on there, which the
`control_keys` capability (§13) reports. Mapping the exact set would invite
depending on it.

The consequence is concrete: an editor opened on zmx can be typed into and
read, but not saved or exited, because both are control keys. Doors MUST report
this through the capability rather than by failing the keypress, since the
keypress itself succeeds.

### 4.10 The key vocabulary is open, in five shapes

`press` takes five shapes, and every backend MUST translate all five:

- a named key: `enter`, `escape`, `tab`, `s-tab` (back-tab), `backspace`,
  `delete` (forward delete), `space`, `up`, `down`, `left`, `right`, `c-up`,
  `c-down`, `c-left`, `c-right`, `s-up`, `s-down`, `s-left`, `s-right`,
  `m-up`, `m-down`, `m-left`, `m-right`, `home`, `end`, `page-up`,
  `page-down`, `m-enter`;
- `c-<letter>` for any ASCII letter with control held;
- `m-<letter>` for any ASCII letter with alt held;
- `m-<symbol>` for any other printable ASCII character with alt held: a digit
  or a punctuation mark, `!` to `~` less the letters;
- `f1` to `f12`.

#### Why open

A closed list of keys is the obvious design and is wrong. Driving a full-screen
program means pressing whatever it binds, and a caller who cannot spell Ctrl-X
cannot leave nano.

#### Spelling

A letter's case is a spelling, not a different key. A symbol is spelled as the
character itself, `m-;`, `m-\`, `m-'`, with no escaping and no alias. No shell
stands between a caller and the key, so quoting is the calling shell's
business, and a second spelling would be a second name for one key.

#### Bytes

The keys are those a terminal sends:

| Key | Bytes |
|---|---|
| `delete` | `ESC [ 3 ~` |
| `s-tab` | `ESC [ Z` |
| modified arrows | xterm form `ESC [ 1 ; m A` to `D`, `m` 2 for shift, 3 for alt, 5 for control |
| alt chords | a prefix, not a bit: `m-a` is `ESC a`, `m-/` is `ESC /`, `m-enter` is `ESC CR` |

A backend that spells keys by name uses the multiplexer's own name for the same
keypress, and where that name does not deliver it, §4.9 applies.

#### Everything else is `usage`

That includes what merely looks like a shape: `c-1`, `m-ab`, `m-12`, `m-`
followed by a space, a tab, DEL or a character past ASCII, `meta-a`, `s-a`,
`s-home`, `c-home`, `f0`, `f13`. Accepting one would mean sending nothing and
reporting success.

Space and the control range are not symbols because a key bar does not send
them as one. `m-enter` is the one alt chord of that kind with a name. Function
keys stop at 12 because terminals disagree about the encoding above it.

---

## 5. Screen capture

### 5.1 tmux capture flags are mutually constrained

| Flag | When | Why |
|---|---|---|
| `-J` | viewport captures only | rejoins a line tmux auto-wrapped at the pane's width, so a consumer matching against the text sees one logical line |
| `-e` | opt-in colors | preserves ANSI escapes; stripped by default |
| `-S -<lines>` | opt-in history | scrollback above the visible viewport, to the requested depth; §6.4 decides that depth |

**`-J` MUST be dropped whenever history is requested.**

#### Why

`-J` is correct on the live viewport, where nothing has been wrapped and
re-flowed since. Across full scrollback it is wrong: `capture-pane -J -S -`
rejoins a long line that tmux already wrapped at capture time with its own
historical continuation, merging two scrollback lines that never appeared as
one on screen.

#### A capture target MAY name a window

On tmux the target may be `<session>:<window>`, by index or by name, and it
then reads that window's active pane. This is the one operation outside §8.9
that takes the shape. The split is the first colon, as §8.9 splits it. A window
the session lacks is `SESSION_NOT_FOUND`.

§10.1 is otherwise unchanged: every other operation stays session-scoped, and
`stop <session>:<window>` is not a way to close a window.

It exists for §8.9's reader. A bare attach pins a view to a window, and whoever
reads that view wants the same window's scrollback, which the session's own
active pane may not be showing.

### 5.2 zmx capture: no rejoin, opt-in colors

zmx's history command has no `-J` equivalent. A line hitting the session PTY's
width comes back split by a literal `\n` indistinguishable from a real newline.
Olympus passes this through unmodified. Consumers matching against zmx capture
output MUST tolerate a wrap-split line. §6.4 and §7.3 both exist because of
this.

The session PTY's fallback size, when no real attach client has ever resized
it, is 24 rows by 160 columns. That is hardcoded in zmx upstream, so it is
coupled to the zmx release rather than the host.

Colors are opt-in and not a no-op on zmx. Default output has every ANSI escape
byte stripped, and the VT flag preserves them byte-for-byte.

History is a documented no-op on zmx, whose history command already returns
full scrollback with no separate viewport mode. Both flag states MUST return
byte-identical output, regression-guarded.

#### A capture reflects an in-place repaint

A program that clears and redraws is captured as it currently appears, not as
it first appeared, on tmux and zmx alike. The limitation that blocks driving a
full-screen program on zmx is input, not capture (§4.9).

#### Trailing whitespace differs by backend

tmux preserves a row's padding and the trailing space of an unterminated
prompt. zmx normalizes it away, so a REPL prompt captured as `>>> ` on one comes
back as `>>>` on the other. A pattern that requires a trailing space matches on
one backend and silently never on the other.

Doors MUST NOT paper over this by re-padding. The fix belongs in the pattern
(`^>>>\s*$`), and §5.4 says so where callers will read it.

### 5.3 Alt-screen panes are captured; only their scrollback is refused

A pane on the alternate screen (a full-screen program issuing `\e[?1049h`) has
no scrollback. Its visible grid is real and readable; there is nothing behind
it.

Two layers behave differently:

- **The backend's capture method** never refuses a target for being on the alt
  screen. It returns the visible grid, with the alt-screen metadata flag beside
  it.
- **The door layer** gathers metadata for every target first. Where the
  alt-screen flag is true it drops any history request for that target and
  discloses that it did (§0.8). It still captures.

**The door MUST NOT skip an alt-screen capture.**

#### Why

Skipping it assumes a live consumer already mirrors the grid, which is true
only for an attached human. For a program driving an editor, a pager or a TUI
client, the visible grid is the only way to observe it at all. Skipping would
mean such a program can be started and never seen, with an empty string and no
error.

What the alternate screen lacks is scrollback, and that is the part the door
refuses. The request is dropped and a warning says so, rather than quietly
returning less than was asked for.

#### Backends that do not report alt-screen

zmx never reports it. Its capture metadata is always the zero value, with no
subprocess run to check. This is not an unsupported-class error: the question
has an honest answer on zmx ("not tracked"), so the call succeeds with zeroes.

herdr never reports it either, for a different reason. Its terminal tracks the
alternate screen internally, and nothing in its socket API exposes that. The
answer is the same, "not tracked", because the capability describes what
Olympus can observe, not what the multiplexer knows. A capture of an alt-screen
pane there still returns the visible grid, and a scrollback request comes back
with the grid alone.

### 5.4 Waiting for a pattern is line-oriented

Waiting matches a caller's regular expression against each line of the screen,
never against the whole capture as one string.

Each line is tried both as captured and with trailing whitespace trimmed.

**Patterns MUST NOT require a trailing space.** Whether one survives into a
capture is a backend difference (§5.2), so `^>>> $` matches on one backend and
never on the other. `^>>>\s*$` is the portable form, and doors SHOULD say so
where callers will read it.

The matched line is reported alongside the screen.

#### Why

Callers write line-oriented patterns: `^>>> ` for a REPL prompt, `\$ $` for a
shell. A regular-expression engine anchors `^` and `$` to the whole text by
default, so whole-screen matching makes every anchored pattern silently never
match, while a plain substring keeps working. That is what lets the defect ship
unnoticed.

Trimming exists because a terminal pads rows to the pane's width. The padding
is invisible to whoever wrote the pattern, and requiring them to know about it
would tie the pattern to a width they do not control.

The matched line is reported because a caller waiting on a pattern almost
always wants it, and re-running the match to find it would reimplement what
just happened.

### 5.5 Capture metadata

Per-target metadata carries the alt-screen flag and the copy-mode scroll
position (lines scrolled up from the live bottom; 0 when not in copy mode).

The two halves are independent:

| Backend | Alt-screen flag | Scroll position |
|---|---|---|
| tmux | tracked | tracked |
| zmx | not tracked | not tracked |
| meja | not tracked | not tracked |
| herdr | not tracked | tracked, on every pane row |

### 5.6 Following is a tap on the stream, not a capture in a loop

Following streams a session's output as it is produced. What a follower
receives is raw terminal output, escape sequences included. It is a stream, not
a rendering: a caller that wants to match on content captures or waits instead,
and one that wants a picture renders it.

#### Why not capture in a loop

A capture reports the pane as it looks now. Anything printed and scrolled past
between two polls is gone, which is the output someone following a long build
cares about. A program that repaints in place has no meaningful delta between
polls at all.

#### Per backend

Every backend provides a primitive, and the backend layer uses it rather than
emulating one:

| Backend | Primitive |
|---|---|
| tmux | pipes the pane into a command |
| zmx | tails the session |
| meja | hands back a headless client's PTY |
| herdr | streams read-only frames |

tmux pipes into a command rather than a descriptor Olympus holds, so the tap
points at a temporary file the reader follows. Turning the tap off MUST happen
before that file is removed, or tmux keeps writing to a path that no longer
exists for as long as the pane lives. A pane has one pipe, and a second
`pipe-pane` silently replaces the first, so a follow of a pane already piped,
by another follow or by the operator, is `CONFLICT`. The reader cannot see the
pane end through a file, so it asks whether the session still exists while it
waits, and the stream ends when the session does, as it does on every other
backend.

herdr's stream emits one JSON envelope per frame carrying base64 ANSI. The
backend decodes those back into the byte stream this interface promises, so no
consumer has to know about the wrapping.

Following on herdr does not resize the pane, so the frames' geometry belongs to
the follower alone. Measured: a pane at 70x22 stayed 70x22 while a follower read
it at 100x30.

On herdr the stream is the server's rendering of the pane rather than the exact
bytes the program wrote. A repaint reaches a follower as the cursor addressing
that redraws it. Output scrolled past between two captures is still delivered,
but a consumer diffing a follow against a program's own stdout would find them
different.

---

## 6. Running commands

### 6.1 The sentinel protocol

Running a command in an existing session injects a sentinel-wrapped line and
polls the screen for completion:

```
echo <START>; <cmd>; echo "<DONE>_$?_"
```

The identifier baked into both markers MUST be unique across concurrent
processes, goroutines, and time: process id, per-process counter, and random
bytes.

#### Quoting does not hide a marker from the screen

The injected line echoes both marker strings onto the pane before the shell
runs it. Quoting controls shell parsing, not terminal rendering. What tells the
echoed command line from the real completion is expansion: the echoed line
shows a literal `$?`, while the real DONE marker is followed by digits.

### 6.2 Marker parsing rules

- **DONE** is the last occurrence of the done marker immediately followed by
  1 to 3 decimal digits and then a literal `_` delimiter. The digit requirement
  rejects the echoed, unexpanded occurrence.
- **START** is the last occurrence of the start marker strictly before the DONE
  position. The command line's echo of the start marker appears before the real
  start marker's own output, so "last before DONE" selects the right one.
- **Wrap tolerance**: the raw capture is stripped of newlines once into a search
  copy with a parallel index map back to raw offsets. Parsing runs against the
  stripped copy. Positions map back through that table to slice the real output
  region, then one leading and one trailing newline are trimmed.
- **Both markers are required while a deeper look is still available.** A
  capture window that catches DONE but scrolled past START MUST parse as "not
  found", never a truncated or garbled partial match. A too-small window reads
  as "still running", and the run keeps polling with a larger one.
- **Once the window has stopped growing, DONE alone is the answer.** A run at
  its maximum window whose START is absent MUST take the exit code DONE
  carries, report the output that remained above it, and mark the result
  truncated. The loss of where the output began MUST be disclosed (§0.8).
- DONE is never relaxed. Without a completion there is no exit code and nothing
  separates the capture from a command still running.

#### Why the trailing delimiter

Without it, a digit at the start of the next captured line, such as a prompt
like `12:34 $`, is absorbed into the exit-code digits once newlines are
stripped. `..._0\n12:34 $` would parse as exit code 12 instead of 0.

#### Why DONE alone at the maximum window

Where no deeper look is available (the run is at its maximum window, or it is a
poll, which always asks for the maximum), no later capture can be better.
Refusing a completion legible on screen discards the only answer the protocol
will produce. The exit code is exact because it is read off the completion
marker. A partial output whose payload looks whole is worse than no answer,
which is why the loss is disclosed.

### 6.3 The command MUST be validated up front

An empty or newline-containing command MUST be rejected before any injection.
Rejecting up front also means no partial pane interaction happens.

#### Why

Neither degradation is a timeout, so only an explicit check catches them:

- A newline makes the shell run the fragments as separate commands. Both
  markers still echo and the run succeeds, reporting the exit code of the last
  fragment.
- An empty command is shell-dependent. bash hard-errors (no markers, a genuine
  timeout), but zsh, macOS's default login shell, tolerates it and reports
  success with exit 0.

### 6.4 The capture window grows where the depth can be requested

Long-running output can scroll the sentinel markers off-screen while the
command is still producing output above them.

| Backend | Window |
|---|---|
| tmux | history with an explicit depth. Starts at 200 lines, quadruples on every miss, capped at 10,000. This deliberately does not inherit §5.1's viewport-only default |
| zmx | no scrollback-window primitive, so polling uses plain capture every time. zmx returns scrollback, but its depth is zmx's own, not requestable, with an unknown ceiling. A command scrolling its own sentinel past it can be missed. No workaround |
| herdr | requestable, capped at 1,000 lines by the server |

#### herdr's cap

The server counts its 1,000 lines from the bottom of the grid, visible screen
included, while a depth here is scrollback above the screen. So Olympus adds
the viewport height before asking, and the history available at the cap is
1,000 less the viewport.

A larger request is not refused by the server but silently clamped. Olympus
clamps it too and discloses the clamp (§0.8), rather than asking for a number
it will not get. The growing window works below the cap. Above it, the remedy
tmux offers does not exist.

Above the cap, §6.2's relaxation recovers the run whenever the completion is
still legible, so the cap costs the start of the output rather than the whole
answer.

#### A timeout with no start marker says so

A run that reaches the maximum window and finds no start marker on it MUST say
so in the timeout. A run whose start marker is on screen MUST NOT. A diagnosis
attached to every timeout carries no information.

That message MUST report the observation and name both causes rather than
asserting one.

##### Why both causes

Measured on herdr: an alternate-screen program produces the same capture as
output scrolled past the cap. The start marker is absent and returns when the
program exits. herdr does not track the alternate screen (§13), so nothing can
tell the two apart. Naming one would be a guess, and wrong for every run that
pages its output.

#### Detached polls

The window a detached poll searches is the maximum by default. The cap it will
hit is disclosed against the window actually searched, never against an unset
option (§0.8).

### 6.5 The target pane MUST be running a shell

The sentinel line uses `;` chaining and `$?`, both shell syntax. Pointed at a
pane whose foreground process is not a shell (`cat`, `vim`), the markers never
execute and the run times out, indistinguishable at this layer from a slow
command. A consumer wanting a clearer diagnosis must know independently that
the target runs a shell.

### 6.6 A session killed mid-poll is not-found, on every backend

On zmx this requires care. Mapping any history failure to backend-unavailable
is wrong, because the deterministic `session ... does not exist` stderr is not
the intermittent case that class is for. Match that substring explicitly and
classify it as not-found before falling back.

### 6.7 Detached runs are stateless: the scrollback is the state

A detached run injects once and returns an id. Polling answers `pending` /
`completed` / `died` for a `(target, id)` pair, any number of times,
lock-free.

Nothing durable is written: no registry, no pending-command table, no disk
state. The id is baked into the sentinel markers, and a caller resumes by
re-presenting `(target, id)` so the poll re-scans scrollback for the matching
pair.

**The exit code field MUST be a pointer, omitted unless completed.** Pending and
died MUST NOT populate it, so the payload never carries a fake zero a naive
consumer could read as success. Consumers branch on status first.

#### Consequences that MUST NOT be "fixed"

- **An unknown id and a still-pending command are indistinguishable.** Both read
  as pending until the caller's own timeout. A registry would mean persistent
  state, which is out of scope. The caller bounds how long it waits on an id it
  is not sure is real, as it already bounds a slow command.
- **"Completed then killed" and "died mid-command" are indistinguishable.** If
  the command finishes and the session dies before any poll sees the DONE
  marker, the marker vanishes with the scrollback. `died` is the only honest
  answer.

#### The detached window is one-shot

The detached path's window is a fixed request, not a growing loop. On tmux the
requested depth (default 10,000) passes straight through. If scrollback pushed
the marker beyond it, poll reports pending forever and the remedy is re-polling
with a larger window. On zmx the value is ignored (§6.4).

### 6.8 Polling answers about the command, never about the backend

Two deliberate divergences follow from poll's posture: answer the desired-state
question, never surface backend plumbing.

#### A target that never existed answers `died`, not not-found

Poll's question is about a command, not the existence of a session. From a
read-only vantage point, "the target vanished" and "the target was never real"
are indistinguishable, so both get the same answer.

Starting a detached run against a bad target is different. It touches the
target, so the injection fails loudly and MUST surface not-found normally.

#### A dead tmux server also answers `died`

Listing maps "no server running" to an empty list (§3.3), and poll uses listing
to tell pending from died. A socket whose server was killed reports `died`
instead of the backend-unavailable error every other operation gives. A caller
cannot read `died` as "this session died" versus "the whole backend
disappeared".

### 6.9 Died detection MUST cover a corpse pane, not just a dead session

Poll MUST check the per-session dead flag it already parses. When no completion
marker is found and the session is listed, a dead pane means `died`, not
`pending`. This is a no-op on zmx, which has no corpse concept.

#### Why

With `remain-on-exit`, a dead command's pane becomes a corpse but the session
stays listed. Session-level death detection alone would report pending forever.

### 6.10 Throwaway sessions

Running a command without naming a target creates a throwaway session for that
run and kills it afterwards, on success, failure, and timeout alike. It gets
the default shell, §1.1's sanitized spawn environment, and a name reserved per
§17.1.

**A cleanup failure MUST NOT override the run's own result.** Report it on
stderr and return what the run produced. A leaked throwaway session is a
problem to notice separately, not a reason to hide the answer.

A throwaway run MUST NOT be combinable with detaching, since nothing would be
left to poll. Reject the combination as a usage error.

---

## 7. Verified delivery

Verified delivery sends literal text, then polls the screen until that text is
observed there, resending once before failing.

### 7.1 Normalization

Normalization is UI-tolerant: lowercase, then strip every rune that is not a
Unicode letter or digit (punctuation, whitespace, box-drawing and prompt glyphs
all drop).

#### The 24-character truncation applies to the needle only

It answers how much of the typed text must be seen again for the echo to count.
A line being searched MUST be normalized whole.

##### Why

Truncating both is a common defect. bash's default prompt puts
`user@host:/dir$ ` on the same line as the command and normalizes to more
characters than the cap, so a truncated line ends before the typed text begins.
A verified send then fails after its resend with its own echo on screen. A
multi-line prompt puts the command on a clean second line and hides the defect
in review.

#### The needle is taken from both ends of the text

Either end seen counts. A text of 24 normalized characters or fewer yields one
needle.

##### Why

An input box narrower than the text scrolls to the cursor, at the end. A long
text's first 24 characters are above the box's top edge while its last 24 are
on screen. Looking for the head alone reads that as a dropped delivery, and the
§7.4 resend types the text a second time: the input line holds it twice and
nothing is submitted. Measured with a prompt of about 1,500 characters into a
52-column pane of a full-screen agent client.

### 7.2 Match per line, not across the whole screen

Normalization MUST be applied per line, never to the full multi-line capture as
one blob. The rule is about which text is compared, not about truncating it
(§7.1).

#### Why

A terminal's status or prompt banner routinely fills a line before the line
holding the echoed text. Normalizing the whole screen as one string risks
matching a needle assembled from two unrelated lines.

### 7.3 Also match adjacent line pairs, for wrap tolerance

The matcher MUST check each line individually and each adjacent pair of lines
concatenated. The pair check covers every single-boundary split without
reintroducing whole-screen matching.

The matcher is shared, so every backend behaves identically. tmux does not need
it, since `-J` rejoins the wrap before capture sees it.

#### Why

Per-line matching alone regresses §5.2's wrap problem. On zmx a needle whose
echo straddles the PTY's column width comes back split by a literal newline
that cannot be rejoined, so a per-line-only matcher times out on every straddle.

#### Scope

Pair concatenation covers one wrap boundary. A needle split across two wrap
points needs a pane narrower than the roughly 24-character normalized needle,
and sub-24-column panes are not a supported target.

### 7.3.1 A caller's pattern MUST NOT assume a prompt starts a line

Anchoring a wait on `^` is unreliable against any program that paints by cursor
addressing rather than by printing lines.

Measured: Python 3.13's default REPL, started under load, draws its first
prompt onto the row the shell's echo still occupies, and the pane renders

```
$ /usr/bin/python3 -q>>>
```

The capture is correct: that is what the terminal drew. It is not a defect to
fix in a backend or the matcher. The same program produces a line-anchored
prompt on one run and an appended one on the next, depending on timing.

A caller that needs the stricter assertion should pin the program's behavior
instead of the pattern, for a REPL by selecting its line-oriented mode. Olympus
reports what is on the screen. It does not normalize away where a program put
it.

### 7.4 One resend, two independent budgets

Send, poll for up to one attempt budget, and on a miss resend the same text
once and poll a second, independent budget. Only a miss on that second window
fails.

Worst-case wall time before failure is twice the attempt budget, and the
conformance suite MUST assert that elapsed time so a regression cannot silently
return early on the first miss.

#### Why

The failure guarded is a dropped or coalesced first delivery, not a garbled
second attempt.

### 7.5 A verified send refuses an agent waiting on a person

Before anything is typed, and inside the same lock as the delivery (§11.2), a
verified send reads the target. Where the target's pane holds a known agent
and that agent's manifest reads the capture as `blocked` (§3.7), the send
fails with `AGENT_BLOCKED` (§12) and types nothing.

The same reading is repeated on every capture while the echo is polled for. A
capture read as `blocked` stops the delivery at once, with no resend and no
terminator, and fails with `AGENT_BLOCKED` marked `typed`.

`typed` is what tells the two apart. Without it, a caller that sends again once
the prompt closes types the text a second time, beside the copy the first send
left in the input box.

An atomic send (§4.7) is refused by the same reading before its one write. It
has no echo to poll, so that reading is all it gets.

An agent whose manifest names its input box (`prompt_box_body`) and whose
capture shows no box has something else open over its input (a rewind list, a
model picker, a transcript viewer), or has not drawn the box yet. A verified or
atomic send into it MUST fail with `AGENT_BLOCKED` and type nothing, since an
Enter there answers what is open. A box that goes while the echo is polled for
stops the delivery the way a prompt does: no resend, no terminator, and
`AGENT_BLOCKED` marked `typed`. The box is read off the whole capture, not the
detection tail, since a tall draft can push its top rule out of the tail. Measured on Claude Code 2.1.274, the box is drawn while the agent is idle,
working, holding a paste placeholder and listing slash commands, and is absent
under its rewind list, its model picker and its transcript viewer.

A box can also read as absent when it is drawn wrong. Before either refusal,
where the backend can ask the pane's process to redraw (a herdr server that
advertises `pane_redraw`), Olympus MUST ask once and read the screen again for
up to one second. A box drawn by then is read as if it had been there, and the
send goes on. A box still absent is refused as above. A backend that cannot
ask refuses at once. During the echo poll a redraw is asked for each time the
box reads as absent, so one drawn wrong twice is redrawn twice, and the first
that leaves it absent stops the delivery. On herdr the request shrinks the pane's PTY one row for a moment
and restores it, so the process gets a real size change, and herdr's own
screen is not resized.

Measured on Claude Code 2.1.274: its input line was drawn one row low, over the
box's bottom rule, so only its words overwrote the rule
(`──Reply─with─the─single─word─ok.──`) and the `❯` row was blank. The text was
in its input, and one typed character drew the box again. A send was refused
as typed before any Enter, and every later send as untyped. A signal of the
same size and focus events did not make it redraw. A size change did, with the
input intact.

The state is read off the capture the send is about to type into, never off
the agent listing's status. On a backend that detects agents natively the
listing still names which agent it is.

An answer to a prompt is a keypress the caller chooses (`press`), never a side
effect of sending text.

#### Why

A blocked agent's screen is a permission prompt or a question, and its input
is that prompt's. Measured against a Claude Code pane (2.1.273) in its
default permission mode:

| Sent while the pane showed | Result before this rule |
|---|---|
| A permission prompt, with the sent text already on screen | Verified at once, and the Enter approved the command |
| A question with two options, with the sent text already on screen | Verified at once, and the Enter chose the first option |
| A permission prompt, with text not on screen | Never verified; failed after both budgets with nothing submitted |

The first two are a decision made for a person by a caller that meant to
deliver a message.

The listing's status is not what is read because it lags the screen. On herdr
the native status turned `blocked` about 1.2 s after the prompt was on
screen (measured, polling every 0.5 s), and a send in that window would have
been let through.

#### Scope

- A prompt the manifest does not recognise is not refused, and the send falls
  back to §7.6's rules.
- Only a target of one pane is read for an agent, since nothing says which
  of several panes input lands in. On a backend that detects agents itself,
  the listing's row for that pane names it (§3.7), and an agent in another
  pane of the same session is not on the screen read. Elsewhere the pane's
  process tree or foreground command names it. A target whose agents cannot
  be listed holds none: a shell does not stop taking input because a process
  table could not be read.
- `type`, `paste` and `press` are not refused. They are raw input: a caller
  that presses a key into a prompt has chosen to.

### 7.6 An agent's composer is where its echo is looked for

Where the target's agent draws its input as a box its manifest names
(`prompt_box_body`), the echo MUST be looked for inside that box, and nowhere
else on the screen. The box counts only while the capture is not read as
`blocked` (§7.5).

Everywhere else — a shell, a REPL, an agent whose manifest names no such box,
a capture where no box is drawn — the §7.2 whole-screen match applies.

§7.1's two needles apply inside the box as they do on the screen.

An agent may draw a long paste as a placeholder instead of its text (Claude
Code's `[Pasted text #N]`, Codex's `[Pasted Content N chars]`). A text drawn
that way has nothing to match, so a placeholder that was not there before
typing MUST count as the echo: in the box where one is drawn, and on the whole
screen where it is not. Placeholders are counted in the capture taken before
typing (§7.5). Where that capture failed, they are not counted.

A paste can arrive in pieces, each its own placeholder, so a count that rose
counts only once the next capture shows the same count. On the whole screen
only a placeholder naming the text's own length counts (Codex's `[Pasted
Content N chars]`), since an older one can scroll away and a transcript can
quote one.

#### Why

An agent's transcript repeats what was typed at it earlier. A whole-screen
match counts that repetition as the echo of a text that has not arrived yet,
and submits whatever is in the input line. It also counts text that happens
to appear in a prompt the agent is showing: a question's option, a command
awaiting approval.

Measured against the same pane, text sent unsubmitted was found inside the
box in every case the whole-screen match already passed:

| Case | Inside the box |
|---|---|
| Typed while the agent was working | Head and tail |
| One line of 1,646 characters | Tail; the head had scrolled above the box |
| Three lines | Head and tail |
| Sixty-two lines | Tail; the box shows its last lines, with no paste placeholder |
| One line of about 3,000 characters | Neither; the box shows `[Pasted text #N]` placeholders, and before the rule the send timed out and its resend left the text twice |

A question fills the same region, and the manifest's rule for a live box
matches the line of its highlighted option. Measured, the box read for a
two-option question held the question and both options. That is why the box
counts only while §7.5's reading is not `blocked`, rather than on its own.

---

## 8. Attach

Attach hands off to the backend's own attach client inside a PTY Olympus owns,
streaming stdio both ways until the child exits.

### 8.1 The presence gate is mandatory on zmx

Attach MUST probe presence first and fail closed: on probe errors and "no
server" as well as on confirmed absence.

#### Why

`zmx attach <name>` on an absent name upserts it, spawning a fresh unrelated
shell under that name. tmux fails cleanly in the same situation. Without the
gate, a race between a session's death and an attach fabricates a phantom
session that looks legitimate.

### 8.2 Interactive attach owns the outer terminal's discipline

With a TTY stdin, attach MUST put it into raw mode for the attach's lifetime and
undo everything on every exit path: normal detach, error teardown,
supersession, and termination by signal (`SIGTERM`, `SIGHUP`).

The signal paths are easy to miss. Deferred cleanup covers the common ones, but
a process killed by an unhandled `SIGTERM` runs no defers, leaving the
operator's terminal in raw mode with mouse reporting on.

#### Raw mode makes keystroke forwarding real

Without it the outer line discipline interprets keys itself, turning Ctrl+C
into SIGINT against Olympus (a spurious detach) instead of a `0x03` byte for the
inner shell. With `ISIG` cleared, Ctrl+C, Ctrl+Z and Ctrl+\ all forward inward,
matching `tmux attach`. Detaching is the inner backend's job (tmux `C-b d`,
zmx's own key), not the outer terminal's.

#### Exit MUST restore two layers

1. The saved termios is reinstated.
2. A reset sequence is written to stdout: mouse reporting off
   (`?1006l ?1003l ?1002l ?1000l`), focus reporting off (`?1004l`), bracketed
   paste off (`?2004l`), cursor shown (`?25h`).

Without (2), an inner application that enabled mouse or focus reporting through
the PTY leaves the outer terminal emitting `\e[<...M` / `\e[I` junk into the
next shell prompt after detach.

The restore MUST be exactly-once across all exit paths. A failure to enter raw
mode MUST degrade to cooked-mode behavior rather than abort the attach.

#### Piped stdin is untouched

A non-TTY stdin gets no raw mode and no reset bytes injected into a stream a
programmatic consumer parses. That consumer owns its own client-side terminal
state.

### 8.3 Resize protocol

- **The first size** MUST be on the PTY before the client starts: the caller's
  terminal size on a TTY stdin, else the size the caller gave.
- **TTY stdin**: `SIGWINCH` is forwarded to the PTY, synced immediately on
  attach, then on every signal.
- **Piped stdin**: no `SIGWINCH` exists, so an in-band control line on stdin
  resizes the PTY (below).

#### Why the first size comes first

A client that reads its size as it starts sees whatever the PTY holds at that
instant. A PTY sized a moment after the start reads 0x0, and herdr's client
exits on it ("terminal reported a zero-sized grid"). Measured under load as
about one attach in 120.

#### In-band controls

Three controls ride the stdin stream. All are read on a TTY stdin as well: a
consumer driving the attach under a PTY of its own has that stream and no
other, and nothing a person types spells one.

| Control | Effect |
|---|---|
| `\x1b]olympus;resize;<cols>;<rows>\x07` | resizes the PTY |
| `\x1b]olympus;go;<target>\x07` | moves the live client onto another target on its server |
| `\x1b]olympus;focus;<pane>\x07` | focuses a pane the way a click on it does |

Controls MUST be matched byte-for-byte and stripped before forwarding, never
written into the session. Malformed payloads MUST be ignored: a bad control
sequence must not kill the session.

Every control in a read is taken, one after another. A control cut by the end
of a read is held for its end, and so are its opening bytes where a read that
filled the buffer ends inside them. A read that did not fill it is never held
on a guess: a lone Escape is the opening byte of a control too, and holding it
would stall the key until the next one. An unterminated run past a target's
length is ordinary bytes.

#### `go`

It applies to a client that can be moved: a bare herdr session client (§8.10).
It runs in the stream's order: bytes before it reach the session before the
move, bytes after it wait until the client is on the new target. Nothing is
forwarded before the client has settled on its first target.

On an attach that cannot be moved the control is dropped and stderr says so.
The bytes around it still reach the session. A go that fails ends the attach
with its error (§8.10).

#### `focus`

It puts the client on the pane's tab with that pane focused and the tab not
zoomed, in the stream's order as a go is. A target that is not a pane ends the
attach as `USAGE`.

Where the attach cannot focus a pane this way (an attach that cannot be moved,
or a herdr server without `client_view_pane`) the control is dropped and stderr
says so. Any other failure ends the attach as a failed go does (§8.10).

### 8.4 Attach supersedes prior clients by default

A new attach takes over from prior clients on every backend, mirroring
`tmux attach -d`. Opting out is explicit.

| Backend | Supersession |
|---|---|
| tmux | `-d` in the attach argv; tmux's client detaches the prior one |
| zmx | a guard and a sweep (§8.5) |
| meja | none |
| herdr | the backend's own takeover |

#### tmux

`-d` is an argv transform at the door layer, not a backend interface method.
Adding it to the interface would leak a tmux-specific flag into a contract zmx
has no equivalent for.

#### meja

There is no displacement of any kind. A prior client stays attached, and meja
sizes the session to its smallest client, so the new attach may reshape what
everyone else sees. An attach that asked to supersede succeeds and MUST carry a
notice saying it did not. Silence would read as a supersession that happened.

#### herdr

herdr allows one attached client per terminal and refuses a second unless it
asks to take over, so §8.5's guard and sweep have no work here. Measured: a
second attach without takeover is refused with `terminal <id> already has an
attached client; retry with --takeover`. With it, the prior client is detached
cleanly and told `terminal attach taken over`.

One consequence MUST be documented rather than hidden. The refusal is the
server's, so a non-superseding attach onto an occupied terminal fails inside the
client, after the PTY is running, rather than as a conflict Olympus raises
before spawning. herdr reports no per-terminal client count, so there is
nothing to check beforehand.

### 8.5 zmx supersession needs both a guard and a sweep

zmx co-attaches: a second attach takes the session to two clients with the
first still alive and both rendering.

#### The guard

The guard governs Olympus-vs-Olympus. One pidfile per (directory-hash,
session), whose `flock`, not its content, is the exclusivity mechanism. The pid
is read only to know whom to signal.

- No holder, or a stale pidfile, is reclaimed silently. Stale means a dead pid,
  or a flock that is acquirable regardless of content (a holder that crashed
  between writing the pidfile and exiting).
- Without steal, a live holder is an immediate conflict error, before the PTY is
  spawned.
- With steal, the holder's pid gets `SIGUSR1`, then the stealer polls every
  50ms for up to 3s for the flock to free. If it never frees (holder hung,
  signal lost) the acquisition gives up with the same conflict shape rather than
  blocking forever.

#### Winning the flock is not proof of holding the slot

Every acquisition site MUST re-verify, immediately after winning the flock, that
the fd's `(st_dev, st_ino)` still matches a fresh `stat` of the path. A
mismatch, or a path that is gone, MUST restart the whole acquisition, reopening
the path so it lands on the inode actually there.

##### Why

`unlink` detaches a name from an inode without invalidating an fd a waiter
already holds. A waiter blocked on an fd opened before a concurrent release can
win a lock on the now-nameless inode at the moment a third acquisition creates a
new inode at the same path and succeeds too. Two callers then both believe they
hold the slot.

#### The sweep

The sweep covers what the guard cannot see: a raw `zmx attach` an operator
started from their own terminal has no pidfile, no flock, and no signal handler.

The primitive is undocumented and not discoverable from `zmx help`:

- `zmx detach` is listed as taking no arguments ("Detach all clients").
- A session name passed positionally is accepted and ignored.
- Run bare from outside a session it is a no-op that exits 0.
- It resolves its target from the ambient `ZMX_SESSION`, the variable zmx sets
  inside a session's own shell. Setting `ZMX_SESSION` explicitly from outside
  aims it at any session. It detaches every client and leaves the session
  alive.

#### Combining the two

- **Order is guard-then-sweep.** The signal lets a prior Olympus holder tear
  its own PTY down cleanly, restoring the outer terminal per §8.2. The sweep
  then covers what the guard cannot see. The reverse would pull the client out
  from under a holder that then cleans up a PTY whose client is already gone.
- **The sweep is best-effort and loud on failure.** A failed sweep leaves prior
  clients co-attached, degraded but usable, which is no reason to refuse the
  attach. But it MUST print to stderr: a silent no-op is indistinguishable from
  a successful steal, the failure this exists to remove.
- **Residual race, accepted.** The sweep is point-in-time, so a client attaching
  between the sweep and Olympus's own attach survives it. Closing that would
  need a zmx-side exclusive-attach primitive that does not exist.

### 8.6 Signalling and its accepted risks

The supersession handler's message MUST be generic ("detached: superseded"),
never "superseded by pid N". POSIX signal delivery carries no sender pid
portably, so the superseded side cannot name who stole from it. The "by pid N"
framing belongs on the stealer's side, which read the holder's pid from the
pidfile.

#### A holder with no handler

It falls to SIGUSR1's POSIX default disposition, termination. That is an
accepted risk, not a mechanism to rely on: it was observed not to terminate a
Go process on a GitHub Actions runner, while terminating the same binary on
macOS and in a plain container. A steal from such a holder is best-effort, and
the bounded wait (§17.3) keeps it from blocking forever.

#### The handler's timing

The handler runs on its own goroutine, installed before the PTY is spawned. It
can fire before the PTY exists, mid-stream, or after the attach tore it down on
child exit. A mutex-guarded PTY handle, populated when the PTY starts and closed
by the handler only when non-nil, makes each of those a safe no-op rather than a
nil-pointer panic or a double-close.

#### Accepted risk: pid recycling

Between reading the pid and signalling it, the OS could reap the holder and
recycle its pid onto an unrelated process. This is unfixable without a
handle-based signal primitive (`pidfd`), which Go does not expose. The window is
narrow, but the blast radius varies: a Go process without the handler treats an
un-notified `SIGUSR1` as non-fatal, while a non-Go process hits the default
disposition, termination. Accepted, not fixed.

### 8.7 A viewer role on zmx MUST drop resize as well as input

A read-only viewer on zmx MUST drop resize calls in addition to keystrokes. This
is a stronger gate than tmux needs.

#### Why

zmx has one PTY per session shared by every client, unlike tmux's per-view
grouped sessions. A viewer resize on zmx physically resizes the driver's
terminal.

#### meja and herdr refuse a viewer

meja has no read-only client, so there is nothing to make passive. A viewer
attach is refused as `UNSUPPORTED` (§12) rather than downgraded to a
controller. A watcher who believes they cannot type, and can, will eventually
type into somebody else's session.

herdr's read-only stream is not a terminal client. It emits JSON frames for a
program to decode, not a rendering for a human to sit in, so there is nothing to
hand a PTY. A viewer attach is `UNSUPPORTED` there too. The stream is what §5.6's
follow is built on, but following and attaching are different operations.

### 8.8 A spontaneous attach exit must still reap its view session

A view session's cleanup MUST NOT depend solely on an explicit close from the
consumer. The attach client can exit on its own (base session died, process
killed) without any close running. The exit handler MUST independently reap the
per-view session, or it leaks forever.

### 8.9 A bare attach on tmux is an attach onto a throwaway view

A bare attach shows a session as a plain pane with no chrome. zmx and meja have
neither a chrome-drawing client nor views, so a bare attach is `UNSUPPORTED`
there (§12).

#### herdr

A bare attach is the session client under a configuration file written for the
one attach:

- Its chrome is hidden.
- Every key that leaves the workspace is unbound: tabs, workspaces, worktrees,
  the sidebar, the picker.
- What the operator does inside the workspace stays theirs. The prefix is the
  one their own configuration names (§13.3), and the pane keys behind it
  (split, close pane, zoom, resize, focus between panes) keep herdr's bindings.
- A divider is drawn between split panes and nothing around a lone one
  (`pane_borders = true`, the legacy spelling of 0.9.0's `"auto"`, which an
  older herdr still parses).

#### tmux

A bare attach is a view (§9). A grouped session is already bare by construction:
no status bar, no prefix, an inert key table (§9.3). A bare attach MUST create a
view onto the session, attach the client to the view rather than the session,
and reap the view when the attach ends, on every exit path (§8.8).

Attaching the session itself with `status off` would reconfigure it for every
other client of it.

#### A window target

The target MAY name a window: `<session>:<window>`, by index or name. A grouped
session keeps its own current window (§9.2, §9.4), so the view is pinned to
that window while the base and every sibling view keep showing their own.

The split is at the first colon. Olympus refuses a colon in a tmux session
name (§2.11), so a session name never contains one, and a window name may. The
window MUST be validated against the base before the view exists (§9.4). A
window the base does not have is `SESSION_NOT_FOUND` with nothing created.

#### Naming the view and turning mouse off

The caller MAY name the view and MAY turn its mouse reporting off.

The name MUST carry the reserved prefix (§17.1) or it is `USAGE` before a view
exists. An unprefixed view would be invisible to enumeration and every sweep.
Either option on a backend whose bare attach makes no view is `USAGE`, not
ignored: a caller naming a view it then drives would otherwise drive nothing.

##### Why

An attach is interactive and has no channel to report a generated name back. A
consumer that scrolls the view from outside (§9.2) or focuses a pane in it
(§9.6) while the attach runs has to address it, and a name it chose is the only
way. Mouse reporting off is for a client that keeps its own text selection and
drives the scroll through §9.2.

#### What a bare view cannot promise

The active pane within a window is the window's, shared with the base. A bare
view of a multi-pane window shows the base's active pane, and Olympus never
selects a pane in a view (§9.4).

#### The rest of attach is unchanged

The view is a session, so the ordinary argv applies. A viewer role attaches it
read-only (§8.7), and the supersede flag is harmless on a session no client has
reached. Opting out of supersession has nothing to act on and is accepted
rather than refused: a bare attach never displaces the base's own clients.

### 8.10 A session-client attach on herdr is steered onto its target

herdr has two clients, and they are different programs:

| Client | Selected by | What it is |
|---|---|---|
| raw pane stream, `herdr terminal attach` | default | a plain terminal on the pane the target resolves to (§3.6), no chrome, no selection |
| session client | `--client`, or `--bare`, which implies it | herdr's own application: sidebar, tabs, mouse selection, scrollback, copy |

The session client takes no target of its own, so Olympus MUST put it on the
target. How depends on what the server supports:

| Server | Method | Section |
|---|---|---|
| without `client_view_focus`, `--client` | server steering | below |
| without `client_view_focus`, `--bare` | walk | "The walk" |
| with `client_view_focus` | per-client view requests | "A server that moves one client's view" |
| also with `client_view_ack` | per-client view requests, confirmed by acknowledgement | "A server that reports when a client applied its view" |
| also with `client_view_pane` | pane focus for one client | "A pane focused for one client" |

#### Server steering

Steering runs level by level, in the order the client will read them:

| target | steering |
|---|---|
| workspace | `workspace focus <ws>`; then, if its active tab is zoomed, `pane zoom --pane <focused> --off` |
| tab | `workspace focus <ws>`, then `tab focus <tab>`; then the same zoom-out if the tab is zoomed |
| pane | `workspace focus <ws>`, `tab focus <tab>`, then `pane zoom --pane <pane> --on` |

Below herdr 0.9.0 every client shows the server's one focus. The steering runs
before the spawn, and the client comes up showing it.

From 0.9.0 a client that moved between workspaces on its own keeps a view of
its own, but `workspace focus` still moves every client, those included. On the
server there is no way to put two clients on two workspaces: steering it for a
second tab moved the first. So a bare client is walked, and a `--client` attach
without `--bare` is still steered on the server and moves every other client
with it, since it has no keys the backend can count on.

These were measured with two clients on one server, a marker typed into each
and read back from each workspace's pane. Window titles were not usable for
this, since a title repaints for the focused client alone.

##### Why the pane step is a zoom

herdr has no server pane-focus request, and a zoom both focuses the pane and
shows it alone, which is what a caller attaching one pane of a split tab means.
Measured: zooming a pane that was not focused answers `focus_changed: true`,
and zooming into a tab already zoomed on another pane reports `already_zoomed`
and still moves focus.

##### Why workspace and tab rows zoom out

The zoom outlives the attach. Without the zoom-out, a second attach onto a
two-pane tab showed only the pane an earlier pane attach had zoomed, with
nothing the caller could target to bring the other back. The zoom state is read
from the tab's layout row, the same row that names its focused pane. A tab that
is not zoomed gets no extra request.

##### Steering is a server call and is not undone

The steering is server requests over the socket, so a session-client attach is
a server call. It is not undone when the client exits: the server keeps the
focus and zoom a human would have left the same way.

#### The walk

On a server without `client_view_focus`, a bare client comes up on the server's
focus (measured) and is walked to its target with its own workspace keys.

##### Waiting until the client reads keys

The client asks the terminal for the kitty keyboard protocol (`CSI > 7 u`) as
it starts. The backend names that sequence as the attachment's `SettleAfter`,
and the engine watches the output for it and waits 400ms after it before
walking.

A key written with the push is dropped. A key written a stretch after is read.
Idle that stretch is short, but under load (several clients attaching to one
server) it is longer: the two-client e2e failed 6 of 6 at 250ms and passed 6 of
6 at 400ms.

For a client that names no mark, the engine waits for the first quiet stretch
after output begins, capped at two seconds. The mark exists because a client on
a workspace whose pane never stops painting never goes quiet, and waited out the
cap (measured: 2.1s to the walked frame, against 0.2s by the mark).

##### The keys

The backend reads the workspaces in the order of their numbers and writes the
client's next- or previous-workspace key, the shorter way round the ring, once
per step with a gap between presses. Two written at once were read as one.

The bare configuration binds those keys to F17 and F18, keys a terminal almost
never sends. They are written in the kitty spelling the client asked for; the
legacy `CSI 31 ~` went unread.

##### A press is read when the server's focus moves

A press MUST be taken as read only once the client has painted a window title
AND the server's focus is on the next workspace. Where the focus was already
there before the press, it MUST also stay there for a quarter of a second. A
press whose focus is still on the workspace the client is on after a second and
a half was not read, and is made again, twice in all, before the walk fails. A
press whose focus is anywhere else by then fails the walk without a second
press. The frame end the walk waits for is the one after the title of the press
that was read.

###### Why

A walked client's move is a `workspace focus` it sends the server, so the
server's focus follows it (measured on 0.9.0 and 0.9.1). The title alone was
the signal before herdr 0.9.1. From 0.9.1 the client counts a step from the
workspace its own last snapshot names, and one whose snapshot had not caught up
focused the workspace it was already on, painted that workspace's title, and
stayed. The walk took the title as the press read, and what was typed next
landed on the workspace the client had not left: the two-client e2e failed 2 of
3 on herdr 0.9.1, passed 4 of 4 on 0.9.0, and passed 8 of 8 on 0.9.1 with the
focus confirmed.

That stale press puts the focus on the workspace the client is on, which a
press read never does. A focus on a third workspace is somebody else's request,
and it says nothing about where the client went: a second press there could
take a client that did move one step too far, and a client on the wrong
workspace is worse than a failed attach. Where the focus was on the next
workspace already, a stale press is seen only by the focus leaving it.

A tab or pane target is then steered on the server for its tab and zoom. Those
are the workspace's own state and move no client on another workspace.

The backend hands the walk to the engine as the attachment's `Settle`, run with
the client's own input. A `Settle` that fails ends the attach with its error: a
client left on the wrong workspace is worse than none. A client that exits
mid-walk ends the `Settle` too: its context is cancelled, and the attachment's
cleanup runs only once it has returned, since that cleanup drops the walk lock
and a walk still steering after it would move the focus under another attach.

##### Steps are counted at build, under a lock

Where the client comes up is read when the attach is built, and the steps are
counted then, not when the walk runs.

Bare attaches onto one server are built one at a time. A lock per server, in
the reserved lock directory (§17.1), is held from the moment the focus is read
to a beat after the last key of the walk, or to the attachment's cleanup where
the client ended before it walked. A second attach waits up to fifteen seconds
and is then refused as a conflict. A consumer opening several tabs at once pays
the walks in a row, each under a second, and every one lands.

###### Why

A walk moves the server's focus (the focus follows whichever client last moved,
measured). A second bare client spawned a beat after the first came up on the
focus of one moment and walked from the focus of another, landing one workspace
off.

The `focus` verb (§13) does not take this lock and still steers the server
directly, which from 0.9.0 moves every client.

##### Moving a walked client: `go`

A bare client, once walked, can be moved. The attachment's `Go`, asked for with
the in-band `go` control (§8.3), walks it to another target on the same server
the same way:

- The steps are counted from where this backend last left it: the attach's
  target, then each go's; where a walk is cut short, the workspace of the last
  key pressed.
- It runs under the walk lock, with tab and zoom steered on the server after.

That is how a consumer with one client per server switches workspaces without a
spawn. The probe follows the client, so the attach ends with the target it is
on, not the one it was made for.

A go onto a target that does not exist, or a walk that fails, ends the attach
with its error. A caller that believes the client moved is worse off than one
told it did not.

##### Every press is confirmed

Every press of a walk, the first one's and a go's alike, is confirmed by the
client rather than assumed from a delay:

1. Before the press, the engine is asked to watch the output for a window title
   (`Expect`). The client paints its title as it lands on a workspace, on its
   own switches every time (measured).
2. If no title has come within 1.5 seconds, the press is made once more, then
   given up as the walk's error.
3. The switch's synchronized frame (DEC 2026) is waited for to its end, and a
   beat after it, before anything else is written.

Any title counts. For a workspace nobody named, herdr names the list's row from
the directory its process is in and the title from the directory its terminal
last reported, so a Claude Code pane listed under its project directory painted `<host>: ~`. A walk
matching the label gave up on every press that landed.

###### Why the frame, not the title alone

The title comes at the start of the switch, and a marker typed between the
title and the end of the frame never echoed. Against a consumer's e2e under
load: with a fixed delay after the press, one press in three went unread and the
next marker landed in the workspace the client was still on. With the title
alone, the marker was lost one run in eight. With the frame, eight of eight
landed.

##### What a walk cannot hold against

See §8.11. In short: a `workspace focus` or `tab focus` on the server, from any
CLI, moves every client and this backend cannot see it. A workspace created or
closed during a walk shifts the ring, since the lock serialises only this
backend's walks.

A client that came up on its target with no walk does not follow the server's
focus afterwards: a marker typed into one on `w14` landed in `w14` after another
client walked to `w0` (measured).

#### A server that moves one client's view is not walked

A herdr server that answers `ping` with `capabilities.client_view_focus: true`
has these:

| Interface | Effect |
|---|---|
| `--workspace <workspace id>` (client launch flag) | starts that client on the workspace without moving the server's focus or any other client |
| `--client-tag <tag>` (client launch flag) | names the client |
| `client.list` | reports each client's tag, workspace and tab |
| `client.view.focus` | moves the client a tag names, and no other, onto a workspace and optionally a tab |

Whether the server has them is asked, never read from the version: a build with
them reports the same version as one without. The capability is read once per
backend handle and forgotten when that handle starts or stops a server. A failed
ask is not kept.

A server that does not advertise it is walked as above and never handed the
flags, since a herdr without them refuses to launch.

A request that moves the server's focus (`workspace focus`, `tab focus`, and
from herdr 0.9.1 `agent focus` and `pane move --focus`) moves every client the
server holds. The fork's builds before `0.9.1+agm.1` moved a tagged client with
the rest. From `0.9.1+agm.1` a tagged client stays where it is, moved only by
`client.view.focus`. Olympus sends no such request for a bare attach on a
server with the view, so the difference reaches a caller only through somebody
else's request.

The ping and the client requests have no CLI verb, so they go over the API
socket directly, one JSON line each way on a connection of their own. Everything
else still goes through the CLI.

##### The bare attach on such a server

The session client with the operator's configuration is steered as before. A
bare attach runs:

| step | what runs |
|---|---|
| build | the zoom steps of the steering table, and nothing else on the server: no `workspace focus`, no `tab focus`, no walk lock |
| spawn | the client with `--workspace <ws> --client-tag <tag>`: the caller's tag where the attach names one (§13.5), else `olympus-client-<16 hex>` (§17.1), drawn per attach |
| settle | wait for the tag in `client.list`; the zoom steps; then, if the client is not on the target's workspace (and, for a tab or pane target, its tab), `client.view.focus` with `workspace_id` and, for a tab or pane target, `tab_id`; then the confirmation below |
| go | resolve the target; the same as settle, from where `client.list` has the client |
| probe | `client.list` for the tag, then the presence of the target the backend last put the client on |

A client already where the target is (launched onto its workspace, or a go onto
where it is) is not moved again. No key is pressed and no window title is
waited for.

##### Confirmation by frame

`client.view.focus` is confirmed by its answer: the client it returns must be on
the workspace, and the tab where one was asked, or the settle or go fails.

The answer is not enough to forward what was typed after a go. So the end of
the repaint's synchronized frame is waited for, bounded, and a beat after it,
before the call returns. A client that paints nothing ends the attach with an
error.

###### Why

The client addresses its input to the pane it believes it shows. The server
drops input for a pane the client no longer views until the repaint that tells
the client where it is has reached it. Measured: a marker written straight
after the answer never echoed, and landed once the frame was waited for.

##### The probe follows the client

The server moves a client for two reasons: the workspace it showed closed, or
somebody moved it by its id. The target the backend last put it on tells them
apart. Gone, the attach ends. Still there, the probe takes the workspace
`client.list` reports as where the client is.

Measured with two bare clients on one server, the case the walk could not hold:
each of twelve alternating goes across three workspaces landed where asked, the
other client stayed put, the server's focus did not move, and a marker typed
after each go landed in the workspace the go took the client to.

The walk failed there because herdr paints the foreground client the title of
the server's focus and skips a title it has already sent. A press that landed
painted nothing, was made again, and every later go landed one workspace off.

#### A server that reports when a client applied its view

A herdr server that also answers `ping` with `capabilities.client_view_ack:
true` (asked and kept with `client_view_focus`, never read from the version)
adds:

| Interface | Effect |
|---|---|
| `client.list` field `snapshot_acks` | whether the client acknowledges the snapshots it applies |
| `client.list` fields `pane_id`, `zoomed` | the focused pane of the tab it shows, and whether that tab is zoomed |
| `client.list` field `view_applied` | whether it has acknowledged a snapshot carrying its current view (workspace, tab, focused pane, zoom), the snapshot it routes input through |
| `client.view.wait` | answers once the client has applied its current view |
| `client.view.focus` with `wait: true` | moves the client and answers once it has applied the new view |

Where the server advertises it and the client's row says `snapshot_acks: true`,
the settle and the go run the zoom steps and then one request with `timeout_ms`
5000 (herdr's own default):

- `client.view.focus` with `wait: true` where the client is not on the target's
  workspace and tab;
- `client.view.wait` where it is: the first placement of a client launched onto
  its target, and a zoom that moved the focus of the tab the client shows.

Nothing the client paints is watched.

The client the answer carries must be applied, on the target's workspace, on
its tab for a tab or pane target, and for a pane target have that pane focused
and its tab zoomed as the zoom step's own answer left it. A lone pane is not
zoomed: herdr answers `single_pane` (measured). Anything else fails the settle
or go.

herdr's `timeout` fails it as `TIMEOUT`, saying the client did not apply its
view. What was typed after the go is not forwarded into a pane the client may
not address. The request's own deadline sits past the server's, so that answer
is herdr's rather than a socket that stopped answering.

A server without the capability, or a client that does not acknowledge
snapshots, takes the frame confirmation unchanged.

#### A pane focused for one client, without a zoom

A herdr server that also answers `ping` with `capabilities.client_view_pane:
true` takes `pane_id` in `client.view.focus`. The client moves onto that pane's
tab, the tab's focused pane becomes that one, and the tab's zoom is left as it
was.

The `focus` control (§8.3) uses it and nothing else. The server's own
`pane focus` moves the server's focus, which is every other client's too.

The sequence:

1. A zoom on the tab is taken off first with `pane zoom --off`, before the view
   moves, for the reason the zoom steps run first on a go (below).
2. The call carries `wait: true` and `timeout_ms` 5000 where the server
   advertises `client_view_ack` and the client acknowledges snapshots.
   Otherwise the end of the move's synchronized frame is waited for, as for a
   go.
3. The client the answer carries must be on the pane's workspace and tab, with
   that pane focused, not zoomed, and applied where it was waited for. Anything
   else fails the focus.

Afterwards the attach is on the pane's tab, so a pane closed later leaves the
client where it is rather than ending the attach. A server without
`client_view_pane` is `UNSUPPORTED`, which the control drops.

#### Zoom steps run before the view moves

On both confirmations the zoom steps run before the view moves, and at build
before the client exists.

Where the client already shows the target's tab there is no view change to
carry the zoom. A zoom that moves that tab's focus is followed by the same frame
wait. One that leaves the focus where it was (the pane already focused, or a
zoom-out) needs none. On a server that acknowledges the view, the one request
after the zoom steps confirms zoom and move together, since the view it waits
for includes the focused pane and the zoom.

##### Why

The client addresses its input to the pane its own copy of the tab has focused,
and herdr takes input for a zoomed tab from its focused pane alone. A zoom that
moves the tab's focus is typed past until the client has it (measured: a marker
typed straight after a zoom onto the second pane of a split never echoed).

Zoomed first, the state the view change sends already has the pane focused, and
a client launched onto a zoomed tab gets it in its first state. Zoomed after the
view moved, the zoom needed a frame wait of its own, and that wait was met by a
frame the view change painted late, in 17 of 20 goes onto a pane in another tab
under load. The marker typed after the go was dropped in 4 of them.

##### Why the zoom steps stay at all

A zoom is the tab's own state, and there is no per-client zoom. A pane target
still means that pane alone, and a workspace or tab target still means the split
an earlier pane attach left zoomed.

A zoom focuses its pane, and herdr moves its own focus with it (measured: a go
onto a pane in `w3` moved the server's focus from `w1` to `w3`). No client moves
with that focus on such a server (the other client stayed where it was), so
nothing here depends on it.

A workspace target names no tab, and the client shows the tab it last showed in
that workspace, as a person switching to it would see.

#### Which client is spawned

It depends on how the server was selected (§13.2):

- **By name** (`--server <name>`): the server is one of herdr's named sessions,
  and its client is `herdr session attach <name>`, resolved under the operator's
  configuration directory. That client needs the name, not the socket.
- **By path** (Olympus's own default socket, or `--socket-path` onto a headless
  server): there is no named session. The client is plain `herdr` with the
  socket override, measured to attach the server on that socket rather than the
  operator's default. A server Olympus started gets Olympus's own configuration
  directory, the one it was booted against. A server Olympus found keeps the
  operator's (§17.5).

`--bare` overrides the configuration file with the stripped one (§8.9) without
moving the configuration directory, so the client renders as a plain pane and
still reaches the same server.

The session client has no viewer role and no co-attach control (§8.7, §8.4). A
viewer attach is refused, and `--keep-others` is reported as unhonored rather
than dropped. A target that does not exist is not-found before any client is
spawned, the same gate as §8.1.

#### A session-client attach MUST end when its target ceases to exist

While the client runs, Olympus polls the target's presence (§3.5, by resolved
id, every half second). On `absent` it:

1. says `detached: the target is gone` on the narration channel;
2. sends the client SIGTERM;
3. sends SIGKILL after a short grace.

The attach then exits `0`. Olympus ended the client, so the status is Olympus's,
and the attach did what was asked until its subject ended. §12.1's handoff to
the client's own status applies only to a client that exited by itself.

An `error` answer is skipped, not treated as gone. A socket hiccup must not end
a live terminal, and a server that has genuinely gone away ends the client on
its own.

##### Why

The client is attached to the whole session, not to the target, so it does not
end when the target does. Measured: `exit` in a workspace's only pane closes the
pane and the workspace, and herdr moves the client onto whatever it focuses
next, still running. A caller that closes something when the attach returns is
left showing a workspace it never asked for.

##### Where it is not needed

The raw pane attach exits by itself when the pane's terminal closes. The tmux
bare attach does too: its view shares the base's windows, so the base's last
pane exiting destroys the view and the client with it. A `kill-session` of the
base leaves the view standing (§9.2).

#### Steering without attaching: `focus`

`focus <target>` runs the server steering table above and spawns nothing. On a
server without per-client views, every session client shows the server's one
focus, so two clients steered onto two targets both show whichever was steered
last. A caller holding one client per tab re-steers whenever it brings a client
to the front.

The target reaches the backend as given, since the point is precision below the
session, which §10.1's resolution would discard. Presence is gated through the
resolved session first.

| Backend | `focus` |
|---|---|
| herdr | runs the steering table |
| tmux | `<session>:<window>` selects the window; a pane id selects its window and then the pane; a bare session has nothing to steer and is accepted |
| zmx, meja | `UNSUPPORTED`: sessions are one pane |

tmux has the need in its own vocabulary: clients attached to one plain session
share its current window and pane. A view (§9) is what gives a client its own.
`focus` (§13) is the capability to probe.

#### No `<server>/<target>` grammar

There is deliberately no `<server>/<target>` target grammar. The server is
`--server`, the same option every other verb takes. A second spelling inside the
target would be a second contract to keep in step.

---

### 8.11 What the bare client cannot hold against

On a server without `client_view_focus`, three limits of a bare herdr client
come from herdr's API rather than from the walk:

| Limit | Detail |
|---|---|
| A server-side focus moves every client | `herdr workspace focus`, `herdr tab focus` and `herdr agent focus`, from any CLI or agent, and Olympus's own `focus` verb (§13), move every attached client, this one included, and nothing reports it. Such a server has no request that names a client and no way to read which workspace a client is on. A client dragged this way shows the wrong workspace until its next walk |
| The ring can shift under a walk | The steps are counted from the workspaces as they stood when the walk began. A workspace created or closed meanwhile moves the ring under the keys. The walk lock serialises only Olympus's own walks on a server |
| A walk is bounded by a lock and a press timeout | Bare attaches onto one server are built one at a time (§8.10). A second waits up to fifteen seconds and is then refused as a conflict. Each press waits 1.5 seconds for the client's title and is made once more. A client that never answers ends the attach with an error |

#### On a server with `client_view_focus`

A bare client there is not walked (§8.10). The second and third limits go with
the walk: there is no ring to shift, and no lock or press timeout is taken.

The first is narrower. herdr documents that a `workspace focus` still moves
every client, but `client.list` then reports where the client went, so a go puts
it back from wherever it is and the probe follows it rather than guessing.

On a server without the capability, the walk is the only way in, and it is
confirmed press by press so that what it cannot see is at least not guessed.

#### Without `client_view_ack`, nothing confirms the client has what it was sent

herdr reports no sign that a client applied a view or focus change.
`client.list` gives a client's id, tag, workspace and tab as the server holds
them, and the client acknowledges no state. The end of a frame is the only sign,
and a late frame the client painted for where it was before ends the wait as
well. Input forwarded then is addressed to a pane the client no longer shows,
and herdr drops it.

Measured with the zoom made first: a frame the zoom made the client paint for
its old view ended the view change's wait before its answer arrived, and the
marker typed after that go was dropped. That go failed in 3 of 60 runs under
load and none of 60 without. With the zoom made after, it failed in 3 of 20
under the same load.

#### With `client_view_ack`, it is closed

For a client whose row says `snapshot_acks: true`, the server answers only once
the client has acknowledged a snapshot carrying its view, focused pane and zoom
included (§8.10), so no frame is read as the sign.

Measured on ten cores under fourteen busy loops: sixteen goes in a row between
the panes of two split tabs, each with a marker typed straight after it (in the
same tab and across workspaces), thirty runs each. By the frame, 3 runs dropped
a marker, each on a go across workspaces. By the acknowledgement, none did.

What remains is a bound, not a guess: a client that has not applied its view
within five seconds fails the go as a timeout.

## 9. Views

A view is a read-only grouped session over a base. Views are **tmux only**. zmx
has no grouped-session concept and MUST return an unsupported-class error rather
than emulating one badly.

### 9.1 Group by immutable session ID, never by name

A view MUST be grouped by the base's session ID, never by its name.

#### Why

tmux resolves `-t <name>` against **group** names before session names. If a
base session dies but its group name lingers inside a stale view, grouping a new
view by name silently joins the wrong window set instead of failing.

### 9.2 Lifetime is independent; window and pane are shared

A view is a real, separately killable session. Killing the base leaves the view
alive, and sweeping views is the caller's responsibility.

The window and pane are **shared** with the base and every other group member,
so copy-mode state and scroll position are shared too. Scrolling one view moves
the scroll position for the base and all sibling views. There is no independent
viewport per view.

### 9.3 Creating a view, and what it MUST NOT reconfigure

View creation is not a side-effect-free read. It defines a key table via
`bind-key -T`, which tmux scopes to the server rather than to the new session.

#### The key table is inert, and MUST stay inert

A named key table applies only to sessions whose `key-table` option points at
it, so the server gains an entry no other session consults. A view MUST NOT
rebind anything in tmux's own `root` or `prefix` tables, where it would change
what the operator's existing sessions do.

#### What the pass-through table binds

- **The wheel**, both ways (§9.2).
- **A click.** `MouseDown1Pane` selects the pane under the pointer and forwards
  the click, as tmux's own root binding does. A view attached interactively
  (§8.9) needs that on touch, where no keyboard shortcut moves focus. The active
  pane is the shared window's (§9.4), so the base follows, exactly as a click in
  the base would.
- **No drag.** Copy-mode on a shared pane would drag the base into it.

#### A view MUST NOT touch `terminal-features`

`terminal-features` is a server option with no per-session form. Appending to it
changes how tmux renders for *every* client of that server, including the
operator's own sessions, permanently, whenever Olympus is pointed at a server
they already run. Olympus pins only what it discloses (§17.5), and this is
neither disclosed nor necessary.

#### The hyperlink capability belongs to the client

The attach path MUST declare the `hyperlinks` feature for its own client with
tmux's `-T` flag. The flag is global, so it precedes the command:

```
tmux -S <socket> -T hyperlinks attach-session -t =<name>:
```

##### Why

tmux strips OSC 8 hyperlink escape sequences for any client whose terminal has
not declared the `hyperlinks` capability. A headless PTY client never answers
tmux's runtime probe, so without a declaration the links vanish for that client
with no error anywhere. A real terminal answers the probe and needs nothing.

Measured: `#{client_termfeatures}` reports `hyperlinks` for a client started
this way and omits it otherwise, while the server's `terminal-features` is
unchanged.

Scoping the declaration to the client is strictly better than the server option.
It reaches exactly the clients that need it, is not shared with anyone, and
disappears when the client does.

### 9.4 Focusing a view moves the BASE's active pane

A grouped view keeps its own current *window*, but the current *pane* is a
property of the shared window rather than the session. So the `select-pane`
call that focuses a view on the pane its base is showing **also moves the base
session's own active pane**.

On a single-pane session, the only shape Olympus's creation verbs produce, this
is unobservable. It becomes visible when a consumer splits a base session into
several panes and then creates a view over it. This is accepted, and documented.

#### A view MAY be pinned to a window

The current window is per-session even inside a group, so selecting a window in
the view moves nobody else. Measured: a base showing window 0 and a view pinned
to window 1 report `0` and `1` respectively, and `status off` on the view leaves
the base's status bar alone.

A pinned view MUST NOT then `select-pane`. The pane is the one thing it cannot
choose privately, and a bare attach (§8.9) exists to show one window without
disturbing anyone.

#### The window MUST be matched exactly before the view is created

The window MUST be checked against the base's own window list, as an exact match
on the index or the whole name. A window the base does not have is
`SESSION_NOT_FOUND`, and nothing is created.

##### Why

tmux's own target matching accepts a name prefix, so handing it the caller's
spelling turns a typo into a window.

### 9.5 Listing views

Views owned by this backend are enumerable as `{view name, base session}`. An
empty result MUST serialize as an empty list, never null.

The base name comes from tmux's own `#{session_group}`. §9.1 groups by the base's
session ID rather than a synthetic name, so tmux's group-name answer for *any*
member of the group is the name the base had when the group formed. A rename of
the base (§2.11) does not move it. So while a session still answers to the group
name, that is the base; once none does, the base is the group's one member that
is not a view. A group an operator has added sessions of their own to is left
under its group name rather than guessed at.

#### Views are not sessions to `ls`

The ergonomic layer's `Sessions` (behind `ls` and `list_sessions`) MUST leave
out every name of the §17.1 view shape. The backend-level `Sessions` is
unchanged, since `Views` and the tmux group bookkeeping read it.

##### Why

To the multiplexer a view is an ordinary session, so a backend's own listing
returns it. A view is scaffolding Olympus built over a session, and a caller
offered it as a session will attach a view onto a view. Measured: a web launcher
listing `ls` verbatim did exactly that.

### 9.6 Focusing a pane by cell

`view focus <view> --col N --row M` (`focus_view`, `FocusView`) selects the pane
under a cell the caller names.

#### Why

A view MAY be attached with mouse reporting off, so that a desktop browser keeps
its native text selection. A click then never reaches tmux, the §9.3 click
binding cannot fire, and nothing can change the active pane by touch. The client
still knows the clicked **cell**, so this verb turns it into the same
`select-pane`.

#### Coordinates

Coordinates are 0-based within the client area. On the view's **current
window**, the pane selected is the one whose rectangle contains the cell. The
rectangle is tmux's `#{pane_left}`, `#{pane_top}`, `#{pane_right}` and
`#{pane_bottom}`, every edge inclusive.

Measured on an 80x24 window split in two: `%0` spans columns 0 to 39, `%1` spans
41 to 79, and column 40 is the border.

| Cell | Result |
|---|---|
| inside a pane | that pane is selected |
| on a border, or outside every pane | nothing is selected; the result reports an empty pane id and MUST NOT be an error |
| negative coordinate | usage error |

A cell with no pane is not an error because the coordinate was legitimate and
there was no pane there.

#### Resolution

The active pane is the shared window's (§9.4), so the base follows, exactly as
the click binding moves it. The view MUST be resolved like every other target
(§10). A pane id addresses its owning view, and a view's absence from `ls`
(§9.5) does not affect resolution, which reads panes rather than the filtered
session list.

---

## 10. Targets and resolution

**A pane id addresses the session that owns it, on every backend.** An id a
caller reads out of a pane listing MUST work as a target without them knowing
which backend produced it. Otherwise the listing hands out identifiers its own
API rejects.

### Pane id spelling per backend

Only the spelling differs, so only the spelling is per-backend:

| Backend | Pane id | Why that shape is unambiguous |
|---|---|---|
| tmux | `%0` | The prefix cannot begin a session name Olympus would use. |
| meja | `1` | meja rejects a session name that is entirely numeric, so a bare integer can only be a pane. |
| zmx | the session's own name | No pane concept. The row is synthesized 1:1 from the session, so resolution is the identity. |
| herdr | `w1:p2`, `w4Y:pA` | Not structurally unambiguous. See below. |

#### herdr ids

The same alphabet spells herdr's other two levels: `w1` for a workspace and
`w1:t2` for a tab. A workspace with an empty label is NAMED by its id (§3.6).

Each segment is a herdr public number: base 32 over
`123456789ABCDEFGHJKMNPQRSTVWXYZ0`, digits for the first nine allocations and
letters from the tenth. The shapes MUST accept letters, or they stop matching
real ids on any server that has seen its tenth workspace, tab or pane. Measured:
the tenth pane is `w1:pA`, the tenth workspace `wA`, and the workspace counter
survives a restart.

#### herdr rejects the id shapes as names

herdr accepts a workspace label of any spelling, `w1:p2` included, so a session
could be shadowed by a pane id, a workspace id or a tab id. The backend
therefore REJECTS a name of any of the three shapes at creation as a usage
error.

That rejection turns "probably a pane" into "certainly a pane". meja gets the
same guarantee for free from its own naming rule; herdr buys it explicitly.

On herdr the shape is read by the backend rather than passed into the shared
resolution, because a pane id there does NOT resolve to its session (§10.1).

### The shape is passed in, never branched on

The shape MUST be passed into resolution rather than branched on inside it. One
rule with a per-backend spelling stays one rule. A copy per backend is where the
two silently stop agreeing.

### Every pane row MUST name its own session

This includes a whole-server listing. That listing is exactly where a caller
cannot supply the owner, and it is what resolution reads to swap an id for a
session. A row that cannot name its owner resolves to nothing, and the operation
then reports a live session as absent.

### 10.1 A pane id is an address for the session, not for the pane

After resolution the operation runs against the session's **active** window and
pane. Addressing a pane in some other window does not reach that pane. `send %0`
and `send <session>` are the same operation.

#### Why

This reads like a defect until the reason is visible. Every name comparison and
every write-lock key is session-scoped (§11). A pane-precise target would key
locks on something the rest of the system cannot see, and two callers driving
two panes of one session would serialize against nothing. Precision would buy
addressing and sell the lock.

It costs nothing on a session Olympus made, which is single-window and
single-pane by §17.4. It only becomes visible on a session somebody else added a
window to. Then the honest answer is that Olympus does not manage windows, not
that it will reach into one.

#### Tests MUST NOT depend on which window is active

Which window is active after another window appears is the **multiplexer's**
decision, and it differs: measured, tmux switches to the new window while meja
stays on the current one. Olympus MUST NOT depend on either. Tests of this
behaviour MUST assert that a pane id and a session name are indistinguishable,
not which window received the text.

#### herdr is pane-precise

herdr is the one deliberate exception to this section. A target passes through
resolution unchanged and the backend reads its level from its shape (§3.6):

- `w5:p3` acts on that pane, not on the pane its workspace is showing.
- `w5:t2` acts on the pane that tab is showing.

Every herdr request already addresses a pane, so precision costs no addressing.
What it costs is the lock. The write lock (§11) is keyed on the target as given,
so a caller driving `w5` and a caller driving `w5:p3` serialize against nothing,
even while the workspace is showing that pane.

This is recorded rather than fixed. Keying the lock on the owning workspace
would put a listing before every lock take, on the path §11 keeps
subprocess-free. Two callers driving two panes of one workspace, the common
shape on a herdr fleet, are not contending for anything. A caller who wants the
session-scoped guarantee addresses the workspace.

#### tmux: exact-match targets and their scope

Every tmux backend operation addresses sessions through exact-match `=<name>`
syntax. That syntax does **not** accept a bare pane id (`%0`), even though
tmux's own `-t` does. Without resolution, consumers holding pane ids would have
to do their own session lookup before every call.

The exact-match prefix does not make one target shape fit every command. tmux
resolves `-t` against whatever the command operates on, so the scope suffix
matters:

| Scope | Target | Commands |
|---|---|---|
| session | `=<name>:` | `has-session`, `kill-session`, `list-panes -s`, `set-option` and `show-options` for a session's status |
| window | `=<name>:` | `set-option -w` |
| a named window | `=<name>:=<window>` | `select-window`, `rename-window`, a window capture |
| pane | `=<name>:.` | `send-keys`, `capture-pane`, `set-option -p` |

A session target carries the colon too. Without it tmux reads a `.` in the name
as a pane separator: `=a.b` is session `a`, pane `b`, so a session named `a.b`
could not be probed, and a kill that could not find it would report success. A
bare name without `=` is no better, since `set-option -t cwd` reaches a session
named `cwdx`. The window part takes its own `=` for the same reason: tmux
matches a window name by prefix, and `sec` would land on `second`.

`send-keys` and `capture-pane` reject a bare session target outright, and
`set-option -w` rejects it with `no such window`. That last one is why §2.2's
cleanup rule has its form: the `new-session` at the head of the chain has
already succeeded, so a rejected suffix leaves a live, half-configured session
behind rather than failing cleanly.

#### tmux: resolving a pane id

On tmux, a target beginning with `%` MUST be resolved against a full-server pane
listing and swapped for its owning session's name before the call proceeds. Any
other target passes through unchanged. Resolution MUST live in **one** shared
place every operation calls, never duplicated per operation.

The resolution rules:

- **No match.** Resolution fails and the operation returns not-found **naming
  the pane id**, not a resolved session name. There was never a session to name.
- **A corpse pane (§2.7) MUST still resolve.** Resolution answers which session
  owns a pane, not whether that session is healthy. Collapsing the two would
  turn every died-session question into not-found before the caller's own death
  handling could report it.
- **More than one row.** A base session and its views share the same underlying
  pane (§3.4), so resolution MUST select the base, the earliest `created_at`,
  and not merely the first match. Resolving to a view operates on the wrong
  session, and killing one leaves the real session running. `created_at` is
  whole seconds, so a view made in the same second as its base ties with it;
  the tie goes to the lower session number, which the server hands out upward.
- **A failed listing MUST NOT become not-found.** "Could not ask" and
  "definitely gone" stay distinct for the reason §3.2 gives, so the listing
  error propagates with its own code.
- **An empty target is `USAGE`.** An empty string compares equal to nothing and
  would key a write lock of its own, which is the mismatch this section exists
  to prevent.

On zmx there is no pane-id concept, so a `%`-prefixed target is an unknown
session name under the ordinary lookup: still not-found, and it MUST NOT crash.

### Resolve before comparing or locking

Any caller that compares a target against a session name, or keys a lock on it,
MUST resolve first. Otherwise a pane-id caller silently mismatches every name,
which is the source of false "already gone" and false "died" reports.

---

## 11. Concurrency

### 11.1 The per-session write lock

Concurrent writers to one session MUST serialize through an advisory,
`flock`-based, per-session lock.

#### Key derivation

The key is the (backend, socket-or-directory, session) triple:

- **Hash the whole triple.**
- **Sanitize the session name** for a readable prefix: keep `[A-Za-z0-9._-]`,
  replace everything else with `_`.
- **Place the lock file** under a private temporary directory with mode 0700.

Two different sockets, directories or backends MUST never contend on the same
lock file, even when a session name collides.

The hash MUST cover the session name, not only the socket or directory.

##### Why

Sanitizing makes a name a *safe* path component but not a *unique* one. `my
build` and `my_build` sanitize identically, so a key that only sanitizes the
name makes two unrelated sessions share a lock. The visible symptom is not only
over-serialization: it is a `CONFLICT` raised against a caller about a session
it never touched.

#### The lock file is never removed

Releasing closes the descriptor and leaves the file.

##### Why

Unlinking the file on release races another process that has the same path open
and is about to lock the now-unlinked inode. Both would then hold a lock on
different inodes and run at once, which is the failure the lock exists to
prevent.

The cost is one empty file per distinct (backend, scope, session) triple, for
the life of the temporary directory. It is bounded by how many distinct sessions
a machine addresses between reboots, the files are zero bytes, and a system that
clears its temporary directory clears them. The accumulation looks like a leak,
and the obvious fix for it reintroduces the race.

#### Other properties

- **Advisory only.** `flock` is cooperative: only other Olympus processes going
  through the same path observe it. A human typing in a raw `tmux attach`, or
  any non-Olympus writer, is unaffected and can still race.
- **Contention is a conflict-class error**, after polling for the configured
  wait.
- **The target MUST be resolved before it is used as a key** (§10). A pane-id
  caller and a session-name caller addressing the same session would otherwise
  take two different locks and not serialize at all.

#### Which operations take it

Operations that MUST take the lock, and the scope each holds it for:

| Operation | Lock scope |
|---|---|
| literal text send | the send |
| key send | the send |
| paste | the paste, plus the optional trailing submit |
| verified send | send, verify, submit, as one section (§11.2) |
| atomic send | both writes, on backends needing two (§4.7) |
| ensure | the whole check-then-create decision (§2.6) |
| run (sync) | the injection only, released before polling (§11.2) |
| run (detached start) | the injection only |

Operations that MUST NOT take it: every read (list, probe, capture,
capabilities, pane listing), and detached-run polling. A read that blocks on a
writer's lock turns observation into contention, and observing a busy session
is the case that matters most.

Opting out MUST be possible for a caller that already serializes its own writes,
and MUST be explicit.

### 11.2 Lock scope is per-operation, and the two rules are opposites

These cases look analogous and are not. Getting either backwards is a real
defect.

#### Verified delivery holds the lock across send, verify and submit

The three steps are ONE critical section. The lock MUST NOT be released and
reacquired between verification succeeding and the Enter being sent. A competing
writer landing in that gap (another send clearing the line, a resize)
invalidates what verification just confirmed, so the Enter would submit
something other than what was verified.

#### Running a command releases the lock BEFORE polling

Only the injection needs to be atomic with respect to concurrent writers. The
polling phase only reads. Holding the lock across the whole wait would block
every other writer against the target for the full timeout, for no benefit.

#### The distinguishing question

*Does the phase after the lock gate a subsequent write whose correctness depends
on the observed state still holding?* If yes, hold. If it only reads, release.

### 11.3 The attach guard is a separate mechanism

Stealing an attach slot is a different contention problem from serializing
session writes, and MUST NOT reuse the write lock. See §8.5.

---

## 12. Error vocabulary

The error codes and their process exit codes are a **stable, semver-bound
contract**. A shipped code is never repurposed or removed; only new ones are
added.

| Code | Exit | Meaning |
|---|---|---|
| `USAGE` | 2 | Input the caller could have validated. |
| `SESSION_NOT_FOUND` | 3 | The target session or pane does not exist. |
| `BACKEND_UNAVAILABLE` | 4 | The selected backend cannot be reached. |
| `TIMEOUT` | 5 | An operation did not complete or match before its budget elapsed. |
| `CONFLICT` | 6 | A lock or attach slot is held by someone else. |
| `UNSUPPORTED` | 7 | The backend has no concept for this operation at all. |
| `AGENT_BLOCKED` | 8 | The target's agent is waiting on a person or shows something open over its input box, and the input was refused before anything was typed, or stopped before its terminator when the prompt opened after typing (§7.5). |
| `UNEXPECTED` | 1 | Anything not carrying one of the above. |

### Two distinctions that MUST be preserved

- **`UNSUPPORTED` is not `BACKEND_UNAVAILABLE`.** Unsupported means the question
  does not apply to this backend (views on zmx). Unavailable means a backend
  that *has* the concept could not be reached. Consumers should branch on a
  capabilities query rather than on the unsupported error.
- **`UNSUPPORTED` is not "absent".** A tmux server-environment key that is unset
  answers *present: false*: asked, and got a real negative answer. zmx answers
  unsupported, because the question itself does not apply.

### Avoidable errors are `USAGE`

Any error a caller could have avoided by changing one argument MUST be `USAGE`,
including an unknown backend name. `UNEXPECTED` is what a machine consumer reads
as "Olympus broke, retrying will not help."

### Every error reaches the structured output

**Every error, including usage errors, MUST reach the door's structured
output.** A caller MUST NOT have to know which internal layer caught a failure
in order to know whether the failure is machine-readable.

### 12.1 Process exit codes, and the two operations that deviate

For every operation the process exit code is the code from the table above.
**Two deviate, deliberately, and both MUST be documented at the door.**

#### Running a command

A completed run has two independent outcomes that must not be conflated:

- whether the sentinel protocol worked, which is Olympus's concern;
- what the command's own exit code was, which is the caller's concern.

A failing command exiting `1` is a normal result, and neither code is an Olympus
failure. So the two paths differ:

| Path | Process exit on a successful protocol run | Infrastructure failure |
|---|---|---|
| Human | the *command's own* exit code, composing in a shell pipeline like running the command directly | the table |
| Structured | `0`, whatever the command's exit code; that code is carried in the payload | the table and the error envelope |

This asymmetry MUST be a local special case at the run door, never taught to the
shared error-to-exit-code mapping.

##### Why

That mapping translates failures. A successful run carrying a second, unrelated
exit code is not a failure, and making the shared path aware of it leaks
run-specific meaning into code every operation shares.

#### Attaching

Once the presence gate (§8.1) passes, attach hands off to the backend's own
client inside the PTY Olympus owns, and the process's exit code follows *that
client's*. An attach exiting `3` is therefore not necessarily not-found. It may
be the attach client's own unrelated status.

### 12.2 Usage errors MUST NOT escape through the argument parser

Argument-parsing errors MUST be intercepted and emitted through the same
envelope, with the same code, as a usage error the application detected itself.

#### Why

A CLI framework's own flag validation (unknown flags, bad values, wrong
positional arity, missing required flags, mutually exclusive violations)
typically prints to stderr and exits *before* any application code runs.

Whether a usage-class failure is machine-readable would then depend on which
layer caught it, which is an implementation detail from the caller's side. This
is the specific failure mode §12's rule exists for, and Olympus MUST NOT ship
that split.

### 12.3 Absence semantics

"No server running" collapses into the negative answer, not an error, for every
question where the negative answer is meaningful:

| Question | Answer with no server |
|---|---|
| presence probe | `absent` |
| server-environment read | `present: false` |
| listing | empty list |

A query against a socket with no server behind it is "nothing to find here", not
"something went wrong asking".

#### Target-addressed operations name their own target

A target-addressed operation resolves the same absence into not-found **naming
its own target**. tmux reports the absence in its own vocabulary (a socket path,
a pane id, or nothing at all), and a caller holding a session name can match
none of those against what it asked for.

#### Detection is not one string match

tmux spells the condition two ways depending on the subcommand:

- `list-sessions` reports `no server running on <socket>`.
- Most others fail at connect time with `error connecting to <socket> (No such
  file or directory)`.

Matching only the first classifies every other verb's no-server case as
`UNEXPECTED`. That is the opposite of this section's rule, and invisible until a
caller hits a verb nobody tested cold.

---

## 13. Capabilities

Capabilities are static, subprocess-free backend facts a consumer feature-probes
**before** hitting an unsupported error.

| Capability | Field | Rule |
|---|---|---|
| backend name | not on the wire (see below) | |
| native scrollback | `native_scrollback` | §5 |
| views | `views` | §9 |
| remain-on-exit | `remain_on_exit` | §2.7 |
| server environment | `server_env` | §1.2, §12.3 |
| control keys | `control_keys` | below |
| spawn sizing | `spawn_sizing` | below |
| spawn command | `spawn_command` | §2.3.1 |
| session status | `session_status` | §13.1 |
| alt-screen tracking | `tracks_alt_screen` | §5.3 |
| servers | `servers` | §13.2 |
| session client | `session_client` | §8.10 |
| bare attach | `bare` | §8.9 |
| focus | `focus` | §8.10 |
| rename | `rename` | §2.11 |
| agent status | `agent_status` | §3.7 |

The name is carried on the capability value so it is self-describing in-process,
but it is **not repeated on the wire**. Every structured shape that reports
capabilities already names the backend on the row or in the envelope (api §5),
and a second copy would be a second place for the two to disagree.

### Attach capabilities decide which attach a caller can offer

A consumer presenting a "clean" or a "mirror" attach would otherwise have to
branch on the backend's name, and that table goes stale when a backend gains or
loses a client.

| Field | True where | Section |
|---|---|---|
| `session_client` | the backend has a client distinct from its raw per-pane stream | §8.10 |
| `bare` | an attach can show a session as a plain pane with no chrome | §8.9 |
| `focus` | the server's focus can be steered onto a target without attaching | §8.10 |
| `rename` | a target can be given a new name in place | §2.11 |

All four are refused as `UNSUPPORTED` where false.

### Agent status

The agent listing answers on every backend, with different rows. The verb is
never refused (§3.7), so a consumer cannot probe it by trying. `agent_status`
says whether the rows will carry a status and a title, because the backend
detects agents itself, or only the pane and the agent's name, matched on its
command.

### Spawn command

A session's process is not always the caller's to choose. A backend whose panes
run the program its own configuration names has nowhere to put an argv, so
`CreateSpec.Command` is a request it cannot honour at all (§2.3.1).

It is a capability rather than a degraded-operation warning because the caller's
whole approach changes:

- **With it**, spawn the program and read only its output.
- **Without it**, start a shell, hand it over with `exec`, and accept the echoed
  command line and the quoting a shell in the path forces on you.

### Spawn sizing

A backend that sizes a session from the client that attaches it cannot honour a
size chosen at creation. A request for one succeeds while producing something
else: measured, 120x40 becomes 120x40 on tmux and 80x23 on meja.

Both halves of §0.8 apply. The capability says whether to ask, and a
degraded-operation warning says what happened when a caller asked anyway.

### Control keys

Control-key delivery decides whether a full-screen program can be DRIVEN, which
makes it the most consequential entry here. Without it a caller can open an
editor, read it, and never get out of it.

It is a capability rather than a degraded-operation warning because the caller's
whole approach changes. With it, drive the program. Without it, do not start.

### Alt-screen tracking

The alt-screen flag alone is ambiguous. A backend that never sets the flag is
indistinguishable from one whose panes are not on the alternate screen. Without
a capability to branch on, a caller cannot tell "not on the alt screen" from
"not tracked", which is the ambiguity the flag exists to remove.

### 13.1 Session status

A session MAY carry a **status**: an opaque label a process *inside* it leaves
for whoever is driving it from outside.

#### Why

A capture cannot answer the question. A program sitting at a prompt and a
program halfway through work can render identically, and the difference is a
fact only the program itself holds. Waiting on a screen pattern means guessing
at a program's idle appearance. Waiting on a status means the program said so.

#### Olympus MUST NOT interpret the value

Olympus MUST NOT interpret the value, and MUST NOT define a vocabulary of
states. The value is stored and returned exactly as given.

What counts as busy, blocked or finished is a property of the program in the
session, not of the terminal. Enumerating states would name the concerns of
whatever is driving Olympus rather than the thing Olympus drives, which §0 rules
out.

Matching is therefore **exact**, never a pattern. A partial match would be
Olympus reading structure into a string it has promised not to read.

#### An unset status is empty, not an error

This is the same tri-state rule as presence (§3.5). A caller must be able to
tell "has reported nothing" from "could not ask".

#### A backend that cannot carry one MUST refuse both the write AND the read

Refusing the write alone is not enough. A read that answers empty is
indistinguishable from a session that has not reported yet, so a caller cannot
tell "not yet" from "never, on this backend".

Accepting the write and answering empty is the worst outcome of the three. A
caller waiting on a state that can never arrive has no failure to react to, only
silence.

It is a capability rather than a degraded-operation warning because the caller's
whole approach changes. With it, coordinate on reported state. Without it, fall
back to screen patterns and their guesswork.

#### Where the status is stored

The store must outlive the process that wrote it. The reporter is inside the
session and the reader is outside, and they never run at the same moment.

| Backend | Store |
|---|---|
| tmux | a session-scoped user option, which tmux keeps and never acts on |
| zmx | none: no per-session metadata of any kind, so the capability is false |
| herdr | display-only server metadata, handed back on the row. A session's status is the workspace's metadata; a pane target's is the pane's own (§3.6) |

herdr's store has the same shape as tmux's user option: written by one process,
read by another, outliving both.

#### Not a capability: whether a session outlives its command

Capabilities MUST NOT include whether a session outlives its command. That is a
property of the **caller's** own wrapper (does the shell it spawned keep running
after the tracked command exits), not of backend mechanics. Putting it here
misattributes a consumer-side design choice to the backend.

### 13.2 Servers

A server is the level above sessions. Every backend can run several, each behind
its own socket, and every other operation in this specification addresses
exactly one of them: a tmux socket, a herdr socket, a zmx directory.

Enumerating servers and selecting one BY NAME is a capability, `servers`. What a
name resolves to is backend-local, and a caller has to know whether the question
can be asked at all before asking it.

What a row *is* differs per backend and MUST be disclosed rather than reported
as equivalent, the same way §3.4 treats pane fields.

#### tmux

- **A row is a socket NAME** in tmux's per-user directory (`$TMUX_TMPDIR/tmux-<uid>`,
  else `/tmp/tmux-<uid>`), the same resolution `-L` performs. The directory
  MUST be the one tmux itself resolves, so a caller who moved `TMUX_TMPDIR` scans
  the servers they address.
- **Running is measured by asking the server**, never inferred from the file.
  Killing a server does not unlink its socket, so a file with nothing behind it
  is a known server that is stopped.
- **A server started with a socket PATH is not discoverable.** There is no
  registry of those, so it is absent from the listing by construction.
- **`default`** is the row tmux addresses with no `-L`. Olympus's own default is
  a different socket (§17.2).
- **Selecting a name with no socket file behind it is not-found**, like an
  unknown name on every other backend. Passing it through to `-L` would make
  every verb behave as if the server were merely stopped, so `stop` on a server
  that never existed would report `gone`, a success.
- **`--server` only ever selects.** A caller who means to CREATE a server names
  its socket with `--socket`, which takes any name.

#### herdr

A row is a named session, as `herdr session list` reports it. The listing runs
against the operator's real configuration directory, which is where named
sessions live. It therefore carries neither the socket override nor the state
redirect every other invocation carries.

#### zmx

Exactly one row: the socket directory in use, named `default`, running when the
directory exists. There is nothing to stop apart from its sessions.

#### meja

Unsupported. Its profiles resolve under its own store and nothing in its CLI
enumerates them, so a server there is addressed by knowing its socket path and
by nothing else.

### 13.3 A server's prefix is reported, never changed

The server row carries `prefix`, read from the server's configuration. `info`
carries the same key for a present session, so a caller holding a target learns
it without a server listing, which meja cannot give.

#### Why

A multiplexer's own key bindings sit behind a prefix key. A caller that hands a
human a terminal onto a server (a browser with a soft keyboard, say) has to know
the prefix to offer it, since a chord a keyboard cannot form is a binding a
human cannot reach.

#### Where each backend reads it

| Backend | Source |
|---|---|
| tmux | the global `prefix` option, asked of a running server. A stopped one cannot answer, and the row says nothing |
| herdr | `[keys] prefix` of the config.toml the server resolves under: the session's own where it has one, else the operator's. herdr's default where none is set |
| meja | the constant the program fixes, `C-b`, documented as unconfigurable |
| zmx | no prefix |

The spelling is tmux's whichever backend answered (`C-b`, `C-Space`, `M-a`,
`F19`), so a caller turns one form into bytes rather than one per backend.

#### Read, never written

Which key a server binds, and what it binds behind it, is the operator's
configuration. Olympus configures only servers it starts (§17.5). A caller
wanting a different prefix edits the configuration, not the server through
Olympus.

#### Selecting a server by name resolves INTO the backend's ordinary address

The resolution happens in one place, so the lock key (§11) identifies the server
the same way whichever spelling chose it.

- **A name given together with an explicit address is USAGE.** It is two answers
  to one question, and whichever lost would leave the caller on a server they did
  not mean.
- **An unknown name is not-found.**

#### On herdr, a server selected by name MUST be addressed by its socket alone

The socket-only environment applies to every invocation, and **nothing Olympus
would write goes under the directory the socket sits in**: no state home, no
managed configuration. The derived client socket still has to fit the platform
budget, and an over-long one is refused by name before any invocation.

##### Why

A named session's socket lives inside the operator's configuration tree. The
state-home derivation §2.9 requires of a socket PATH would put Olympus's own
configuration and state directories inside that tree.

#### A create MAY start such a server, and starting it is not owning it

The server boots on the operator's own configuration. The socket-only
environment carries no redirect, so it reads exactly what their own
`herdr server` would. No pins are laid down, no ownership is recorded, and the
ownership-scoped stop of §2.9.1 still refuses it. What the tree gains is what
herdr itself puts there, which is the operator's server doing its own business.

##### Why

The hazard is the *writing*, not the boot. Refusing to boot a named server
would make the one server a caller cannot start the very one they named. On a
box whose herdr comes up with the operator rather than with the machine, that is
every session on it after a reboot, and the caller would be told only that a
socket has nothing behind it.

#### Stopping a server takes every session on it

Stopping a server is its own operation, never a side effect of stopping a
session. The layer above the backend checks the name against the listing first:

| Server | Result |
|---|---|
| unknown | not-found |
| not running | `gone`, without the server being told anything |
| running, then stopped | `killed` |

This is the same idempotence §2.8 gives a session. On herdr this is
`session stop` by name, deliberately distinct from the ownership-scoped stop of
§2.9.1: a caller naming a server has named the thing they mean to take down.

### 13.4 A server can be told to come up, and told only that

Starting a server is its own operation, and it MUST NOT create anything on the
server it starts: no session, no window, no pane. What comes up with the server
is whatever the backend restores of its own accord, and a caller reads that from
a listing afterwards like any other state.

#### Why

Every other operation here refuses to boot a server. Creation is what starts
one, the way tmux's first `new-session` does, and a listing or a probe that
started what it was asked about would answer with a thing it had just made
(§13.2).

That leaves a machine that has just rebooted with no way to say "come up". That
is when saying so matters most, because a backend that RESTORES what it was
running does that when its server boots. herdr does: its named session comes
back with the panes it was running when it stopped. Without this operation, the
only way to reach that restore would be to create a session nobody asked for.

#### An answering server is left alone

A server that is already answering MUST be left alone (not restarted, not
reconfigured, not claimed) and reported as running rather than refused. This is
the same idempotence `stop` gives a session (§2.8).

Starting a server is not owning it. A server addressed by name comes up on the
operator's own configuration, with none of Olympus's pins written into their
tree, and Stop still refuses it (§2.9.1, §13.2).

#### Backends without an independent server answer unsupported

tmux and zmx come up with their first session and have nothing to start on their
own. meja has no server listing to select from (§13.2). Only herdr starts a
server.

### 13.5 Which client shows what is the server's to report

The `clients` listing is the SERVER'S report, read when asked. Olympus MUST NOT
answer it from the targets its own attaches were given, which describe where a
client was put, not where it is.

#### Why

A caller holding one client per person (a web terminal with one bare attach per
browser) has to answer "which session, window and pane is this person's client
showing now". It cannot answer from what it asked for. A person moves the client
with its own keys (a pane focused inside a split, a zoom), and on a server whose
clients each keep their own view nothing else holds the answer.

#### Row fields

One row per client the server reports, in the server's order:

| Field | Meaning |
|---|---|
| `id` | the client's id |
| `tag` | its tag, where it was launched with one |
| `session_id` | the session it shows |
| `window_id` | the window it shows |
| `pane_id` | the focused pane of the window it shows, which is where what it sends goes |
| `zoomed` | that window shows the pane alone |
| `view_applied` | the client has applied the view the server holds for it, so what it sends now reaches that pane |

A field the server does not report MUST be omitted, never answered false or
empty. `zoomed: false` and `view_applied: false` are answers, and a server that
cannot give them must not be read as giving them.

#### herdr

On herdr a session is a workspace and a window is a tab (§3.6). Only a server
that advertises `client_view_focus` reports where each client is, over
`client.list` (§8.10).

- Rows carry `pane_id` and `zoomed` only where the server also advertises
  `client_view_ack`.
- Rows carry `view_applied` only for a client there whose row says
  `snapshot_acks: true`.

Measured against such a server:

1. A bare client launched onto a split tab is listed on the tab's focused pane.
2. After a `pane focus` on the server moves that tab's focus to the other pane,
   the same client is listed on the other pane, on the same workspace and tab.
3. After the client's own focus-pane key (the prefix, then `h`, written to the
   client in the kitty spelling it reads keys in), it is listed back on the
   first.

#### Where the listing is unsupported

A server that does not advertise the capability cannot say, and the listing is
UNSUPPORTED there, as it is on every other backend. An empty list would claim
there are no clients. No server running is an empty list (§3.3).

#### Not a field of `capabilities`

The server capability is asked, never inferred from the version. It is not a
field of `capabilities` (§13): those are static facts of a backend, and this is
a fact of one running server, which two servers on one backend answer
differently. The listing itself is the probe.

#### Tag filter

A tag filter answers the one client carrying it, as a listing of one. A tag no
client carries is SESSION_NOT_FOUND: a caller asking where its own client is must
tell "not there" from "there".

A tag is what the server holds it to: one to 128 bytes of UTF-8 with no control
character. One outside that is USAGE before the server is asked (§12).

#### Naming a client

A caller names its client with the attach's client tag (`--client-tag`, §8.10),
since an interactive attach has no channel to report a generated name back.

The tag MUST be refused as USAGE wherever no tagged client is launched:

- any backend but herdr;
- an attach that is not bare;
- a herdr server that does not advertise `client_view_focus`.

It is refused rather than dropped because a caller that then asks where its
client is would look for a name nobody carries.

herdr does not hold tags unique, so two attaches given one tag are two clients
the filter cannot tell apart. Which tag is whose is the caller's to keep.

## 14. Exit-marker inspection

Exit-marker inspection parses a caller-supplied completion echo out of a session
that outlives its command (the wrapper pattern
`echo output; cmd; echo DONE:$?; sleep N`).

### The marker is always caller-supplied

Olympus has no opinion on the marker format, and there MUST NOT be a default
marker. A fixed default invites collision with ordinary program output or stale
scrollback, and weakens the caller-controlled uniqueness the design assumes.

### The marker is the whole prefix, separator included

For the wrapper above, the marker is `DONE:`, not `DONE`. Olympus takes the exit
code from the token immediately after the marker string and does not skip a
separator of its own. Skipping one would be an opinion about the format it has
promised not to have.

#### Why this is stated

Getting it wrong fails **silently**. A marker that never matches is reported as
not-found, a legitimate answer meaning "that command has not finished", so a
reaper waiting on it never fires. A caller passing `DONE` while echoing
`DONE:$?` waits forever and sees no error.

### The exit code is the leading token after the prefix

The exit code is the leading whitespace-delimited token after the marker prefix,
not the whole rest of the line. The token itself stays strict: `MARK:0abc` is
still malformed, and mid-line occurrences are still not line-anchored.

#### Why

After a TUI process exits, the wrapper's echo lands on a rendered row still
carrying leftover screen content to its right, because the exiting TUI never
cleared to end of line:

```
TASK_COMPLETED:0 Esc to cancel
```

Requiring the entire remainder to parse as an integer classifies every such
legitimate exit as malformed. The exit code stays null forever, and any consumer
whose reaper treats a missing marker as "still running" never reaps anything.

### A content question, not a run question

This answers a **content** question ("what marker, if any, is on screen"). A
detached run answers a **desired-state** question ("did the injected run line
finish"). The two read different evidence and neither substitutes for the other.

---

## 15. The MCP door

### 15.1 Target revision and SDK

Olympus's MCP server targets MCP revision **`2026-07-28`** and is built on the
**official Go SDK**, `github.com/modelcontextprotocol/go-sdk`, pinned at
**v1.7.0**: the first release whose latest supported revision is `2026-07-28`.

Protocol framing MUST NOT be hand-rolled. The SDK is one of the three budgeted
dependencies so that this door tracks the spec by upgrading a pin rather than by
editing wire code.

### 15.2 What this revision changes

`2026-07-28` is not incremental. The handshake is gone:

- **There is no negotiation handshake.** Requests are stateless and
  self-contained. Every request declares its protocol version in its `_meta`
  field, and the server accepts or rejects each request independently.
- **Servers MUST implement `server/discover`**, returning supported versions,
  capabilities and instructions. Clients MAY call it before anything else but
  are not required to. A client may invoke any RPC inline and handle the error.
- **An unsupported requested version MUST be answered with
  `UnsupportedProtocolVersionError`**, JSON-RPC code **`-32022`**. Its data lists
  both the versions the server supports and the one requested, so the client can
  retry with a mutually supported version.
- **Optional extensions** are negotiated through an `extensions` map in
  capabilities, keyed by prefixed identifiers. If one party supports an
  extension and the other does not, the supporting party MUST either revert to
  core behavior or reject with an appropriate error.

### 15.3 Dual-era support, and why it costs nothing

The MCP spec calls a server **modern** if it uses per-request metadata,
**legacy** if it uses the `initialize` handshake, and **dual-era** if it serves
both. Olympus is dual-era **by construction**, because SDK v1.7.0 already:

- registers `server/discover` unconditionally in the server's method table;
- emits `-32022` with the supported-version list on an unsupported version;
- still answers legacy `initialize`, **capping that path at `2025-11-25`**.

Olympus MUST NOT suppress either era. A modern client negotiates `2026-07-28`, a
legacy client gets `2025-11-25`, and both are served.

#### The legacy cap is correct

`2026-07-28` deprecates `initialize` itself, so an `initialize` request *is* the
client selecting legacy semantics. A dual-era server picks its era from how the
client opens, which is what the SDK does.

#### Three sharper details the conformance tests depend on

- **`server/discover` is registered unconditionally but SERVED conditionally.**
  A request that does not itself declare `2026-07-28` or later gets
  method-not-found. That lets a client probe an older server and learn it is
  legacy, rather than getting a confusing partial answer.
- **A modern request carries its whole identity in `_meta`**, not just a
  version. The client capabilities key is **required**, and a request omitting
  it is rejected as invalid params rather than defaulted. There is no handshake
  to have carried it earlier, which is why it must ride on every request.
- **`-32022` applies only within the modern era.** A version string ordering
  *below* `2026-07-28` is not a malformed modern request. It is a legacy-era
  request, and the legacy gate handles it. Only an unknown version at or above
  the modern revision produces the unsupported-version error.

#### The advertised capabilities MUST be explicitly empty

The SDK advertises `{"logging":{}}` when capabilities are left unset. Olympus
MUST override that with an explicit empty set: logging is deprecated (§15.5),
and a client must not be told this server offers it.

#### Discover advertises every revision the SDK knows

The stdio transport declares no version restriction. It does not implement the
SDK's optional protocol-version-supporter interface, so every revision the SDK
knows, including `2026-07-28`, is advertised by discover.

### 15.4 Statelessness is the protocol's model, not only ours

Non-negotiable #4 ("no daemon, no persistent state") and the modern era's
stateless request model agree, and both depend on that agreement.

Every tool handler MUST be self-contained: no backend handle cached per session,
no state keyed by connection, nothing assuming a prior call happened. This is
the same property §6.7 demands of detached runs, reached from a different
direction.

Do not introduce session-scoped state to make a tool feel more convenient. It
breaks the transport model and the run contract at once.

### 15.5 Deprecated features Olympus MUST NOT adopt

**Roots, sampling and logging are all deprecated as of `2026-07-28`**
(SEP-2577). They remain functional during a deprecation window of at least
twelve months, which is what makes them a trap: they work today and are dead
ends.

Olympus is a pure tool server. It MUST NOT depend on any of them, and MUST NOT
emit MCP log notifications. Diagnostics go to **stderr**, which the stdio
transport leaves alone.

### 15.6 Tool surface

- **Typed parameters and results**, so the SDK generates JSON schemas and
  populates structured content. Hand-marshalled untyped results are a regression
  from what the SDK gives for free.
- **The door translates; it does not decide.** Tool names and result shapes
  mirror the ergonomic layer and the CLI. A default invented here is a second
  contract.
- **Instructions MUST be set** on the server. With no handshake, discover's
  instructions are how a modern client learns what this server is for. Leaving
  them empty removes the only description a stateless client receives.
- **A version tool MUST exist**, reporting the same literal the server identity
  carries, so a consumer can floor-check without shelling out.
- **An operation failure is a tool error carrying the §12 code**, never a
  JSON-RPC protocol error. Protocol errors are reserved for protocol problems.
  Conflating them makes a session that was fine look broken. Its first text
  content is `CODE: message`. An `AGENT_BLOCKED` whose text was typed (§7.5)
  adds a content of its own reading `typed: true`.

The registered surface is 34 tools, pinned in `ToolNames` in
`internal/mcp/tools.go` and listed in api §1.

### 15.7 Conformance requirements

The MCP door's tests MUST assert:

1. `server/discover` advertises `2026-07-28`.
2. A modern-era request (per-request `_meta`, no handshake) completes a real
   tool call end to end.
3. A legacy `initialize` still negotiates `2025-11-25` and serves the same tools.
4. An unknown requested version yields `-32022` carrying the supported list.
5. No advertised capability includes a deprecated feature (§15.5).
6. The registered tool list is pinned, so a tool cannot silently appear or
   vanish.

Assertion 3 is not optional politeness. Most deployed clients are still legacy,
and a change that breaks them would otherwise pass a modern-only suite.

---

## 16. Testing requirements

These apply beyond §2.9's isolation rules.

### Warm the shell before timing-sensitive assertions

Block until the shell has **provably** executed a command, by re-sending a probe
until its *expanded* output appears. The probe MUST be expansion-based: the
typed line shows the format string verbatim, so only the substituted output
proves execution rather than echo.

#### Why

Several behaviors are exercised by typing into a session created milliseconds
earlier, then polling a fixed deadline. Under load the login shell may not be
reading input yet when the keys arrive, so a one-shot send is lost and the
deadline expires. This surfaces as a flake that rotates between tests rather
than reproducing in one.

#### The probe MUST use the atomic submit of §4.7

The probe MUST NOT be composed out of injection and a separate terminator.

- **No production caller composes those two.** Every inject-then-submit path
  goes through one verb that owns its terminator and retries it (§4.4). A
  harness composing them would prove a path nothing ships, and the conformance
  suite exists to exercise the operation callers actually use.
- **Atomic delivery is the only shape that is safe to re-send.** This probe is
  retried until its expansion appears, and §4.7 guarantees that a retried
  invocation leaves no typed-but-unsubmitted line for the next attempt to
  concatenate onto.

### Assert the substituted output, never the typed string

PTY echo paints typed bytes onto the screen, so asserting on a literal string
proves only that it was typed. Use `printf 'marker-%d\n' 42` and assert on
`marker-42`.

#### The negative assertion too

For "not executed", the trap is counting rather than matching. "The text appears
once, so it was not executed" measures how many times the text was *typed*, and
§7.4 licenses two: a verified send whose first window is lost to load resends
the same text, and the second copy on the input line reads as an execution that
never happened.

Measured on meja under load: a line holding
`echo unsubmitted-markerecho unsubmitted-marker` failed a did-not-submit
assertion with nothing ever submitted.

Assert that the expansion is ABSENT instead. That is the only evidence execution
leaves, and it is unaffected by how many times the source line was typed.

### A probe that must survive a resend is ONE simple command

§7.4's resend types the same text a second time onto a line that still holds the
first (§4.4), so the shell runs the concatenation.

| Probe | Doubled output | Newest marker |
|---|---|---|
| a sequence: `sh -c 'exit 3'; echo MARK:$?` | `MARK:3sh -c exit 3`, then `MARK:0` | `MARK:0`, a code no command returned |
| one simple command: `printf 'MARK:%d\n' 3` | `MARK:0`, `MARK:0`, `MARK:3` | `MARK:3`, the requested one |

A doubled sequence ends in a complete trailing copy of its LAST command, which
runs on its own. A doubled simple command glues into its argument list instead,
where the assertion still holds. Measured identically in sh, bash and zsh.

### Two captures of a live session: retry the PAIR

A rule that compares captures (history against viewport on a native-scrollback
backend, for instance) asserts a property of one screen but reads two, and the
session repaints between them: a prompt lands after the last line of output, a
row is redrawn. Settling before the first read says nothing about the second.

Take both, retry the pair while they disagree, and fail only when no pair agrees
within the budget. One disagreeing pair is the race. A whole budget of them is
the defect.

### Anchor sessions on a shared tmux socket

Killing the last session on a socket tears down the whole server, so tests that
kill sessions MUST keep an anchor session alive.

### Assert plausibility for environment-dependent fields

The shell binary differs across environments (`sh`, `bash`, `zsh`), and a wrong
tmux format variable expands to empty *with exit 0*.

- Assert `created_at` unconditionally against a plausible epoch window, rather
  than gating on non-zero.
- Assert `current_command` non-empty, rather than equal to a fixed string.

### Race-shaped fixes need reproducing tests

§2.2's chained option ordering and §8.5's inode re-verification both fail
*intermittently* when reverted. A test that passes once against the fix proves
nothing. It must reproduce the interleaving.

---

## 17. Reserved identifiers, isolation, and defaults

Everything Olympus writes into a shared namespace (a backend's session list, a
tmux server's option tables, a temporary directory) is a name other software can
collide with. This section is the single registry of those names and of the
tunable values used above.

### 17.1 Reserved names

Olympus MUST use these and only these, and MUST NOT invent per-door variants.

| Name | Shape | Used for |
|---|---|---|
| tmux socket | `olympus` (default, overridable by name or path) | §17.2 |
| herdr socket | `<temp>/olympus-herdr/herdr.sock` (overridable by path) | §17.2 |
| herdr state directory | `<socket dir>/<socket stem>-state` | §2.9 |
| herdr metadata source | `olympus` | §13.1 |
| herdr metadata token | `status` | §13.1 |
| tmux buffer | `olympus-<pid>-<counter>` | per-call injection buffer (§4.1) |
| tmux key table | `olympus-passthrough` | view wheel and click bindings (§9.3) |
| tmux server marker | `@olympus_managed` | records a server Olympus started (§17.5) |
| view session | `olympus-view-<base>-<nonce>` | grouped views (§9) |
| throwaway run session | `olympus-run-<pid>-<nonce>` | §6.10 |
| run sentinels | `OLY_S_<id>` / `OLY_D_<id>_<code>_` | §6.1 |
| run/command id | `<pid><counter><8 hex>` | §6.1, §6.7 |
| lock file | `<backend>-<session>-<hash>.lock`, inside the lock directory | §11.1 |
| lock directory | `<temp>/olympus-locks`, mode 0700 | §11.1 |
| attach guard pidfile | `olympus-attach-<hash>-<session>.pid` | §8.5 |
| attach resize control | `\x1b]olympus;resize;<cols>;<rows>\x07` | §8.3 |
| attach go control | `\x1b]olympus;go;<target>\x07` | §8.3, §8.10 |
| attach focus control | `\x1b]olympus;focus;<pane>\x07` | §8.3, §8.10 |
| herdr client tag | `olympus-client-<16 hex>` | a bare client on a server that moves one client's view, where the caller names no tag of its own (§8.10, §13.5) |
| follow sink | `<temp>/olympus-follow-*` | tmux output tap (§5.6) |

#### The view-session prefix MUST NOT change

Enumerating views (§9.5) selects on the prefix, so changing it orphans every
view created by an older binary.

#### Two names carry no `olympus-` prefix

- **The lock file** already lives inside `olympus-locks/`.
- **The run id** is embedded in a sentinel marker that carries its own prefix.

Repeating the prefix would add length to a name whose length is budgeted (§2.5)
without adding any separation.

### 17.2 Isolation posture differs by backend, and users MUST be told

This asymmetry is sharper because the default backend is zmx (§0.1).

| Backend | Default posture | Visible to the operator's own tools |
|---|---|---|
| tmux | Olympus's **own socket** | no: invisible to a plain `tmux ls` |
| zmx | the operator's live daemon; there is **no socket equivalent** | yes: in their `zmx list` |
| herdr | Olympus's **own socket path** | no |

No posture is wrong, but they are opposite, and a user who learns one will be
surprised by the other. The diagnostic (§0.6) MUST report which is in effect and
where.

#### tmux

Olympus never touches the operator's default tmux server unless explicitly
pointed at it.

tmux addresses a server two ways, and they are NOT interchangeable:

- **A socket name** is resolved by tmux inside a per-user directory it chooses.
- **A socket path** is used verbatim.

Both MUST be offered. The name is the familiar form. The path lets the socket
live somewhere the caller controls: a project directory, a mounted volume, a
directory with tighter permissions than the shared one. A path also means the
socket disappears with the directory holding it, which a name does not, since
killing a server does not unlink its socket file.

The two MUST NOT collapse to one identifier. Whichever form is in effect is what
a lock key and the diagnostic identify the server by, and a name and a path are
different servers whose sessions cannot see each other.

#### zmx

Sessions are global to one daemon per user, selected by environment (§2.9). So
Olympus shares the operator's live daemon, and its sessions appear in the
operator's own `zmx list` alongside everything else.

#### herdr

Olympus never uses the operator's server by default. A session Olympus created
in somebody's live herdr would appear in their workspace list and their sidebar,
a change well outside the target they named. So the posture matches tmux's, not
zmx's.

Pointing `--socket-path` at the operator's socket is how a caller opts into the
other posture. That opt-in is a supported mode rather than an escape hatch, and
§2.9.1 says what changes when it is taken: the server is driven and never
started, reconfigured or stopped.

A socket NAME is not offered, because herdr addresses a server by path only. The
path decides more than which server answers. The configuration and state
directories are derived from it (§2.9), so it also decides which `config.toml` a
server Olympus starts reads and where the saved layout lands.

### 17.3 Default values

One place decides these. A door that invents its own has created a second
contract.

| Value | Default | Rule |
|---|---|---|
| backend | `zmx`, falling back to `tmux`, then `meja`, then `herdr` | §0.1, §0.3 |
| tmux socket | `olympus` | §17.2 |
| herdr socket | `<temp>/olympus-herdr/herdr.sock` | §17.2 |
| herdr server start deadline | 20s, env-overridable | §17.2 |
| herdr capture/poll window cap | 1,000 lines | §6.4, §6.7 |
| tmux `history-limit` | 50,000 lines | §17.5 |
| spawn `TERM` | `xterm-256color` | §1.1 |
| spawn `LANG` | `en_US.UTF-8` when unset | §1.1 |
| zmx spawn registration deadline | 15s, env-overridable | §2.4 |
| zmx session-name budget | 103 bytes of path | §2.5 |
| graceful kill: presses / gap / poll / timeout | 1 / 150ms / 150ms / 2s | §2.8 |
| submit settle gap | 150ms | §4.5 |
| capture window: start / growth / cap | 200 lines / ×4 / 10,000 | §6.4 |
| detached poll window | 10,000 lines (tmux; ignored on zmx) | §6.7 |
| run timeout / poll interval | 60s / 250ms | §6 |
| verified-send per-attempt budget / poll | 5s spent twice / 100ms | §7.4 |
| verified-send needle length | 24 normalized characters | §7.1 |
| screen-wait timeout / interval | 30s / 250ms | §5 |
| write-lock wait | 10s, env-overridable | §11.1 |
| attach steal wait | 3s, polled every 50ms | §8.5 |
| attach initial size | 80×24 | §8 |
| follow poll interval | 50ms | §5.6 |
| write-lock retry interval | 25ms | §11.1 |

Two are **per-attempt, not total**:

- The verified-send budget is spent twice (§7.4).
- The graceful-kill timeout bounds only the poll phase, so total wall time is
  `presses*gap + timeout` (§2.8).

Env-overridable values MUST be read at call time, never cached at process start.
This is the same rule §1.1 applies to `LANG`, for the same reason.

### 17.4 What Olympus deliberately does not do

Recorded so they are not re-proposed as missing features:

- **No command registry.** Statelessness is a design constraint (§6.7).
- **No pane splitting, and no windows.** See below.
- **No embedded multiplexer, and no PTY-only degraded mode.** §0.7.
- **No Windows target.** The attach path is Unix-PTY-bound.
- **No default exit marker.** A fixed default invites collision (§14).

#### No pane splitting, and no windows

Every session Olympus creates is single-window and single-pane, which is the
only reason §9.4's side effect is unobservable. Windows and panes are *reported*
(every pane row carries its window index) and never created: there is no verb,
no tool and no method that makes either.

| Backend | Windows |
|---|---|
| tmux | yes |
| meja | yes |
| herdr | yes: its tabs are the windows (§3.6) |
| zmx | neither windows nor panes; its pane row is synthesized from the session, so the window index is always 0 |

What follows when somebody else adds a window is §10's business.

### 17.5 A private socket is not a private configuration

tmux fixes a server's configuration **at boot**, from the operator's
`tmux.conf`. The socket only decides *which* server that is. A backend addressed
by `-L olympus` or by `-S <path>` therefore inherits every line of the
operator's configuration. This is measurable:

| option | server on a private socket | `-f /dev/null` |
|---|---|---|
| `history-limit` | whatever the operator set | 2000 |
| `mouse` | whatever the operator set | off |

Most of that inheritance is **wanted**. A session Olympus drives is still a
terminal a human may end up sitting in (§0.8), and someone who attaches should
find their own prefix, bindings and theme. Olympus MUST NOT take those away.

#### The two options Olympus's correctness rests on

- **`default-command`** chooses the shell a session's pane runs, and the run
  protocol's exit marker (§6.2) is written *by that shell*. Under `csh`,
  `echo "OLY_D_<id>_$?_"` becomes `OLY_D_<id>_1`: `csh` reads `$?_` as "is the
  variable `_` set", so the real exit status is replaced by a `1` and the
  closing delimiter disappears. A caller is then told a command that failed with
  3 succeeded, or the marker never parses and the run reports a timeout for a
  command that finished.
- **`history-limit`** decides what a capture of N lines can actually return
  (§5.2). Unpinned, the same request reads a different depth on every machine,
  and a truncated history is indistinguishable from a short session.

#### Olympus configures only servers it STARTS

Olympus MUST pin these on a server it is starting, and MUST NOT pin them on one
that is already running. The test is whether anything is listening before the
create runs.

No state has to be kept to remember the decision. A second create on Olympus's
own server finds it already up and skips the pins, which is correct, because the
first create's pins are server-global and still in force.

##### Why

Both options are set with `set-option -g`, which reaches **every session on the
server**. On a server the operator already runs, a caller who asked Olympus to
drive one session would have all their other sessions changed underneath them,
an effect well outside the target they named (§0.4). Disclosure explains an
action; it does not change who bears it.

#### What the pins are

- **`default-command` is pinned to empty.** That restores tmux's own behaviour,
  the operator's login shell, so what is removed is only a config file's ability
  to substitute a *different* shell behind Olympus's back.
- **`default-shell` is deliberately NOT pinned.** tmux has no notion of a
  non-interactive pane, so pinning it would hand a human who attaches a bare `sh`
  prompt instead of their own shell. That is a real cost to one audience for a
  guarantee it does not deliver, since a login shell may be non-POSIX either way.
  That the run protocol assumes a POSIX-compatible shell is stated here and
  reported by the diagnostic, not enforced by confiscating the operator's shell.

#### The pins MUST come first, in the same invocation

The pins MUST be applied ahead of the command whose behaviour depends on them,
in the same invocation. A pane reads `default-command` and `history-limit` when
it *spawns*. Applying them after `new-session` configures the next session and
leaves this one as misconfigured as before: a fix that measures as working while
fixing nothing.

#### The pins MUST be options, not `-f`

- **`-f` is ignored on a running server.** Configuration is per-server and fixed
  at boot.
- **`-f` cannot reproduce tmux's configuration search order**, which prefers the
  XDG location over `~/.tmux.conf`. Replacing the file would mean
  re-implementing that order and getting it wrong on some machine.

#### Ownership MUST be recorded, never inferred

A server Olympus starts MUST be marked, with `@olympus_managed` on the server
scope, in the same chain that starts it. A server Olympus merely finds never
receives the mark, because that chain never runs there.

The mark is a user option, which tmux stores and never acts on, so a server that
carries it behaves no differently for having it.

##### Why

Inferring ownership by comparing the pinned VALUES fails on the most likely case
rather than an exotic one. An operator who sets a large `history-limit`
themselves, an ordinary thing to set, would have a server Olympus never touched
reported as one Olympus started and configured.

##### There is a race

A server can be started by somebody else between Olympus's check and its
`new-session`, and the pins would then land on theirs. The window is narrow, and
the outcome is no worse than applying the pins unconditionally.

#### What this does not cover

Hooks and plugins in the operator's configuration run when the server boots,
before any Olympus command can intervene. A `session-created` hook therefore
executes in Olympus's sessions, and a plugin manager loads into Olympus's server.
Only replacing the configuration file outright would prevent it, at the cost
above. Callers needing that isolation MUST boot the server themselves with `-f`,
on a socket of their own.

On a server Olympus did not start, `history-limit` is whatever that server was
given, and `default-command` may name a shell the run protocol cannot read an
exit code through. Neither is corrected, and both are **reported**. `doctor`
states whether the answering server was started by Olympus and what the two
options are actually set to, not what Olympus would have pinned. Only the
effective values decide how a run behaves, which is why the report shows them.

#### Pinning MUST be disclosed

A tool that silently overrides a line in somebody's `tmux.conf` turns "my
configuration is being ignored" into an unanswerable question, which is the
failure the diagnostic (§0.6) exists to prevent. `doctor` names every pinned
option and its value, in both output modes.

Backends with no configuration file pin nothing, and MUST report nothing rather
than an empty claim.

#### herdr inverts this section's premise, and still discloses

herdr's configuration follows its configuration DIRECTORY, and §2.9 has already
moved that directory alongside the socket. So a private socket here IS a private
configuration, which tmux cannot give. Nothing of the operator's is inherited
and nothing of theirs is overwritten.

Two options are still pinned on a server Olympus starts, and still disclosed.
Both turn off a background NETWORK check the server would otherwise run at boot:
one for its own updates, one for remote agent-detection manifests. Neither has
anything to do with driving a terminal.

##### Why

A tool that silently decides when a program may reach the network turns "why did
this call home" into an unanswerable question. That is the failure this
disclosure exists to prevent, regardless of whose file is being written.

##### Ordering

The ordering rule of §17.5 applies unchanged. Configuration is read at boot, so
the file MUST be written before the server that reads it starts. Writing it
afterwards configures the NEXT server and leaves this one as unpinned as before.
An existing file in that directory is left alone: a caller who put one there
chose it.
