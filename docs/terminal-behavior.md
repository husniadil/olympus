# Terminal behavior specification

This document is the normative specification for how Olympus drives terminal
multiplexers. Each rule exists because the obvious implementation is wrong in a
way that stays invisible until it costs a bug.

**Read this before touching a backend.** Implementations MUST satisfy every
`MUST`/`MUST NOT` below. The conformance suite (`backend/backendtest`) enforces
the rules observable through the `Backend` interface; the rest are enforced by
each backend's own tests and marked *(backend-local)*.

Reference versions: **tmux 3.3+** (floor — `allow-passthrough` landed there;
developed against 3.7b) and **zmx 0.6.0**. Platform: macOS and Linux only. The
default backend is **zmx**; §0 covers resolution, fallback, and the case where
neither backend is installed.

Terminology:

- **backend** — a multiplexer implementation (tmux, zmx).
- **session** — a named, addressable terminal owned by a backend.
- **target** — the string a caller uses to address a session.
- **door** — a public entry point (Go API, CLI, MCP server).
- **consumer** — whatever is driving Olympus.

---

## 0. Backend selection and preflight

Nothing below matters until a backend has been chosen and proven to exist. Both
halves are contract, because both are the first thing a new user hits.

### 0.1 Resolution order

The backend resolves from the first of these that is set:

1. An explicit selection — CLI flag, library option, or MCP parameter.
2. The `OLYMPUS_BACKEND` environment variable.
3. **The default: `zmx`.**

An unknown backend name MUST be a usage-class error (exit 2), never
unexpected-class. The caller advertised a closed set of legal values and one
corrected argument fixes it, whereas unexpected-class tells a machine consumer
"retrying will not help" — the opposite of the truth.

### 0.2 Availability preflight

Before the first backend invocation, Olympus MUST verify the backend's binary is
on `PATH` — a single lookup, no subprocess, on every code path.

A missing binary MUST surface as a backend-unavailable error whose message names
the binary, says it was not found on `PATH`, and gives the install command for
the host platform. A raw `exec: "zmx": executable file not found in $PATH` is a
contract violation: it tells a first-time user nothing about what Olympus needs.

**Installed is not reachable.** The preflight proves only that the binary exists.
A zmx daemon that will not answer, or an unreachable tmux server, is discovered
at call time and surfaces as the same error class from there. The preflight makes
the *common* failure cheap and legible; it does not guarantee the backend works.

### 0.3 Fallback applies to the default only

- **Not explicitly selected** (resolved via the default): if `zmx` is unavailable
  and another supported backend is present, fall back to it, in the order `zmx`,
  `tmux`. Refusing to start on a host with a working multiplexer installed is
  hostile for no gain.
- **Explicitly selected** (flag or environment): **no fallback, ever.** An
  explicit choice that cannot be honored MUST fail loudly. Silently running
  somewhere the caller did not ask for is worse than failing.

### 0.4 A fallback MUST be disclosed

Sessions are **backend-scoped**: they never migrate and never merge, and a
session created on one backend is invisible from the other. A silent fallback
would therefore let a user create sessions, change their installed tooling, and
find those sessions apparently vanished with nothing explaining why.

The **resolved** backend — not the requested one — MUST be observable:

- present in every structured output envelope;
- shown in human-readable listing output;
- reported by the diagnostic (§0.6) along with *why* it was chosen.

When a listing comes back empty on the resolved backend while another installed
backend does have sessions, the door SHOULD say so. An empty list that should not
be empty is exactly when a user needs to learn that backends are scoped.

### 0.5 Version floors

- **tmux ≥ 3.3** (`allow-passthrough`).
- **zmx 0.6.0** is the reference version; support is best-effort.

A below-floor backend MUST be reported by name and version rather than allowed to
fail later in a way that looks like an Olympus bug. A version probe costs a
subprocess, so it is **not** part of §0.2's hot-path preflight: run it in the
diagnostic, and at the specific call sites where a below-floor version would
misbehave silently rather than error.

### 0.6 The diagnostic is part of the contract

Olympus MUST ship a first-class diagnostic that reports, without side effects:
which backends are installed and at what version, which one resolves right now
and by which rule, whether any is below its floor, the socket or directory in use
(§17.2), install commands for whatever is missing, and a **capability matrix**
for every installed backend.

The matrix is not decoration. The backends differ substantially (§13), the
default is the less capable of the two, and a user needs one place that says so
rather than discovering it one unsupported error at a time.

This is what turns "it does not work on my machine" into one command's output,
and it is what every error in §0.2 and §0.3 points at.

### 0.7 Neither backend installed

The error MUST be a single complete message, not a failure per attempted backend.
It states that Olympus drives an existing terminal multiplexer and does not embed
one, gives the install command for each supported backend on the host platform,
and points at the diagnostic.

Olympus MUST NOT degrade to a non-multiplexer PTY here. Detach, reattach, and
durable sessions are the whole product; a mode quietly lacking them would fail
later, further from the cause.

### 0.8 Degraded operations MUST announce themselves

Some operations succeed on the resolved backend while meaning materially less
than they do on the other. These are not errors — they return something real —
but a caller unaware of the difference draws a wrong conclusion from a successful
result.

A degrading operation MUST say so once: on stderr for the CLI (never stdout,
which is the data channel), and through the result for structured doors. Known
cases, all zmx:

| Operation | What silently differs |
|---|---|
| pane listing | `current_path` is the spawn directory, frozen (§3.4) |
| pane listing | `current_command` is the spawn argv, not the live process (§3.4) |
| capture with history | the flag is accepted and changes nothing (§5.2) |
| capture | wrapped lines cannot be rejoined (§5.2) |
| capture metadata | always zero, never tracked (§5.3) |
| detached run poll | the requested window size is ignored (§6.7) |
| graceful kill | exec-spawned sessions cannot be interrupted (§2.8.1) |

Contrast with §12's `UNSUPPORTED`, which covers an operation the backend has **no
concept of** and which returns nothing at all. Degradation returns a real answer
with a narrower meaning; failing these outright would make the default backend
refuse work it can genuinely do.

Announce once per operation, never once per row — a warning per listed pane is
noise that trains users to ignore it.

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

This applies to **every** spawn path: explicit creation, idempotent ensure, and
throwaway sessions.

**`TERM` is forced** because a host running inside tmux or screen inherits a
screen-family `TERM`. A shell such as zsh, seeing a screen-family terminal, emits
its window title as the screen sequence `ESC k <title> ESC \`. A consumer that
does not interpret that sequence renders it as literal text, leaking every
command name into the pane's visible output.

**`LANG` is defaulted** because processes started by launchd have no `LANG` at
all, degrading output to the C/ASCII locale and mangling every non-ASCII byte.
The default MUST be read at call time, never cached at process start.

**Multiplexer identity is stripped** because an inherited `TMUX` makes tmux treat
the new client as a nested session, changing its behavior including locale
handling. An inherited `ZMX_SESSION` is worse: `zmx attach <name> <argv>` with it
set does **not** create or attach `<name>` — it switches the *current* session's
daemon, yanking that session's leader client over to `<name>`. Running from
inside a zmx session without this strip hijacks a live session.

### 1.2 The tmux server's global environment is a second leak

Setting `cmd.Env` on the tmux client Olympus execs is **not sufficient**. A new
tmux session's environment is seeded from the *server's* global environment,
fixed when the server booted — so if another process booted the server on this
socket, sessions Olympus creates inherit that dirty environment regardless.

`new-session` MUST therefore also pass the sanitized values per-session via
`-e VAR=VAL` (tmux ≥ 3.2, below our floor). Passing `-e ZMX_SESSION=` —
set-to-empty, not omitted — yields an empty value in the pane even against a
server whose global environment carries a poisoned one.

*(backend-local)* tmux re-sets `TMUX`, `TMUX_PANE` and forces `TERM` inside its
own panes regardless of what we pass, so the tmux backend's *observable*
guarantees are the `ZMX_*` strip and the `LANG` default only. Assert exactly that
subset; asserting the rest produces a test that passes for the wrong reason. The
full guarantee holds on zmx and on any non-multiplexer path.

### 1.3 Attach environment

The attach client builds its own environment rather than reusing §1.1's, because
an interactive attach MUST inherit the operator's real `TERM` — forcing
`xterm-256color` would misrepresent the terminal the human is sitting at.

It MUST strip `TMUX`, `TMUX_PANE`, `ZMX_SESSION`, and `ZMX_SESSION_PREFIX`, and
MUST default `LANG` per §1.1.

`ZMX_SESSION` is worse here than on the spawn path: `zmx attach <name>` launched
from inside a zmx session **ignores `<name>` entirely** and fails with
`session "<ambient>" does not exist`, where `<ambient>` is whatever
`ZMX_SESSION` held. It does not degrade — it silently retargets. Any consumer
running inside a zmx session hits this on every attach.

### 1.4 The tmux attach client needs `-u`

Without `-u`, the *client itself* — not the pane's programs — sanitizes every
non-ASCII byte to `_` before those bytes reach the consumer. The pane is fine;
the stream is not.

This is additional to §1.1's `LANG` default: `LANG` is a belt for the programs
inside the pane, `-u` is for the client. The defect hides during manual testing
from inside tmux, because the inherited `TMUX` that §1.3 strips also flips the
client to UTF-8.

---

## 2. Session lifecycle

### 2.1 Creation

Creation takes a required, backend-unique name, plus optional working directory,
initial size, and command. An empty command means the user's default shell.

Initial size on zmx is accepted for interface conformance and **ignored** — zmx
has no spawn-time sizing concept, and the PTY is sized entirely by whatever
client attaches later. Do not paper over this.

**A session that finishes before creation returns is not a failure.** Without
`remain-on-exit` (§2.7) a session takes itself down when its command exits, so a
fast-exiting command is routinely gone by the time the confirming listing runs.
Creation MUST NOT report that as an error — an ordinary short command would look
like Olympus broke. It returns the row it can honestly give: named, outcome
`created`, liveness `gone`. The caller learns both that it was created and that
it is already over.

### 2.2 tmux option ordering: chain, never a second call

Options applying to a new tmux session MUST be chained into the *same*
`new-session` invocation using tmux's `;` in-process separator — never issued as
a separate `set-option` call afterwards.

A fast-exiting command tears its window down before a second tmux invocation can
run, which then fails with `no such window`. Symptoms of getting this wrong:

- `remain-on-exit` set separately does nothing for the fastest-failing commands —
  exactly the ones a caller most wants a corpse to inspect.
- `allow-passthrough` set separately makes successful spawns return a backend
  error, because the pane died before the second invocation ran.

**Chain order matters**: `remain-on-exit` first (pin the corpse), then
`allow-passthrough`. The reverse lets an instantly-exiting pane vanish between
the two chained commands before the corpse flag lands. On any failure of the
chained line, the session MUST be killed best-effort so a half-configured session
never leaks.

This race fails *intermittently*, so a single green test run does not prove it
fixed.

### 2.3 zmx spawn must exec, not type

Spawning on zmx MUST use `zmx attach <name> <argv>`, which execs `argv` as the
session process with nothing typed.

`zmx run <name> <cmd>` *types* `<cmd>` into a login shell, echoing the command
text into scrollback. tmux hides this behind alt-screen redraw; zmx's native
scrollback shows it, putting the spawn command line into the session's own
output.

### 2.4 zmx spawn is asynchronous, and the client's exit means nothing

1. **An early attach-client exit during spawn is NOT a failure signal.** With
   stdin ignored, the client hits EOF and exits as soon as it has forked the
   daemon — routinely *before* the session appears in `zmx list`. The daemon is a
   separate, longer-lived process. The only correct check is polling `zmx list`
   to a deadline, ignoring the client's exit entirely.

2. **The registration deadline is 15 seconds**, env-overridable and read at call
   time. Three seconds is too short: on a loaded host the daemon registers the
   session *after* a tight deadline, so the caller reports failure and races a
   deadline-triggered kill against the daemon's own session creation — producing
   a live but completely untracked orphan process. 15s makes that false negative
   rare while bounding a genuine failure (bad argv, no daemon) to seconds.

### 2.5 zmx session names have a socket-path budget

The daemon places a session's socket at `<dir>/<name>` — bare name, no suffix.
`<dir>` is `ZMX_DIR` when set, otherwise `$TMPDIR/zmx-<uid>`. The rule is:

```
len(dir) + 1 + len(name) <= 103
```

(103 bytes of `sun_path` plus the NUL, i.e. `sunPathMax = 104`.)

Names exceeding this MUST be rejected up front with a usage-class error, before
any zmx invocation, naming the computed path, its length, and the budget.
Validation MUST live in the backend's `New`, so every path reaching it — create,
ensure, throwaway run session — inherits the rejection without duplication.

Without the check the failure is misleading rather than silent: zmx errors
loudly, but the spawn path deliberately ignores the spawn command's exit code
(`zmx run <name> -d` exits non-zero even on success), falls through to the 15s
registration poll, and times out into a backend-unavailable error that never
mentions the real cause.

Resolving the socket directory for *validation* differs from resolving it for
daemon selection: the daemon-selection path returns bare `$TMPDIR`, which
under-counts the budget by the `zmx-<uid>` component. Validation needs its own
resolution.

### 2.6 Idempotent ensure

Ensure makes a named session exist and be alive, reporting which of three things
happened:

- **alive → reused.** Options other than the name are ignored; the existing
  session is returned as-is.
- **present but dead → reaped.** Kill, then recreate with the given options.
- **absent → created.** Plain create.

**The reaped branch is unreachable on both shipped backends** — tmux sessions
created without `remain-on-exit` take their session with them when the pane
exits, and zmx auto-reaps immediately, so a finished session is indistinguishable
from an absent one and yields *created*. The conformance suite MUST assert this
explicitly, so a future backend that starts leaving dead rows surfaces here
instead of silently changing behavior.

Ensure itself does no locking. The **caller** holds the per-session write lock;
that is what turns two concurrent ensures of one name into a deterministic
outcome instead of a race. With locking disabled, both can observe "absent" and
both create, and the loser's outcome is backend-defined.

Options apply on the create path only, and are **not retroactive** on a reused
session (§2.7).

### 2.7 `remain-on-exit` is tmux-only and write-only

On zmx it MUST fail with an unsupported-class error immediately, before any zmx
invocation: zmx has no corpse concept, since the daemon reaps finished sessions
itself.

The rejection MUST happen in ensure *before* branching on session state, not only
inside create. Otherwise the contract becomes state-dependent: a fresh name
correctly rejects via the create path, but an already-alive session takes the
reuse branch, never reaches create, and silently accepts and ignores the flag.

On tmux the flag is observable only through the corpse it eventually leaves.
There is no way to read it off a live session and no way to change it on one. A
live session created with the flag reuses like any other; the flag becomes
visible only once the tracked command exits and leaves a dead row for a *later*
ensure to reap.

### 2.8 Graceful kill

A decision engine with injectable operations (send interrupt, probe, force kill,
sleep) so it can be unit-tested without a backend:

1. **Probe first.** An initial "gone" means the session was *already* absent →
   outcome `gone`, zero interrupts sent. Probing first is what makes `gone` mean
   "was already gone" rather than "died at some point": a gone observed only
   *after* interrupts is `graceful` instead, and without the initial probe the
   two are indistinguishable.
2. Send N interrupts up front, with a gap between presses (none before the first,
   none after the last).
3. Poll presence until the session dies → `graceful`, or the timeout elapses →
   force kill → `killed`.

The timeout bounds the **poll phase only**; total wall time is
`presses*gap + timeout`. Defaults: 1 press, 150ms gap, 150ms poll, 2s timeout.

All three outcomes are success; a transport error from any operation propagates
as an ordinary error instead. Interrupt and force-kill MUST both tolerate a
not-found error as success-shaped, since a session dying between the probe and
the call already means the desired state holds.

### 2.8.1 Interrupting on zmx

On tmux, `C-c` reaches the foreground process group normally and none of this
applies. On zmx, writing `0x03` into the session interrupts nothing, for **two
independent reasons** — conflating them leads to the wrong fix:

**Cause 1 — zmx's send path does not generate a terminal SIGINT.** A foreground
job with an ordinary default disposition (a `sleep` started by the session's own
interactive shell) survives `zmx send <target> $'\x03'` indefinitely, yet dies
immediately from `kill -INT -<foreground pgid>`. The process was willing to die;
the terminal path never produced a signal.

**Cause 2 — an exec-spawned session process inherits `SIGINT` as `SIG_IGN`.** A
session spawned as `zmx attach <name> sh -c 'trap "echo GOT" INT; …'` never fires
the trap, because a signal ignored on entry cannot be trapped or reset. For such
a process `kill -INT -<pgid>` returns success and the process survives, while
`kill -TERM -<pgid>` kills it instantly. Nothing can interrupt it with `SIGINT` —
not the terminal, not the OS.

Required behavior on zmx:

- Olympus MUST NOT use the terminal `0x03` path to interrupt. It does not
  generate a signal even against a target that would happily die.
- The interrupt MUST be delivered as `SIGINT` to the session's **foreground
  process group**, derived from the leader pid zmx's listing reports and that
  process's controlling-tty `tpgid`. A `tpgid` equal to the leader's own process
  group means the session is at its prompt with no foreground job.
- **A session running a shell** — the default and common case — then behaves
  exactly as on tmux.
- **A session exec'd directly onto a non-shell argv** hits cause 2, and no
  interrupt is possible. Graceful kill MUST fall through to force-kill, which
  works. This is a property of how zmx spawns, not something Olympus can route
  around at kill time.

Resetting `SIGINT` to `SIG_DFL` immediately before `exec` would fix cause 2, but
requires an exec shim between zmx and the target argv. Out of scope for v1;
recorded so it is not re-derived.

The conformance suite MUST assert outcomes **per backend and per session shape** —
shell-backed sessions graceful on both, exec-spawned argv sessions graceful on
tmux and force-killed on zmx — rather than papering over the difference with one
expectation.

### 2.9 Test isolation is a hard requirement

Tests MUST NEVER touch the operator's live default server.

- **tmux**: use `-L <private-socket>` per test process.
- **zmx**: there is no `-L` equivalent. Sessions are global to one daemon per
  user, and the daemon's socket directory resolves from environment with priority
  `ZMX_DIR` > `XDG_RUNTIME_DIR` > `TMPDIR`. Session-name namespacing alone does
  **not** protect the operator: however carefully named, every test session still
  lands on that one shared daemon, and test churn there destabilizes real live
  attach clients. Tests MUST set `ZMX_DIR` to a private temporary directory — for
  the backend instance *and* for every raw `zmx` verification or cleanup call.

A shared external server addressed by a bare literal name is a real collision
surface: two processes pointing at the same tmux socket name can crash the same
underlying server out from under each other.

---

## 3. Listing and liveness

### 3.1 zmx listing MUST use the long form

`zmx list --short` fails both questions listing must answer:

- It lacks the `clients` column, so attachment state cannot be computed.
- It **silently omits rows** for any session daemon that fails an internal
  1-second probe — including a live-but-busy daemon under heavy PTY output, not
  only a dead one. Using it for a liveness snapshot makes a merely slow session
  look gone and gets it wrongly reaped.

The long (default, tab-separated `key=value`) form keeps those rows, tagged with
an `err` field.

zmx has no separate session-id concept: the identity IS the name. Session ID MUST
equal session name — never the OS pid, which changes when a named session
restarts.

### 3.2 Liveness is tri-state, and the backend owns the classification

Every listed session and pane row MUST carry a liveness classification produced
**by the backend**, so consumers never parse backend-specific error strings to
make a reap decision:

- **`present`** — a live session the backend vouches for.
- **`gone`** — positive evidence of death. Safe to finalize and reap.
- **`unknown`** — the row exists but could not be confirmed this pass.
  Indeterminate; consumers MUST treat it as present for reap purposes. Never
  finalize on doubt.

| Backend | Condition | Liveness |
|---|---|---|
| tmux | any listed row | `present` |
| zmx | no `err` field | `present` |
| zmx | `err=ConnectionRefused` | `gone` |
| zmx | any other `err` (e.g. `Timeout`) | `unknown` |

`err=ConnectionRefused` is the *only* definitive death signal: zmx itself already
deleted the stale socket this pass.

Leaving this classification consumer-side is how it gets lost — a listing that
synthesizes every row as alive gives consumers no `gone` signal at all, and dead
rows survive reconciliation forever.

A tmux corpse (`remain-on-exit`) stays `present` with the dead flag set. Liveness
and deadness are different questions.

### 3.3 "No server running" is an empty list, not an error

There is nothing to find; nothing went wrong asking.

**A listing is eventually consistent after a kill.** zmx keeps reporting a
just-killed session for a fraction of a second while it tears the socket down,
and reports it with `err=Unexpected` — which §3.2 classifies as `unknown`, not
`gone`. That is the tri-state working as designed: the row is genuinely
indeterminate during that window, and a consumer that reaped on it would be
finalizing on doubt. Nothing may require the row to have vanished the instant a
kill returns; what is required is that the listing converges.

### 3.4 Pane metadata divergences

These fields exist on both backends with genuinely different meanings, and MUST
be documented at every door rather than reported as equivalent.

**`created_at` is session-granular on both backends**, for different reasons.
tmux has no per-pane birth time: `#{pane_start_time}` and `#{pane_created}` do
not exist and expand to the empty string *with exit 0*, so trusting a wrong
format variable yields a silently zeroed column rather than an error. The only
usable birth time is `#{session_created}`. zmx has a real per-row `created` field,
read directly.

A consequence: **pane id is not unique across rows** once a grouped view exists,
because a base session and its views share the same underlying window and pane,
so a full pane listing reports the same pane id for every group member. Consumers
needing one row per logical session MUST dedupe by pane id, keeping the earliest
`created_at` (the base, not a later view).

**`current_path`** is live on tmux (`#{pane_current_path}` tracks `cd` in real
time) and **static on zmx** (`start_dir`, captured at session creation and never
updated). Reading zmx's value as a live tracker reports the original directory
forever.

**`current_command`** is the foreground process's binary name on tmux — live,
tracking whatever has taken over the pane. On zmx it is not live: a listing row
for an exec-spawned session carries `cmd=<spawn argv>`, and a row for a bare
shell session carries no such field. So on zmx this reports the **spawn** command
statically, exactly as `current_path` reports the spawn directory.

Liveness-by-command heuristics ("has a real command taken over from the shell?")
therefore work on tmux only. A consumer MUST NOT read a non-empty value on zmx as
evidence that the command is still running.

### 3.5 Presence probe is tri-state and fails closed

Probe answers `present` / `absent` / `error`, deliberately distinct from
listing's binary shape where "no server running" flattens into an empty list —
indistinguishable from "server up, session absent".

The rationale is reconciliation safety: a caller polling across a flaky backend
needs "definitely gone" and "could not ask" to be different answers, so it
neither wrongly recreates nor wrongly gives up.

A named target that has never existed is `absent` on both backends **even with no
server running** — `tmux has-session` and `zmx list` against a truly absent name
each report a clean not-found, not a connection failure. The `error` arm is
reserved for genuinely unreachable backends.

Probe MUST NOT return a transport error; backend failure is the `error` state.

---

## 4. Input injection

The most expensive rules in this document. Read all of them.

### 4.1 tmux injection is buffer-based with per-call unique buffer names

Literal text MUST be injected via `load-buffer` (stdin → named buffer) followed
by `paste-buffer -d`. It MUST NOT use `send-keys -l`, which mangles special
characters and cannot carry arbitrary bytes via stdin.

The buffer name MUST be unique per call — process id plus a monotonic counter.
Two concurrent injections sharing a name race: one call's `load-buffer` clobbers
the other's text before `paste-buffer` consumes it.

### 4.2 `paste-buffer -d` deletes only on success

`-d` is **not** unconditional cleanup: tmux deletes the buffer only when the
paste succeeds. If the target pane vanished between `load-buffer` and
`paste-buffer` — a real race window — the buffer leaks forever unless the caller
issues an explicit best-effort `delete-buffer` on the failure path.

That cleanup call's own failure MUST be swallowed so it never masks the real
error.

### 4.3 Literal injection NEVER submits — on either backend

Placing text in the input line and submitting it are separate operations. The
injection primitive MUST NOT press Enter; submission is an explicit, separate
call made by the consumer.

This keeps injection symmetric across backends and composable, and it means
§4.4's retry discipline belongs to whoever issues the Enter.

### 4.4 A failed Enter after injection MUST be retried once

Once text sits in the input line, a failed Enter does not merely fail visibly — it
leaves unsubmitted text there, where the *next* injection silently concatenates
onto it, corrupting both. Any composed operation that injects then submits MUST
retry the Enter exactly once before surfacing an error.

### 4.5 The submit terminator MUST be a separate, delayed, lone write

A single write containing both text and a trailing `\r` is treated as a **paste**
by an Ink-based REPL: the terminator becomes a literal newline inside the input
box and nothing is submitted. Submitting requires a genuinely separate write
containing only `\r`, after a 150ms settle gap.

Stated at the level that matters: **the submitting terminator MUST register as a
keypress on paste-detecting consumers, never as part of a paste.** How each
backend achieves that is an implementation detail:

- **tmux**: two `send-keys` subcommands chained by a literal `";"` argv element
  into one client invocation.
- **zmx**: text write, 150ms settle, then a lone `\r` write. zmx has no
  subcommand chaining, so backend-level single-operation atomicity is
  unachievable there.

### 4.6 Paste is normalized, multi-line, and never auto-submitted

Paste lands multi-line text in the input line without submitting it. **The final
line is never submitted without an explicit separate Enter** — the cross-backend
guarantee.

*Intermediate* line execution is consumer-dependent and **not identical across
backends**:

- A canonical-mode, non-line-editing consumer (a raw pipe, `cat`) has no
  bracketed-paste awareness, so embedded newlines execute one line at a time on
  both backends.
- A bracketed-paste-aware line-editing consumer (zsh/ZLE, bash ≥ 5.1 readline,
  TUIs) diverges. tmux's `paste-buffer -p` emits DECSET-2004 framing
  (`ESC[200~` / `ESC[201~`) around the payload, so such a consumer receives the
  whole text as one un-executed paste event and intermediate lines do **not**
  execute. zmx has no bracketed-paste framing at all, so the same shell sees
  plain newlines and executes each intermediate line as it arrives.

Against a spawned zsh session, a two-line tmux paste leaves both lines
unexecuted while the same zmx paste executes the first.

tmux paste is literal injection with `-p` added to the `paste-buffer` argv —
everything else (unique buffer, delete-on-failure, error mapping) is identical to
§4.1, so no buffer leaks either way. zmx paste is a presence check followed by a
raw send with no terminator.

### 4.7 Atomic submit trades verification for atomicity

An atomic operation delivers text **and** submits it as one caller-visible unit,
so the retry unit is the whole delivery-plus-submit. A caller retrying a failed
invocation can never leave a typed-but-unsubmitted line behind to double.

Verify-then-submit cannot provide this: its Enter is a separate call, and any
cross-invocation retry re-types the text before checking, doubling it.

Atomic submit MUST NOT verify — atomicity trades away the on-screen check.
Callers needing both properties do not get them from one call, and the doors MUST
reject the combination.

Atomic submit is **single-line only**, since multi-line text has no unambiguous
submit point. The door layer validates and rejects `\n`/`\r`; the backend does
not re-check.

On zmx, caller-visible atomicity is guaranteed by holding the per-session write
lock across both writes. A failed submit write MUST return a timeout-class error
("text delivered but not submitted"), never silent success.

### 4.9 Control keys are not deliverable on every backend

A backend may accept a control key and silently not deliver it. That is worse
than refusing it: the caller sees success and waits for an effect that never
comes.

Measured by sending each byte to `cat -v` in a live session and reading back what
arrived:

| | tmux | zmx |
|---|---|---|
| printable text | delivered | delivered |
| tab, terminator | delivered | delivered |
| control letters (`c-a`, `c-x`, …) | delivered | **dropped** |
| lone escape | delivered | **dropped** |
| arrows, home | delivered | **dropped** |
| page-up, function keys | delivered | delivered |

The zmx boundary is irregular and is deliberately NOT specified further: what a
caller needs is that control keys cannot be relied on there, which the
`control_keys` capability (§13) reports. Mapping the exact set would invite
depending on it.

The consequence is concrete: an editor opened on zmx can be typed into and read,
but not saved or exited, because both are control keys. Doors MUST report this
through the capability rather than by failing the keypress, since the keypress
itself succeeds.

### 4.8 tmux eats an unescaped trailing semicolon

tmux's `;` chaining separator treats an **unescaped trailing `;` byte** in a text
argv element as a command separator rather than literal text: `-l -- "echo A;
echo B;"` lands `echo A; echo B` with the final `;` dropped, and text that is
just `;` lands nothing. Interior semicolons are untouched — only a trailing one.

Any chained `send-keys` path MUST detect a trailing `;` and escape it to `\;`.
`-l --` is also required, guarding against text beginning with `-`.

---

## 5. Screen capture

### 5.1 tmux capture flags are load-bearing and mutually constrained

| Flag | When | Why |
|---|---|---|
| `-J` | viewport captures only | rejoins a line tmux auto-wrapped at the pane's width, so a consumer matching against the text sees one logical line |
| `-e` | opt-in colors | preserves ANSI escapes; stripped by default |
| `-S -` | opt-in history | full scrollback instead of the visible viewport |

**`-J` MUST be dropped whenever history is requested.** It is correct on the live
viewport, where nothing has been wrapped and re-flowed since. Across full
scrollback it is wrong: `capture-pane -J -S -` rejoins a long line that tmux
already wrapped at capture time with its own historical continuation, silently
merging two separate scrollback lines that never appeared as one on screen.

### 5.2 zmx capture: no rejoin, opt-in colors

zmx's history command has **no `-J` equivalent**. A line hitting the session PTY's
width comes back split by a literal `\n` indistinguishable from a real newline.
Olympus passes this through unmodified — a zmx limitation inherited as-is, not
something to paper over. Consumers matching against zmx capture output MUST
tolerate a wrap-split line. (§6.4 and §7.3 both exist because of this.)

The session PTY's fallback size, when no real attach client has ever resized it,
is 24 rows × 160 columns — hardcoded in zmx upstream, and therefore coupled to
the zmx release rather than the host.

Colors are opt-in and **not** a no-op on zmx: default output has every ANSI escape
byte stripped, and the VT flag preserves them byte-for-byte.

History **is** a documented no-op on zmx, whose history command already returns
full scrollback with no separate viewport mode to opt into. Both flag states MUST
return byte-identical output, regression-guarded.

**A capture DOES reflect an in-place repaint on both backends.** This was
measured after it looked otherwise: a program that clears and redraws is
captured as it currently appears, not as it first appeared. The limitation that
actually blocks driving a full-screen program is input, not capture — see §4.9.

**Trailing whitespace does not survive identically across backends.** tmux
preserves a row's padding and the trailing space of an unterminated prompt; zmx
normalizes it away, so a REPL prompt captured as `>>> ` on one comes back as
`>>>` on the other. A pattern that *requires* a trailing space therefore matches
on one backend and silently never matches on the other. Doors MUST NOT paper
over this by re-padding — the fix belongs in the pattern (`^>>>\s*$`), and
§5.5 says so where callers will read it.

### 5.3 Alt-screen panes capture empty, by design

A pane on the alternate screen (a TUI issuing `\e[?1049h`) has no scrollback, so a
capture only re-reports the visible grid a live consumer already mirrors.

Two layers, behaving differently — state both precisely:

- **The backend's capture method** never refuses a target for being on the alt
  screen. It succeeds, and returns the visible grid. The alt-screen metadata
  flag travels beside it. This layer has no opinion.
- **The door layer** gathers metadata for every target first, and where the
  alt-screen flag is true it **drops any history request** for that target and
  discloses that it did (§0.8). It still captures.

**The door MUST NOT skip an alt-screen capture.** An earlier revision required
exactly that, reasoning that the visible grid is one "a live consumer already
mirrors". That reasoning silently assumes an attached human. Olympus serves
programs too (CLAUDE.md non-negotiable #6), and for a caller driving a
full-screen application — an editor, a pager, a TUI client — the visible grid is
not redundant, it is the **only** way to observe the program at all. Skipping it
means such a program can be started and never seen, with an empty string and no
error to explain why.

What the alternate screen genuinely lacks is scrollback, and that is the part
the door must refuse: a history request against it asks for something that does
not exist, so the request is dropped and a warning says so rather than quietly
returning less than was asked for.

zmx never reports alt-screen: its capture metadata is always the zero value, with
no subprocess run to check. This is **not** an unsupported-class error — the
caller asked a question with an honest answer on zmx ("not tracked"), so the call
succeeds with zeroes rather than failing.

### 5.4 Waiting for a pattern is LINE-oriented

Waiting matches a caller's regular expression against **each line** of the
screen, never against the whole capture as one string.

A screen is lines, and callers write line-oriented patterns: `^>>> ` for a REPL
prompt, `\$ $` for a shell. A regular-expression engine anchors `^` and `$` to
the whole text by default, so whole-screen matching makes every anchored pattern
**silently never match** — while a plain substring keeps working, which is
precisely what lets the defect ship unnoticed.

Each line is tried both as captured and with trailing whitespace trimmed. A
terminal pads rows out to the pane's width, and that padding is invisible to
whoever wrote the pattern; requiring them to know about it would make the
pattern depend on the pane's width, which they do not control.

**Patterns MUST NOT require a trailing space.** Whether one survives into a
capture is a backend difference (§5.2), so `^>>> $` matches on one backend and
never on the other. `^>>>\s*$` is the portable form, and doors SHOULD say so
where callers will read it.

The matched line is reported alongside the screen: a caller waiting on a pattern
almost always wants the line, and making them re-run the match to find it is
asking them to reimplement what just happened.

### 5.6 Following is a tap on the stream, not a capture in a loop

Following streams a session's output as it is produced.

It cannot be built out of §5.1's capture. A capture reports the pane as it looks
NOW, so anything printed and scrolled past between two polls is simply gone —
which is exactly the output someone following a long build cares about — and a
program that repaints in place has no meaningful delta between polls at all.

Both backends provide a primitive for this and the backend layer uses it rather
than emulating one: tmux pipes the pane into a command, zmx tails the session.
tmux's form pipes into a COMMAND rather than a descriptor Olympus holds, so the
tap is pointed at a temporary file the reader follows; turning the tap off MUST
happen before that file is removed, or tmux keeps writing to a path that no
longer exists for as long as the pane lives.

What a follower receives is raw terminal output, escape sequences included. It
is a stream, not a rendering: a caller that wants to match on content captures or
waits instead, and one that wants a picture renders it themselves.

### 5.5 Capture metadata

Per-target metadata carries the alt-screen flag and the copy-mode scroll position
(lines scrolled up from the live bottom; 0 when not in copy mode). tmux-only; zmx
is always the zero value per §5.3.

---

## 6. Running commands

### 6.1 The sentinel protocol

Running a command in an existing session injects a sentinel-wrapped line and
polls the screen for completion:

```
echo <START>; <cmd>; echo "<DONE>_$?_"
```

The identifier baked into both markers MUST be unique across concurrent
processes, goroutines, and time — process id, per-process counter, and random
bytes.

**Quoting does not hide a marker from the screen.** The injected line echoes both
marker strings onto the pane before the shell runs it; quoting controls shell
parsing, not terminal rendering. What distinguishes the echoed command line from
the real completion is **expansion**: the echoed line shows a literal, unexpanded
`$?`, while the real DONE marker is followed by actual digits.

### 6.2 Marker parsing rules

- **DONE** is the **last** occurrence of the done marker immediately followed by
  1–3 decimal digits **and then a literal `_` delimiter**. The digit requirement
  rejects the echoed, unexpanded occurrence.

  The trailing delimiter is not decoration: without it, a digit at the start of
  the *next* captured line — a shell prompt like `12:34 $` — is absorbed into the
  exit-code digit run once newlines are stripped, so `..._0\n12:34 $` parses as
  exit code 12 instead of 0.

- **START** is the **last** occurrence of the start marker strictly *before* the
  DONE position. The command line's echo of the start marker appears before the
  real start marker's own output, so "last before DONE" selects the right one.

- **Wrap tolerance**: the raw capture is stripped of newlines exactly once into a
  search copy with a parallel index map back to raw offsets. Parsing runs against
  the stripped copy; positions are mapped back through that table to slice the
  real output region, then trimmed of one leading and trailing newline.

- **Both markers are required.** A capture window that catches DONE but scrolled
  past START MUST parse as "not found" — never a truncated or garbled partial
  match. A too-small window is therefore indistinguishable from "still running",
  and the run keeps polling until it times out. That is the correct failure mode.

### 6.3 The command MUST be validated up front

An empty or newline-containing command MUST be rejected before any injection.
Neither degradation is a timeout, which is why an explicit check is needed:

- A newline makes the shell run the fragments as separate commands. Both markers
  still echo and the run **succeeds**, silently reporting the exit code of the
  *last fragment*.
- An empty command is shell-dependent: bash hard-errors (no markers, genuine
  timeout), but zsh — macOS's default login shell — tolerates it and reports
  success with exit 0.

Rejecting up front also means no partial pane interaction happens either way.

### 6.4 The capture window grows on tmux and cannot be requested on zmx

Long-running output can scroll the sentinel markers off-screen while the command
is still producing output above them.

- **tmux**: capture history with an explicit depth request. The window starts at
  200 lines and quadruples on every miss, capped at 10,000. This deliberately
  does *not* inherit §5.1's viewport-only default — a run's markers genuinely can
  scroll away.
- **zmx**: no scrollback-window primitive exists, so polling falls back to plain
  capture every time. zmx *does* return scrollback rather than just the visible
  screen, but its depth is governed by zmx itself, is not requestable, and its
  ceiling is unknown. A command producing enough output to scroll its own
  sentinel past whatever depth zmx retains can still be missed. No workaround.

### 6.5 The target pane MUST be running a shell

The sentinel line uses `;` chaining and `$?`, both shell syntax. Pointed at a
pane whose foreground process is not a shell (`cat`, `vim`), the markers never
execute and the run times out — indistinguishable at this layer from a command
that took too long. A consumer wanting a clearer diagnosis must know
independently that the target runs a shell.

### 6.6 A session killed mid-poll is not-found, on both backends

On zmx this requires care: blanket-mapping "any history failure" to
backend-unavailable is wrong, because the deterministic `session ... does not
exist` stderr is not the fuzzy, intermittent case that class is for. Match that
substring explicitly and classify it as not-found before falling back.

### 6.7 Detached runs are stateless — the scrollback IS the state

A detached run injects once and returns an id; polling answers
`pending` / `completed` / `died` for a `(target, id)` pair, any number of times,
lock-free.

**Nothing durable is written.** No registry, no pending-command table, no disk
state. The id is baked into the sentinel markers themselves, and a caller resumes
solely by re-presenting `(target, id)` and having the poll re-scan scrollback for
the matching pair.

Consequences that MUST NOT be "fixed":

- **An unknown id and a still-pending command are indistinguishable.** Both read
  as pending, forever, until the caller's own timeout. A registry would mean
  persistent state, which is out of scope. It is the caller's job to bound how
  long it waits on an id it is not sure is real — the same way it must already
  bound how long it waits on a genuinely slow command.
- **"Completed then killed" and "died mid-command" are indistinguishable.** If
  the command finishes and the session dies before any poll observes the DONE
  marker, the marker vanishes with the scrollback. Reporting `died` is the only
  honest answer.

**The exit code field MUST be a pointer, omitted unless completed.** Pending and
died MUST NOT populate it, so the payload never carries a fake zero a naive
consumer could read as success. Consumers branch on status first.

**The detached path's window is a fixed one-shot request, not a growing loop.**
On tmux the requested depth (default 10,000) passes straight through; if
scrollback has pushed the marker beyond it, poll reports pending forever and the
remedy is re-polling with a larger window. On zmx the value is ignored per §6.4.

### 6.8 Polling answers about the command, never about the backend

Two deliberate divergences, both consequences of poll's posture — *answer the
desired-state question, never surface backend plumbing*:

- **A target that never existed answers `died`, not not-found.** Poll's question
  is about the state of a command, not the existence of a session. From a
  read-only vantage point, "the target vanished" and "the target was never real"
  are indistinguishable, so both collapse to the same answer.

  This is a real asymmetry with *starting* a detached run against a bad target,
  which touches the target — the injection fails loudly and MUST surface
  not-found normally.

- **A dead tmux server also answers `died`.** Listing maps "no server running" to
  an empty list (§3.3), and poll uses listing to distinguish pending from died, so
  a socket whose server was killed out from under it reports `died` cleanly
  instead of the backend-unavailable error every other operation gives. A caller
  cannot read `died` as "this session specifically died" versus "the whole
  backend disappeared".

### 6.9 Died detection MUST cover a corpse pane, not just a dead session

With `remain-on-exit`, a dead command's pane becomes a corpse but the **session
stays listed**, so session-level death detection alone reports pending forever
even though the command has died.

Poll MUST therefore check the per-session dead flag it already parses: when no
completion marker is found and the session **is** listed, a dead pane means
`died`, not `pending`. A no-op on zmx, which has no corpse concept.

### 6.10 Throwaway sessions

Running a command without naming a target creates a throwaway session for that
run and kills it afterwards — on success, failure, and timeout alike. It gets the
default shell, §1.1's sanitized spawn environment, and a name reserved per §17.1.

**A cleanup failure MUST NOT override the run's own result.** Report it on stderr
and return what the run produced. A throwaway session that failed to clean up is
a leak to notice separately, not a reason to hide the answer the caller asked
for.

A throwaway run MUST NOT be combinable with detaching — there would be nothing
left to poll. Reject the combination as a usage error.

---

## 7. Verified delivery

Verified delivery sends literal text, then polls the screen until that text is
observed there, resending once before failing.

### 7.1 Normalization

UI-tolerant, absorbing terminal rendering noise: lowercase, strip every rune that
is not a Unicode letter or digit (punctuation, whitespace, box-drawing and prompt
glyphs all drop), then truncate to the first 24 resulting characters.

### 7.2 Match per line, NOT across the whole screen

Normalization MUST be applied per line, never to the full multi-line capture as
one blob.

A real terminal's status or prompt banner routinely burns through 24 alphanumeric
characters *before* the pane even reaches the line containing the just-echoed
text. Normalizing the whole screen as one string truncates at the banner and
discards the line the marker is actually on, producing a false timeout even
though the text is right there one line down.

### 7.3 Also match adjacent line pairs, for wrap tolerance

Per-line matching alone regresses §5.2's wrap problem: on zmx a needle whose echo
straddles the PTY's column width comes back split by a literal newline that
cannot be rejoined, so a per-line-only matcher falsely times out on every
straddle case.

Since a needle can straddle at most one line boundary, the matcher MUST check
each line individually **and** each adjacent pair of lines concatenated. The pair
check covers every single-boundary split without reintroducing whole-screen
truncation.

**Scope caveat**: pair concatenation covers a single wrap boundary only. A needle
split across two wrap points would require a pane narrower than the ~24-character
normalized needle; sub-24-column panes are not a supported target.

tmux is unaffected — `-J` rejoins the wrap before capture sees it — but the
matcher is shared, so both backends behave identically.

### 7.4 One resend, two independent budgets

Send, poll for up to one attempt budget, and on a miss resend the **same** text
once and poll a second, independent budget. Only a miss on that second window
fails.

The failure mode guarded is a dropped or coalesced first delivery, not a garbled
second attempt. Worst-case wall time before failure is therefore **twice** the
attempt budget, and the conformance suite MUST assert that elapsed time so a
future regression cannot silently return early on the first miss.

---

## 8. Attach

Attach hands off to the backend's own attach client inside a PTY Olympus owns,
streaming stdio both ways until the child exits.

### 8.1 The presence gate is mandatory on zmx

`zmx attach <name>` on an **absent** name silently upserts it, auto-spawning a
fresh unrelated shell under that name. tmux fails cleanly in the same situation.

Attach MUST therefore probe presence first and **fail closed** — on probe errors
and "no server" as well as on confirmed absence. Without this, a race between a
session's death and an attach call fabricates a phantom session that looks
completely legitimate.

### 8.2 Interactive attach owns the outer terminal's discipline

With a TTY stdin, attach MUST put it into raw mode for the attach's lifetime and
undo everything on **every** exit path: normal detach, error teardown,
supersession, **and termination by signal (`SIGTERM`, `SIGHUP`)**.

The signal paths are easy to miss because the common ones are covered by deferred
cleanup, and a process killed by an unhandled `SIGTERM` runs no defers at all —
leaving the operator's terminal in raw mode with mouse reporting on.

**Raw mode is what makes keystroke forwarding real.** Without it the outer line
discipline interprets keys itself, turning Ctrl+C into SIGINT against the Olympus
process — a spurious detach — instead of a `0x03` byte delivered to the inner
shell. With `ISIG` cleared, Ctrl+C/Ctrl+Z/Ctrl+\ all forward inward, matching
`tmux attach` semantics. Detaching is the *inner* backend's job (tmux `C-b d`,
zmx's own key), not the outer terminal's.

**Exit MUST restore two layers**, not one:

1. The saved termios is reinstated.
2. A reset sequence is written to stdout: mouse reporting off
   (`?1006l ?1003l ?1002l ?1000l`), focus reporting off (`?1004l`), bracketed
   paste off (`?2004l`), cursor shown (`?25h`).

Without (2), an inner application that enabled mouse or focus reporting through
the PTY leaves the **outer** terminal emitting `\e[<...M` / `\e[I` junk into the
next shell prompt after detach.

The restore MUST be exactly-once across all exit paths, and a failure to enter
raw mode MUST degrade to cooked-mode behavior rather than aborting the attach.

**Piped (non-TTY) stdin is deliberately untouched**: no raw mode to restore, and
no reset bytes injected into a stream a programmatic consumer parses. That
consumer owns its own client-side terminal state.

### 8.3 Resize protocol

- **TTY stdin**: `SIGWINCH` is forwarded to the PTY — synced immediately on
  attach, then on every subsequent signal.
- **Piped stdin**: no `SIGWINCH` concept exists, so an **in-band control line** on
  stdin resizes the PTY instead:

  ```
  \x1b]olympus;resize;<cols>;<rows>\x07
  ```

  It MUST be matched byte-for-byte and stripped before forwarding — never written
  into the session. Malformed payloads MUST be ignored: a bad control sequence
  must not kill the session.

### 8.4 Attach supersedes prior clients by default

A new attach takes over from prior clients on every backend, mirroring what
`tmux attach -d` has always done. Opting out is explicit.

- **tmux**: `-d` in the attach argv; tmux's own client detaches the prior one.
  This is an argv transform at the door layer, **not** a backend interface method
  — adding it to the interface would leak a tmux-specific flag into a contract
  zmx has no equivalent for.
- **zmx**: two mechanisms, because one is not enough (§8.5).

### 8.5 zmx supersession needs both a guard and a sweep

zmx **co-attaches**: a second attach takes the session to two clients with the
first still alive and both rendering.

**The guard** governs Olympus-vs-Olympus. One pidfile per (directory-hash,
session), whose `flock` — not its content — is the exclusivity mechanism; the pid
is read only to know whom to signal.

- No holder, or a stale pidfile (dead pid, or a flock that is simply acquirable
  regardless of content — a holder that crashed between writing the pidfile and
  exiting), is reclaimed silently.
- Without steal, a live holder is an immediate conflict error, **before** the PTY
  is spawned.
- With steal, the holder's pid gets `SIGUSR1`, then the stealer polls every 50ms
  for up to 3s for the flock to free. If it never frees — holder hung, signal
  lost — the acquisition gives up with the same conflict shape rather than
  blocking forever.

**Winning the flock is not proof of holding the slot.** `unlink` detaches a name
from an inode without invalidating an fd a waiter already holds on it. A waiter
blocked on an fd opened *before* a concurrent release can win a lock on that
now-nameless inode at the exact moment a third, independent acquisition creates a
brand-new inode at the same path and "succeeds" too — two callers both believing
they hold the slot. Every acquisition site MUST therefore re-verify, immediately
after winning the flock, that the fd's `(st_dev, st_ino)` still matches a fresh
`stat` of the path. A mismatch, or a path that is simply gone, MUST restart the
whole acquisition from the top, reopening the path so it lands on whatever inode
is actually there now.

**The sweep** covers what the guard cannot see: a raw `zmx attach` an operator
started from their own terminal has no pidfile, no flock, and no signal handler.

The primitive for this is undocumented and not discoverable from `zmx help`.
`zmx detach` is listed as taking no arguments ("Detach all clients"), a session
name passed positionally is accepted and silently ignored, and running it bare
from outside a session is a no-op that still exits 0. **It resolves its target
from the ambient `ZMX_SESSION`** — the variable zmx sets inside a session's own
shell — so setting `ZMX_SESSION` explicitly from outside aims the verb at an
arbitrary session. It detaches every client and leaves the session itself alive.

Rules for combining the two:

- **Order is guard-then-sweep.** The signal lets a prior Olympus holder tear its
  own PTY down cleanly, restoring the outer terminal per §8.2; the sweep then
  covers whatever the guard cannot see. The reverse would kick the holder's
  client out from under a holder that then cleans up a PTY whose client is
  already gone.
- **The sweep is best-effort and LOUD on failure.** A failed sweep leaves prior
  clients co-attached — degraded but usable — which is not a reason to refuse the
  attach the operator asked for. But it MUST print to stderr, because a silent
  no-op here is indistinguishable from a successful steal, which is the precise
  failure this exists to remove.
- **Residual race, accepted**: the sweep is point-in-time, so a client attaching
  between the sweep and Olympus's own attach survives it. Closing that would need
  a zmx-side exclusive-attach primitive that does not exist.

### 8.6 Signalling and its accepted risks

The supersession handler's message MUST be generic ("detached: superseded"),
never "superseded by pid N". POSIX signal delivery carries no sender pid
portably, so the superseded side cannot honestly name who stole from it. The "by
pid N" framing belongs on the **stealer's** side, where the holder's pid is
genuinely known from the pidfile it just read.

The handler runs on its own goroutine, installed **before** the PTY is spawned,
so a steal landing in that window has no PTY to close yet. It can fire at any
point relative to the attach: before the PTY exists, mid-stream, or after the
attach has already torn it down on child exit. A mutex-guarded PTY handle,
populated the moment the PTY starts and closed by the handler only when non-nil,
makes every one of those windows a safe no-op rather than a nil-pointer panic or
a double-close.

**Accepted risk — pid recycling.** Between reading the pid and signalling it, the
OS could reap the original holder and recycle its pid onto an unrelated process.
This is unfixable without a handle-based signal primitive (`pidfd`), which Go does
not expose. The window is narrow, but the blast radius is *not* uniformly
bounded: a Go process without the handler treats an un-notified `SIGUSR1` as
non-fatal, while a non-Go process hits the POSIX default disposition, which is
termination. Accepted, not fixed.

### 8.7 A viewer role on zmx MUST drop resize as well as input

zmx has exactly one PTY per session shared by every client, unlike tmux's
per-view grouped sessions. A read-only viewer MUST therefore drop resize calls in
addition to keystrokes: a viewer resize on zmx physically resizes the *driver's*
terminal — a real disruption, not a self-contained no-op. A deliberately stronger
gate than tmux needs.

### 8.8 A spontaneous attach exit must still reap its view session

A view session's cleanup MUST NOT depend solely on an explicit close from the
consumer. The attach client can exit on its own — base session died, process
killed out from under it — without any close running. The exit handler MUST
independently reap the per-view session, or it leaks forever.

---

## 9. Views

Read-only grouped sessions over a base. **tmux only**; zmx has no grouped-session
concept and MUST return an unsupported-class error rather than emulating one
badly.

### 9.1 Group by immutable session ID, never by name

tmux resolves `-t <name>` against **group** names before session names. If a base
session dies but its group name lingers inside a stale view, grouping a new view
by name silently joins the wrong window set instead of failing.

### 9.2 Lifetime is independent; window and pane are shared

A view is a real, separately-killable session: killing the base leaves the view
alive, and sweeping views is the caller's responsibility.

But the window and pane are **shared** with the base and every other group
member, so copy-mode state and scroll position are shared too. Scrolling one view
moves the shared scroll position for the base and all sibling views. There is no
independent viewport per view.

### 9.3 Creating a view mutates SERVER-GLOBAL state

View creation is not a side-effect-free read. It:

- appends to `terminal-features` (the OSC 8 hyperlink passthrough entry), which
  MUST be idempotent — a second view must not duplicate the entry; and
- defines a key table via `bind-key -T`, itself server-global rather than scoped
  to the new session.

This is self-contained when Olympus owns the tmux socket it is pointed at, the
default for the tmux backend (§17.2). Pointed at an operator's real running tmux
server, the same mutations land there permanently, visible to every other client,
until the server is killed. State this plainly at every door.

**Why the hyperlink entry is needed**: tmux strips OSC 8 hyperlink escape
sequences for clients whose declared terminal lacks the `hyperlinks` capability,
and a headless PTY client never answers tmux's runtime feature probe. Without the
explicit opt-in, hyperlinks silently vanish for every consumer, with no error.

### 9.4 Focusing a view moves the BASE's active pane

A grouped view keeps its own current *window*, but the current *pane* is a
property of the shared window rather than the session. So the `select-pane` call
that focuses a view on the pane its base is showing **also moves the base
session's own active pane**.

On a single-pane session — the only shape Olympus's creation verbs produce — this
is unobservable. It becomes visible the moment a consumer splits a base session
into multiple panes themselves and then creates a view over it. Accepted, but
documented.

### 9.5 Listing views

Views owned by this backend are enumerable as `{view name, base session}`. The
base name comes straight from tmux's own `#{session_group}`: since §9.1 groups by
the base's session ID rather than a synthetic name, tmux's group-name answer for
*any* member of the group already **is** the base's real session name. No separate
lookup or bookkeeping is needed.

An empty result MUST serialize as an empty list, never null.

---

## 10. Targets and resolution

Every tmux backend operation addresses sessions through exact-match `=<name>`
syntax, which does **not** accept a bare pane id (`%0`) even though tmux's own
`-t` does. Consumers holding pane ids would otherwise have to do their own
session lookup before every call.

**The exact-match prefix does not make one target shape fit every command.** tmux
resolves `-t` against whatever the command operates on, so the scope suffix is
load-bearing:

| Scope | Target | Commands |
|---|---|---|
| session | `=<name>` | `has-session`, `kill-session`, `list-panes -s` |
| window | `=<name>:` | `set-option -w` |
| pane | `=<name>:.` | `send-keys`, `capture-pane`, `set-option -p` |

`send-keys` and `capture-pane` reject a bare session target outright, and
`set-option -w` rejects it with `no such window`. That last one is why §2.2's
cleanup rule exists in the form it does: the `new-session` at the head of the
chain has already succeeded by then, so a rejected suffix leaves a live,
half-configured session behind rather than failing cleanly.

On tmux, a target beginning with `%` MUST be resolved against a full-server pane
listing and swapped for its owning session's name before the call proceeds. Any
other target passes through unchanged. Resolution MUST live in **one** shared
place every operation calls, never duplicated per operation.

If the pane id matches no listed pane, resolution itself fails and the operation
returns not-found **naming the pane id**, not a resolved session name — there was
never a session to name, since resolution never happened. A corpse pane (§2.7)
is a listed pane and MUST still resolve: resolution answers which session owns a
pane, not whether that session is healthy, and collapsing the two would turn
every died-session question into not-found before the caller's own death
handling could report it properly.

A pane id can match **more than one row**. A base session and its views share
the same underlying pane (§3.4), so resolution MUST select the base — the
earliest `created_at` — and not merely the first match. Resolving to a view
means operating on the wrong session, and killing one leaves the real session
running.

Resolution MUST NOT flatten a failed listing into not-found. "Could not ask" and
"definitely gone" have to stay distinct for the same reason §3.2 gives, so the
listing error propagates with its own code.

An empty target reaching resolution is `USAGE`. It cannot pass through: an empty
string compares equal to nothing and would key a write lock of its own, which is
precisely the mismatch this section exists to prevent.

On zmx there is no pane-id concept, so a `%`-prefixed target is just an unknown
session name under the ordinary lookup: still not-found, and it MUST NOT crash.

Any caller that compares a target against a session name — or keys a lock on it —
MUST resolve first, or a pane-id caller silently mismatches every name. This is
the source of false "already gone" and false "died" reports.

---

## 11. Concurrency

### 11.1 The per-session write lock

Concurrent writers to one session MUST serialize through an advisory,
`flock`-based, per-session lock.

- **Key derivation** is the (backend, socket-or-directory, session) triple:
  **hash the whole triple**, sanitize the session name (keep `[A-Za-z0-9._-]`,
  replace everything else with `_`) for a readable prefix, and place the lock
  file under a private temporary directory with mode 0700. Two different
  sockets, directories, or backends MUST never contend on the same lock file
  even when a session name collides.

  The hash MUST cover the session name, not only the socket or directory.
  Sanitizing makes a name a *safe* path component but not a *unique* one:
  `my build` and `my_build` sanitize identically, so a name-only-sanitized key
  makes two unrelated sessions share a lock. The visible symptom is not merely
  over-serialization — it is a `CONFLICT` raised against a caller about a
  session it never touched.
- **Advisory only.** `flock` is cooperative: only other Olympus processes going
  through the same path observe it. A human typing in a raw `tmux attach`, or any
  non-Olympus writer, is unaffected and can still race.
- **Contention is a conflict-class error**, after polling for the configured wait.
- **The target MUST be resolved before it is used as a key** (§10). A pane-id
  caller and a session-name caller addressing the same session would otherwise
  take two different locks and not serialize at all.

Operations that MUST take the lock, and the scope each holds it for:

| Operation | Lock scope |
|---|---|
| literal text send | the send |
| key send | the send |
| paste | the paste, plus the optional trailing submit |
| verified send | send → verify → submit, as one section (§11.2) |
| atomic send | both writes, on backends needing two (§4.7) |
| ensure | the whole check-then-create decision (§2.6) |
| run (sync) | the injection only, released before polling (§11.2) |
| run (detached start) | the injection only |

Operations that MUST NOT take it: every read (list, probe, capture, capabilities,
pane listing), and detached-run polling. A read that blocks on a writer's lock
turns observation into contention, which is backwards — observing a busy session
is the case that matters most.

Opting out MUST be possible for a caller that already serializes its own writes,
and MUST be explicit.

### 11.2 Lock scope is per-operation, and the two rules are opposites

These cases look analogous and are not. Getting either backwards is a real defect.

- **Verified delivery holds the lock across send → verify → submit as ONE
  critical section.** It MUST NOT release and reacquire between verification
  succeeding and the Enter being sent. A competing writer landing in that gap —
  another send clearing the line, a resize — invalidates exactly what verification
  just confirmed, so the Enter would submit something other than what was
  verified.
- **Running a command releases the lock BEFORE polling.** Only the injection needs
  to be atomic with respect to concurrent writers; the polling phase only reads.
  Holding the lock across the whole wait would block every other writer against
  the target for the full timeout, for no benefit.

The distinguishing question: *does the phase after the lock gate a subsequent
write whose correctness depends on the observed state still holding?* If yes,
hold. If it only reads, release.

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
| `UNEXPECTED` | 1 | Anything not carrying one of the above. |

Two distinctions that MUST be preserved:

- **`UNSUPPORTED` is not `BACKEND_UNAVAILABLE`.** Unsupported means the question
  does not apply to this backend (views on zmx). Unavailable means a backend that
  *has* the concept could not be reached. Consumers should branch on a
  capabilities query rather than on the unsupported error.
- **`UNSUPPORTED` is not "absent".** A tmux server-environment key that is unset
  answers *present: false* — asked, and got a real negative answer. zmx answers
  unsupported: the question itself does not apply.

`UNEXPECTED` is what a machine consumer reads as "Olympus broke, retrying will not
help." Any error a caller could have avoided by changing one argument MUST
therefore be `USAGE` — including an unknown backend name.

**Every error, including usage errors, MUST reach the door's structured output.**
A caller MUST NOT have to know which internal layer caught a failure in order to
know whether the failure is machine-readable.

### 12.1 Process exit codes, and the two operations that deviate

For every operation the process exit code is the code from the table above. **Two
deviate, deliberately, and both MUST be documented at the door.**

**Running a command.** A completed run has two independent outcomes that must not
be conflated: whether the sentinel protocol worked (Olympus's concern) and what
the command's own exit code was (the caller's concern). `0` is a normal result for
a failing command not to produce and `1` a normal result for it to produce;
neither is an Olympus failure.

- **Human path**: the process exits with the *command's own* exit code, composing
  in a shell pipeline exactly like running the command directly. Genuine
  infrastructure failures still use the table.
- **Structured path**: the process exits `0` for any successful protocol run
  regardless of the command's exit code, which is carried in the payload instead.
  Infrastructure failures still use the table and the error envelope.

This asymmetry MUST be a local special case at the run door, never taught to the
shared error-to-exit-code mapping. That mapping translates failures; a successful
run carrying a second, unrelated exit code is not a failure, and making the shared
path aware of it leaks run-specific meaning into code every operation shares.

**Attaching.** Once the presence gate (§8.1) passes, attach hands off to the
backend's own client inside the PTY Olympus owns, and the process's exit code
follows *that client's*. An attach exiting `3` is therefore not necessarily
not-found — it may be the attach client's own unrelated status.

### 12.2 Usage errors MUST NOT escape through the argument parser

§12's rule has one specific failure mode, and it is why the rule is stated at all:
a CLI framework's own flag validation — unknown flags, bad values, wrong
positional arity, missing required flags, mutually-exclusive violations —
typically prints to stderr and exits *before* any application code runs.

Whether a usage-class failure is machine-readable would then depend on which layer
caught it, which is an implementation detail from the caller's side. Olympus MUST
NOT ship that split: argument-parsing errors MUST be intercepted and emitted
through the same envelope, with the same code, as a usage error the application
detected itself.

### 12.3 Absence semantics

"No server running" collapses into the negative answer, not an error, for every
question where the negative answer is meaningful:

- presence probe → `absent`
- server-environment read → `present: false`
- listing → empty list

A query against a socket with no server behind it is "nothing to find here", not
"something went wrong asking".

A target-addressed operation resolves the same absence into not-found **naming
its own target**. tmux reports it in its own vocabulary — a socket path, a pane
id, or nothing at all — and a caller holding a session name can match none of
those against what it asked for.

Detecting the condition is not one string match. tmux spells it two different
ways depending on the subcommand: `list-sessions` reports `no server running on
<socket>`, while most others fail at connect time with `error connecting to
<socket> (No such file or directory)`. Matching only the first classifies every
other verb's no-server case as `UNEXPECTED` — the exact opposite of this
section's rule, and invisible until a caller hits a verb nobody tested cold.

---

## 13. Capabilities

Static, subprocess-free backend facts a consumer feature-probes **before** hitting
an unsupported error: backend name, native scrollback, views, remain-on-exit,
server environment, control keys, alt-screen tracking.

**Control-key delivery decides whether a full-screen program can be DRIVEN**,
which makes it the most consequential entry here. Without it a caller can open
an editor, read it, and never get out of it. It is a capability rather than a
degraded-operation warning because the caller's whole approach changes: with it,
drive the program; without it, do not start.

**Alt-screen tracking is a capability because the flag alone is ambiguous.** §5.3
gives the door a rule — skip the capture where the alt-screen flag is true — and
a backend that never sets the flag is indistinguishable from one whose panes
simply are not on the alternate screen. Without a capability to branch on, a
caller cannot tell "not on the alt screen" from "not tracked", which is exactly
the ambiguity the flag exists to remove.

The name is carried on the capability value so it is self-describing in-process,
but it is **not repeated on the wire**. Every structured shape that reports
capabilities already names the backend on the row or in the envelope (api §5),
and a second copy would be a second place for the two to disagree.

Capabilities MUST NOT include whether a session outlives its command. That is a
property of the **caller's** own wrapper — does the shell it spawned keep running
after the tracked command exits — not of backend mechanics. Putting it here
misattributes a consumer-side design choice to the backend.

---

## 14. Exit-marker inspection

Parsing a caller-supplied `<marker>:<exit code>` echo out of a session that
outlives its command (the wrapper pattern `echo output; cmd; echo MARKER:$?;
sleep N`).

The marker format is **caller-supplied**, always. Olympus has no opinion on it,
and there MUST NOT be a default marker: a fixed default invites collision with
ordinary program output or stale scrollback, and weakens the caller-controlled
uniqueness the design assumes.

**The exit code is the leading whitespace-delimited token after the marker prefix,
not the whole rest of the line.** After a TUI process exits, the wrapper's echo
lands on a rendered row still carrying leftover screen content to its right,
because the exiting TUI never cleared to end of line:

```
TASK_COMPLETED:0 Esc to cancel
```

Requiring the entire remainder to parse as an integer classifies every such
legitimate exit as malformed, so the exit code stays null forever and any consumer
whose reaper treats a missing marker as "still running" never reaps anything — a
false negative in the safe direction that silently disables the feature the marker
exists for.

The token itself stays strict: `MARK:0abc` is still malformed, and mid-line
occurrences are still not line-anchored.

This answers a **content** question ("what marker, if any, is on screen"), distinct
from a detached run's **desired-state** question ("did the injected run line
finish"). The two read different evidence and neither substitutes for the other.

---

## 15. The MCP door

### 15.1 Target revision and SDK

Olympus's MCP server targets MCP revision **`2026-07-28`** and is built on the
**official Go SDK**, `github.com/modelcontextprotocol/go-sdk`, pinned at
**v1.7.0** — the first release whose latest supported revision is `2026-07-28`.

Protocol framing MUST NOT be hand-rolled. The SDK is one of the three budgeted
dependencies precisely so this door tracks the spec by upgrading a pin rather than
by editing wire code.

### 15.2 What this revision changes

`2026-07-28` is not incremental. The handshake is gone:

- **There is no negotiation handshake.** Requests are stateless and
  self-contained. Every request declares its protocol version in its `_meta`
  field, and the server accepts or rejects each request independently.
- **Servers MUST implement `server/discover`**, returning supported versions,
  capabilities, and instructions. Clients MAY call it before anything else but
  are not required to — a client may invoke any RPC inline and handle the error.
- **An unsupported requested version MUST be answered with
  `UnsupportedProtocolVersionError`** — JSON-RPC code **`-32022`** — whose data
  lists both the versions the server supports and the one requested, so the client
  can retry with a mutually supported version.
- **Optional extensions** are negotiated through an `extensions` map in
  capabilities, keyed by prefixed identifiers. If one party supports an extension
  and the other does not, the supporting party MUST either revert to core behavior
  or reject with an appropriate error.

### 15.3 Dual-era support, and why it costs nothing

The spec calls a server **modern** if it uses per-request metadata, **legacy** if
it uses the `initialize` handshake, and **dual-era** if it serves both. Olympus is
dual-era **by construction**, because SDK v1.7.0 already:

- registers `server/discover` unconditionally in the server's method table;
- emits `-32022` with the supported-version list on an unsupported version;
- still answers legacy `initialize`, **capping that path at `2025-11-25`**.

Three details of that machinery are sharper than "unconditionally" suggests, and
the conformance tests depend on all three:

- **`server/discover` is registered unconditionally but SERVED conditionally.** A
  request that does not itself declare `2026-07-28` or later gets
  method-not-found. That is what lets a client probe an older server and learn it
  is legacy, rather than getting a confusing partial answer.
- **A modern request carries its whole identity in `_meta`**, not just a version:
  the client capabilities key is **required**, and a request omitting it is
  rejected as invalid params rather than defaulted. There is no handshake to have
  carried it earlier, which is precisely why it must ride on every request.
- **`-32022` applies only within the modern era.** A version string ordering
  *below* `2026-07-28` is not a malformed modern request — it is a legacy-era
  request, and the legacy gate handles it. Only an unknown version at or above the
  modern revision produces the unsupported-version error.

**The default advertised capabilities are not empty.** The SDK advertises
`{"logging":{}}` when capabilities are left unset, for historical reasons.
Olympus MUST override that with an explicit empty set: logging is deprecated
(§15.5), and a client must not be told this server offers it.

That cap is correct, not a limitation to work around: `2026-07-28` deprecates
`initialize` itself, so an `initialize` request *is* the client selecting legacy
semantics. A dual-era server picks its era from how the client opens, which is
exactly what the SDK does.

The stdio transport declares no version restriction — it does not implement the
SDK's optional protocol-version-supporter interface — so every revision the SDK
knows, including `2026-07-28`, is advertised by discover.

Olympus MUST NOT suppress either era: a modern client negotiates `2026-07-28`, a
legacy client gets `2025-11-25`, and both are served.

### 15.4 Statelessness is the protocol's model, not only ours

Non-negotiable #4 ("no daemon, no persistent state") and the modern era's
stateless request model agree, and that agreement is load-bearing.

Every tool handler MUST be self-contained: no backend handle cached per session,
no state keyed by connection, nothing assuming a prior call happened. This is the
same property §6.7 demands of detached runs, reached from a different direction.

Do not introduce session-scoped state to make a tool feel more convenient. It
breaks the transport model and the run contract at once.

### 15.5 Deprecated features Olympus MUST NOT adopt

**Roots, sampling, and logging are all deprecated as of `2026-07-28`**
(SEP-2577). They remain functional during a deprecation window of at least twelve
months, which is exactly what makes them a trap: they work today and are dead
ends.

Olympus is a pure tool server. It MUST NOT depend on any of them, and MUST NOT
emit MCP log notifications. Diagnostics go to **stderr**, which the stdio
transport leaves alone.

### 15.6 Tool surface

- **Typed parameters and results**, so the SDK generates JSON schemas and
  populates structured content. Hand-marshalled untyped results are a regression
  from what the SDK gives for free.
- **The door translates; it does not decide.** Tool names and result shapes mirror
  the ergonomic layer and the CLI. A default invented here is a second contract.
- **Instructions MUST be set** on the server. With no handshake, discover's
  instructions are how a modern client learns what this server is for; leaving
  them empty removes the only description a stateless client receives.
- **A version tool MUST exist**, reporting the same literal the server identity
  carries, so a consumer can floor-check without shelling out.
- **An operation failure is a tool error carrying the §12 code**, never a JSON-RPC
  protocol error. Protocol errors are reserved for protocol problems; conflating
  them makes a session that was fine look broken.

### 15.7 Conformance requirements

The MCP door's tests MUST assert:

1. `server/discover` advertises `2026-07-28`.
2. A modern-era request — per-request `_meta`, no handshake — completes a real
   tool call end to end.
3. A legacy `initialize` still negotiates `2025-11-25` and serves the same tools.
4. An unknown requested version yields `-32022` carrying the supported list.
5. No advertised capability includes a deprecated feature (§15.5).
6. The registered tool list is pinned, so a tool cannot silently appear or vanish.

Assertion 3 is not optional politeness: most deployed clients are still legacy,
and a change that quietly breaks them would otherwise pass a modern-only suite.

---

## 16. Testing requirements

Beyond §2.9's isolation rules:

- **Warm the shell before timing-sensitive assertions.** Several behaviors are
  exercised by typing into a session created milliseconds earlier, then polling a
  fixed deadline. Under load the login shell may not be reading input yet when the
  keys arrive, so a one-shot send is lost and the deadline expires — surfacing as
  a flake that rotates between tests rather than reproducing in one.

  The guard is to block until the shell has **provably** executed a command, by
  re-sending a probe until its *expanded* output appears. The probe MUST be
  expansion-based: the typed line shows the format string verbatim, so only the
  substituted output proves execution rather than echo.

- **Assert the substituted output, never the typed string.** PTY echo paints typed
  bytes onto the screen, so asserting on a literal string proves only that it was
  typed. Use `printf 'marker-%d\n' 42` and assert on `marker-42`.

- **Anchor sessions on a shared tmux socket.** Killing the last session on a
  socket tears down the whole server, so tests that kill sessions MUST keep an
  anchor session alive.

- **Assert plausibility, not exact values, for environment-dependent fields.** The
  shell binary genuinely differs across environments (`sh`, `bash`, `zsh`), and a
  wrong tmux format variable expands to empty *with exit 0*. Assert `created_at`
  unconditionally against a plausible epoch window rather than gating on non-zero,
  and assert `current_command` non-empty rather than equal to a fixed string.

- **Race-shaped fixes need reproducing tests, not passing ones.** §2.2's chained
  option ordering and §8.5's inode re-verification both fail *intermittently* when
  reverted. A test that passes once against the fix proves nothing; it must
  reproduce the interleaving.

---

## 17. Reserved identifiers, isolation, and defaults

Everything Olympus writes into a shared namespace — a backend's session list, a
tmux server's option tables, a temporary directory — is a name other software can
collide with. This section is the single registry of those names and of the
tunable values used above.

### 17.1 Reserved names

Olympus MUST use these and only these, and MUST NOT invent per-door variants.

| Name | Shape | Used for |
|---|---|---|
| tmux socket | `olympus` (default, overridable by name or path) | §17.2 |
| tmux buffer | `olympus-<pid>-<counter>` | per-call injection buffer (§4.1) |
| tmux key table | `olympus-passthrough` | view scroll bindings (§9.3) |
| view session | `olympus-view-<base>-<nonce>` | grouped views (§9) |
| throwaway run session | `olympus-run-<pid>-<nonce>` | §6.10 |
| run sentinels | `OLY_S_<id>` / `OLY_D_<id>_<code>_` | §6.1 |
| run/command id | `<pid>-<counter>-<8 hex>` | §6.1, §6.7 |
| lock file | `olympus-<backend>-<hash>-<session>.lock` | §11.1 |
| lock directory | `<temp>/olympus-locks`, mode 0700 | §11.1 |
| attach guard pidfile | `olympus-attach-<hash>-<session>.pid` | §8.5 |
| attach resize control | `\x1b]olympus;resize;<cols>;<rows>\x07` | §8.3 |

The view-session prefix is load-bearing beyond cosmetics: enumerating views (§9.5)
selects on it, so changing it orphans every view created by an older binary.

### 17.2 Isolation posture differs by backend, and users MUST be told

A genuine asymmetry, sharper because the default backend is zmx (§0.1):

- **tmux**: Olympus defaults to its **own socket**, never touching the operator's
  default tmux server unless explicitly pointed at it. Sessions Olympus creates
  are invisible to a plain `tmux ls`.

  tmux addresses a server two ways and they are NOT interchangeable. A socket
  **name** is resolved by tmux inside a per-user directory it chooses; a socket
  **path** is used verbatim. Both MUST be offered: the name is the familiar
  form, and the path is what lets the socket live somewhere the caller controls
  — a project directory, a mounted volume, a directory with tighter permissions
  than the shared one. A path also means the socket disappears with the
  directory holding it, which a name does not: killing a server does not unlink
  its socket file.

  The two MUST NOT collapse to one identifier. Whichever form is in effect is
  what a lock key and the diagnostic identify the server by, and a name and a
  path are different servers whose sessions cannot see each other.
- **zmx**: there is **no socket equivalent**. Sessions are global to one daemon
  per user, selected by environment (§2.9), so Olympus shares the operator's live
  daemon and its sessions appear in the operator's own `zmx list` alongside
  everything else.

Neither posture is wrong, but they are opposite, and a user who learns one will be
surprised by the other. The diagnostic (§0.6) MUST report which is in effect and
where.

### 17.3 Default values

One place decides these. A door that invents its own has created a second
contract.

| Value | Default | Rule |
|---|---|---|
| backend | `zmx`, falling back to `tmux` | §0.1, §0.3 |
| tmux socket | `olympus` | §17.2 |
| spawn `TERM` | `xterm-256color` | §1.1 |
| spawn `LANG` | `en_US.UTF-8` when unset | §1.1 |
| zmx spawn registration deadline | 15s, env-overridable | §2.4 |
| zmx session-name budget | 103 bytes of path | §2.5 |
| graceful kill: presses / gap / poll / timeout | 1 / 150ms / 150ms / 2s | §2.8 |
| submit settle gap | 150ms | §4.5 |
| capture window: start / growth / cap | 200 lines / ×4 / 10,000 | §6.4 |
| detached poll window | 10,000 lines (tmux; ignored on zmx) | §6.7 |
| run timeout | 60s | §6 |
| verified-send per-attempt budget | 5s, spent twice | §7.4 |
| verified-send needle length | 24 normalized characters | §7.1 |
| screen-wait timeout / interval | 30s / 250ms | §5 |
| write-lock wait | 10s, env-overridable | §11.1 |
| attach steal wait | 3s, polled every 50ms | §8.5 |
| attach initial size | 80×24 | §8 |

Two are **per-attempt, not total**: the verified-send budget is spent twice
(§7.4), and the graceful-kill timeout bounds only the poll phase, so total wall
time is `presses*gap + timeout` (§2.8).

Env-overridable values MUST be read at call time, never cached at process start —
the same rule §1.1 applies to `LANG`, for the same reason.

### 17.4 What Olympus deliberately does not do

Recorded so they are not re-proposed as missing features:

- **No command registry.** §6.7 — statelessness is load-bearing.
- **No pane splitting.** Every session Olympus creates is single-pane, which is
  the only reason §9.4's side effect is unobservable.
- **No embedded multiplexer, and no PTY-only degraded mode.** §0.7.
- **No Windows target.** The attach path is Unix-PTY-bound.
- **No default exit marker.** §14 — a fixed default invites collision.
