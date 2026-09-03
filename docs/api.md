# Door contract

`docs/terminal-behavior.md` specifies how Olympus drives a multiplexer. This
document specifies what Olympus **exposes** — the vocabulary, output shapes, and
stability guarantees shared by all three doors.

The behavior spec is the authority on mechanics; where it constrains a door
(§0.4, §0.8, §5.3, §12), this document restates the constraint concretely and
never contradicts it.

**Status.** All sections are settled and binding. §6 was provisional until the
`Backend` interface existed; it now describes the shipped surface.

---

## 1. One vocabulary, three doors

Every operation has exactly **one** name, one set of options, and one result
shape. The CLI verb, the Go method, and the MCP tool are three spellings of the
same operation.

| Operation | CLI | MCP tool | Go |
|---|---|---|---|
| create-or-reuse a session | `start` | `start_session` | `Session` |
| create a session, failing if taken | `new` | `new_session` | `Create` |
| list sessions | `ls` | `list_sessions` | `Sessions` |
| list panes | `panes` | `list_panes` | `Panes` |
| kill a session | `stop` | `stop_session` | `Stop` |
| session detail / presence | `info` | `session_info` | `Info` |
| which session am I in | `self` | `self` | `Self` |
| read/set/await a status | `status` | `session_status` | `SetStatus`, `Status`, `WaitForStatus` |
| type literal text | `type` | `type_text` | `Type` |
| deliver text, confirmed, and submit | `send` | `send_text` | `Send` |
| press named keys | `press` | `press_keys` | `Press` |
| paste multi-line text | `paste` | `paste_text` | `Paste` |
| read one session's screen | `screen` | `screen` | `Session.Screen` |
| read several screens at once | `screen` (many targets) | `screen` (many targets) | `Olympus.Screens` |
| wait for a pattern | `wait` | `wait_for` | `WaitFor` |
| follow output live | `watch` | *(none — streaming)* | `Watch` |
| run a command | `run` | `run_command` | `Exec` |
| run in a throwaway session | `run` (no target) | `run_command`, throwaway set | `RunOnce` |
| start a detached run | `run --detach` | `start_run` | `Start` |
| poll a detached run | `poll` | `poll_run` | `Job.Poll` |
| attach a terminal | `attach` | *(none — interactive)* | `Attach` |
| read an exit marker | `exit-status` | `exit_status` | `ExitStatus` |
| create a view | `view create` | `create_view` | `CreateView` |
| scroll a view | `view scroll` | `scroll_view` | `ScrollView` |
| focus a pane in a view by cell | `view focus` | `focus_view` | `FocusView` |
| list views | `view ls` | `list_views` | `Views` |
| read a server env key | `server-env` | `server_env` | `ServerEnv` |
| list servers | `servers` | `list_servers` | `Servers` |
| stop a server | `servers stop` | `stop_server` | `StopServer` |
| what this backend can do | `capabilities` | `capabilities` | `Capabilities` |
| environment diagnosis | `doctor` | `doctor` | `Diagnose` |
| version | `version` | `version` | `Version` (a package variable) |
| serve the MCP door | `mcp` | *(is the door)* | — |

**The doors translate; they do not decide.** A default, validation rule, or
result field invented at one door is a second contract. Defaults live in the
ergonomic layer (behavior spec §17.3); doors pass them through.

Two operations are door-specific by nature and MUST NOT be forced into the
others: `attach` is interactive and needs a terminal, and `watch` is a stream.
MCP, being request/response over stdio, exposes neither — and those two are the
ONLY operations it does not. Everything else in this table is reachable from
every door, which is what makes the vocabulary one vocabulary rather than three
overlapping ones.

A door lacking an operation for any other reason is a bug, not a design choice,
and is worth checking mechanically rather than by eye: the table above is the
authority, and a tool missing from it is as much a defect as a tool missing from
the server.

`attach` being CLI-only, its flags have no MCP column. Not every one applies on
every backend, and an inapplicable one is refused as `UNSUPPORTED` rather than
ignored (behavior spec §8.7, §8.9):

| Flag | Go option | Meaning | Backends |
|---|---|---|---|
| `--viewer` | `AsViewer` | read-only: no input, no resize | tmux, zmx; refused on meja and herdr |
| `--keep-others` | `KeepOtherClients` | co-attach instead of displacing prior clients | all; meja and herdr's session client carry a notice where it cannot be honoured (§8.4); nothing to do under `--bare` on tmux |
| `--client` | `WithSessionClient` | the multiplexer's own session client (sidebar, tabs, selection, scroll, copy), steered onto the target first: a workspace is focused, a tab is focused within it, a pane is zoomed within its tab (§8.10). With `--server` it attaches that named session; otherwise the server on the resolved socket | herdr only |
| `--bare` | `AsBare` | a plain pane, no chrome: herdr's session client with its chrome hidden (implies `--client`), or on tmux a throwaway view onto the session, killed when the attach ends; target may be `<session>:<window>` | herdr, tmux |
| `--cols`, `--rows` | `AttachSize` | initial size when stdin is not a terminal | all |

### 1.1 Verbs are named for intent, not mechanism

`screen` rather than `capture-pane`, `wait` rather than `expect`, `stop` rather
than `kill-session`, `press` rather than `send-keys`. A person guessing a verb
should land on the right one.

The principle decides more than it first appears. Pressing keys was once `key` on
the CLI, `send_keys` on MCP and `Press` in Go — three words for one operation,
and the two that disagreed with `Press` were both borrowed from the multiplexer's
own vocabulary rather than from what the caller is trying to do. Reading a screen
had the same split, `screen` against `capture`.

**`poll` is a top-level verb, not a subcommand of `run`.** Making it
`run poll <target> <id>` would reserve `poll` as a session name — a session
literally named `poll` becomes unaddressable by `run`, because subcommand
resolution wins. Keeping `poll` top-level costs nothing and removes the trap.

`view` and `servers` are the two legitimate subcommand groups: their operations
act on views and on servers rather than on sessions, and each shares a noun.
`servers` lists when bare, since listing is what a caller reaches for first;
`servers stop <name>` is the noun's one write.

### 1.2 Targets are positional, everywhere

Every operation addressing a session takes it as the first positional argument.
No operation takes the target as a flag. Operations addressing nothing (`ls`,
`doctor`, `version`, `mcp`) take no positional.

Session names are ordinary positionals, so no verb name is reserved as a session
name — see §1.1.

---

## 2. The structured envelope

`--json` on the CLI, and the structured content of every MCP tool result, share
one envelope.

**Success:**

```json
{
  "ok": true,
  "backend": "zmx",
  "data": { },
  "warnings": [
    { "code": "DEGRADED", "message": "current_path is the spawn directory on zmx and does not track cd" }
  ]
}
```

**Failure:**

```json
{
  "ok": false,
  "backend": "zmx",
  "error": { "code": "SESSION_NOT_FOUND", "message": "session \"build\" not found" }
}
```

Rules:

- `ok` is always present and is the only field a consumer needs to branch on.
- `backend` is the **resolved** backend, never the requested one (behavior spec
  §0.4). Present on success and failure alike — a failure is exactly when
  knowing which backend answered matters most.
- `data` carries the per-operation payload, and is absent for operations with no
  payload. It is an object or an array, never a bare scalar.
- `warnings` is omitted when empty, never `null`. It carries degraded-operation
  disclosure (behavior spec §0.8) for the structured doors, where stderr is not
  available.
- `error` is present exactly when `ok` is false, and carries a code from the
  behavior spec's §12 vocabulary.

**Empty collections serialize as `[]`, never `null`.** This applies to `data`
when it is a list, and to every list-valued field inside it.

### 2.1 Why an envelope rather than a bare payload

The reference implementation emitted bare per-operation payloads (`{"sent":"demo"}`)
with errors as a separate shape. That worked because it had no cross-cutting
fields. Olympus has two — resolved backend and warnings — and both must appear on
every operation, including failures.

Adding them to every payload would mean each operation independently remembering
to; the envelope makes it structural. The cost is one level of nesting
(`jq .data.name` rather than `jq .name`), paid once.

### 2.2 Human output is a separate contract

Without `--json`, output is formatted for reading: aligned tables for lists,
plain text for screens and command output. Colour is permitted when stdout is a
TTY and forbidden when it is not; none is currently emitted.

**Human output is not stable and MUST NOT be parsed.** It may change in any
release. Scripts use `--json`. This is stated in `--help` for every operation
that has a table.

`-q` suppresses non-essential human output; it has no effect on `--json`.

### 2.3 Streams are separate

- **stdout** is the data channel: the payload, the envelope, captured screen
  content, command output.
- **stderr** is the narration channel: degraded-operation warnings (behavior spec
  §0.8), attach-steal notices (§8.5), throwaway-session cleanup failures (§6.10).

Nothing diagnostic ever goes to stdout, and no payload ever goes to stderr. A
consumer piping stdout into a parser must never have to filter it.

**`attach` and `watch` have no `--json` form, and asking for one is a `USAGE`
error.** They are the two verbs whose output IS the terminal rather than a
description of it, so no envelope can hold it:

- `attach` hands stdout to the multiplexer's client, which then owns it. Every
  byte the session draws goes there, and so does the client's own failure text —
  `open terminal failed: …` reaches stdout with stderr empty, and no layer above
  can take those bytes back.
- `watch` writes the raw output stream, escape sequences included. Wrapping it in
  an envelope would mean buffering until the stream ends, which is the one thing
  a follower must not do.

Refusing keeps the rule above absolute, and costs the caller nothing they were
getting, since that output was never parseable either way. Use `screen` for a
capture that can be parsed, and `info` to ask about a session.

The MCP door has no attach tool at all, for the same reason: a stdio transport
has no terminal to hand over.

---

## 3. Errors and exit codes

The code vocabulary and process exit codes are specified in behavior spec §12 and
are **semver-bound**: never repurposed, never removed, only added to.

| Code | Exit |
|---|---|
| `USAGE` | 2 |
| `SESSION_NOT_FOUND` | 3 |
| `BACKEND_UNAVAILABLE` | 4 |
| `TIMEOUT` | 5 |
| `CONFLICT` | 6 |
| `UNSUPPORTED` | 7 |
| `UNEXPECTED` | 1 |

Door-level obligations:

- **Every error reaches the envelope**, including argument-parsing errors. The
  CLI MUST intercept its framework's own flag validation rather than letting it
  print and exit (behavior spec §12.2). A caller must never need to know which
  layer caught a failure to know whether it is machine-readable.
- **An MCP operation failure is a tool error carrying the code**, never a
  JSON-RPC protocol error (behavior spec §15.6).
- **The Go door returns typed errors**: `errors.Is` against exported sentinels
  (`ErrNotFound`, `ErrUnavailable`, `ErrTimeout`, `ErrConflict`, `ErrUnsupported`,
  `ErrUsage`) with the code also readable from the error value.

### 3.1 The two exit-code deviations

Restated from behavior spec §12.1 because they are door-visible:

- **`run` (human path)** exits with the *command's own* exit code, so it composes
  in a pipeline like running the command directly. Infrastructure failures still
  use the table.
- **`run` (`--json`)** exits `0` for any successful protocol run; the command's
  exit code is in `data.exit_code`.
- **`attach`** exits with the underlying attach client's code once the presence
  gate passes, so an exit of `3` is not necessarily `SESSION_NOT_FOUND`.

Both are documented in the affected operation's `--help`, not only here.

---

## 4. Global options

| CLI | Environment | Applies to |
|---|---|---|
| `--backend <name>` | `OLYMPUS_BACKEND` | all (`zmx`, `tmux`, `meja`, `herdr`) |
| `--socket <name>` | `OLYMPUS_SOCKET` (MCP door only) | tmux backend only |
| `--socket-path <path>` | `OLYMPUS_SOCKET_PATH` (MCP door only) | tmux, meja and herdr backends |
| `--zmx-dir <dir>` | `ZMX_DIR` | zmx backend only |
| `--server <name>` | `OLYMPUS_SERVER` (MCP door only) | tmux, herdr and zmx backends; exclusive with the three above |
| `--json` | — | all |
| `--no-lock` | — | operations that take the write lock |
| — | `OLYMPUS_LOCK_WAIT` | operations that take the write lock |
| `-q` / `--quiet` | — | human output only |

Precedence is flag over environment over default, per behavior spec §0.1. An
unknown backend name is `USAGE`, not `UNEXPECTED`.

`OLYMPUS_SOCKET`, `OLYMPUS_SOCKET_PATH` and `OLYMPUS_SERVER` are read by the
MCP door alone. The CLI honours only `OLYMPUS_BACKEND` from the environment; on
the CLI the addressing options are flags, `--socket`, `--socket-path` and
`--server`.

`--server` selects a server by NAME — one of the rows `servers` lists — and is
resolved into the backend's own address (behavior spec §13.2): a tmux socket
name, a herdr named session's socket, zmx's one `default`. Given together with
`--socket`, `--socket-path` or `--zmx-dir` it is `USAGE`; an unknown name is
`SESSION_NOT_FOUND`; on meja it is `UNSUPPORTED`.

`OLYMPUS_LOCK_WAIT` overrides how long a writer waits for a contended session
before reporting `CONFLICT`, default 10s. It is a duration string, read at call
time, and is ignored when it does not parse or is not positive.

**An addressing option the resolved backend cannot use is also `USAGE`**, never a
silent no-op. Each of them exists to isolate — to put a server somewhere the
caller controls — so dropping one quietly lands the caller on the shared default
while they believe they are alone on a private one. The message names the option,
the backend, and what that backend does take.

`ZMX_DIR` is read by the zmx binary itself, so it applies whether or not Olympus
passes it: setting it in the environment moves every session, which is what
makes it usable for isolation (§2.9).

The Go door takes these as options to `Open`; the MCP door takes them from its
process environment, since a stateless request carries no session configuration.

`self` is the one operation none of the addressing options apply to. It answers
where the calling process *is*, not what it would address, so honouring
`--backend` or `--socket` there would let a caller's configuration contradict
the truth. It is a package-level `Self(ctx)` in Go for the same reason: a handle
cannot change which session its own process is sitting in.

---

## 5. Payload shapes

Field names are `snake_case` in JSON, and identical across CLI and MCP. These are
semver-bound once shipped.

**Status** (`status`):

| Field | Type | Notes |
|---|---|---|
| `session` | string | The session the status belongs to. |
| `status` | string | Empty when the session has never reported one. |

The same shape in all three modes — read, `--set`, `--wait` — so a caller parsing
the output needs one parser rather than three.

The value is **opaque**: Olympus stores and returns it exactly as given, defines
no vocabulary of states, and matches `--wait` exactly rather than as a pattern.
What counts as busy or blocked belongs to the program in the session, not to the
terminal. Backends that cannot carry one refuse both the read and the write with
`UNSUPPORTED`; `capabilities` reports it as `session_status`. Behavior spec
§13.1.

With no target, `status` addresses the session the calling process is running in
— and takes that session's *backend and server* from the same answer, not from
the defaults. A reporter that resolved its name but not its server would write
onto a different backend entirely on any isolated setup, and the waiter would
time out against a session that never heard anything.

**Identity** (`self`):

| Field | Type | Notes |
|---|---|---|
| `inside` | bool | Always present. False is an answer, not a failure. |
| `backend` | string | Omitted when outside, and when nested. |
| `session` | string | The name another program would use to reach this process. |
| `scope` | string | The socket or directory that session lives on. |
| `nested` | array | Every backend claiming this process, set only when more than one does. |

Being outside a session exits `0` with `inside: false`: a caller told "nowhere"
can act on it, whereas one handed an error must guess whether the error meant
nowhere or could-not-tell.

When `nested` is set, `backend`, `session` and `scope` are all empty. The
environment cannot say which session is inner — both sets of variables are
present and inheritance looks identical either way — and the use this operation
exists for is telling another program where to reply. A confident wrong address
delivers that reply to somebody else's terminal, silently.

**Session row** (`start`, `ls`, `info`):

```json
{
  "name": "build",
  "id": "$3",
  "attached": false,
  "dead": false,
  "liveness": "present",
  "cwd": "/repo",
  "outcome": "created"
}
```

`liveness` is the backend-owned tri-state (behavior spec §3.2). `outcome` appears
only on `start`, and is `created` | `reused` | `reaped`.

**Pane row** (`info`):

```json
{
  "pane_id": "%7",
  "session_name": "build",
  "session_id": "$3",
  "window_index": 0,
  "dead": false,
  "created_at": 1786778830,
  "current_path": "/repo",
  "current_command": "zsh",
  "liveness": "present"
}
```

**On herdr a session is a workspace, a window is a tab, and a pane is a pane**
(behavior spec §3.6). A session's `name` is the workspace's label where it has
one and its `id` — `w25` — where it has not; herdr labels a workspace from its
directory when nobody names it, so several workspaces opened in one directory
carry one label, and the `id` beside the `name` is how a caller addresses one
exactly. A pane row's `session_name` and `session_id` are its workspace's, and
`window_index` is its tab's number. `olympus ls` on a real herdr therefore lists
what its sidebar shows, and `olympus panes <workspace>` every pane in it:

```sh
$ olympus ls --backend herdr --socket-path ~/.config/herdr/herdr.sock
demo   w1   present
tmp    w3   present
$ olympus panes demo --json | jq '.data[] | [.pane_id, .window_index, .session_name]'
["w1:p1", 1, "demo"]
["w1:p2", 1, "demo"]
["w1:p3", 2, "demo"]
```

`current_path` and `current_command` carry different meanings per backend
(behavior spec §3.4) and trigger warnings on zmx and herdr per §0.8. Two are
worth knowing before you branch on them: on herdr `current_command` is populated
for a listing that named a target and left empty for a whole-server listing, and
`attached` is always false there because no per-terminal client count exists.
`created_at` on herdr is derived from the pane's terminal id, which is the only
creation time it publishes.

Panes and windows are **reported, never created**. No verb, tool or method makes
either: every session Olympus creates is single-window and single-pane. tmux,
meja and herdr have both concepts — on herdr a window is a tab, and
`window_index` is the tab's number; zmx has neither, and its row is synthesized
from the session, so `pane_id` is the session's name and `window_index` is
always 0.

`pane_id` works as a target anywhere a session name does, in each backend's own
spelling — `%7` on tmux, `7` on meja, `w1:p2` on herdr, the session's name on
zmx. On tmux and meja it addresses the **session that owns the pane**, not the
pane: after a second window exists, an operation still runs against the
session's active window. Behavior spec §10.1 explains why precision there would
cost the write lock.

**herdr is the exception: a target is pane-precise.** `w1:p2` acts on that pane,
`w1:t2` on the pane that tab is showing, and `w1` or a label on the pane the
workspace is showing; `stop` closes the level named, with everything in it. A
herdr session may not be NAMED like any of the three ids, and creation rejects
one that is, because a workspace with an empty label is named by its id and the
shapes have to stay apart. The trade — the write lock is keyed on the target as
given — is recorded in behavior spec §10.1.

**Screen** (`screen`): one or more targets in a single call.

```json
{
  "screens": { "build": "…" },
  "meta": { "build": { "alt_screen": false, "scroll_position": 0 } }
}
```

An alt-screen target IS captured: its visible grid is the only way to observe a
full-screen application, and `alt_screen: true` tells a caller that there is no
scrollback behind it (behavior spec §5.3). A history request against such a
target is dropped, with a warning. Both maps are always objects, never `null`,
including on the zero value a failure returns.

**Presence** (`info`): `info` carries the tri-state presence answer and **MUST NOT
error on an absent target**:

```json
{ "state": "present", "session": { }, "panes": [ ], "capabilities": { } }
```

`state` is `present` | `absent` | `error` (behavior spec §3.5). `session` and
`panes` are omitted when the target is not present. When it **is** present,
`panes` is always an array — empty if a listing raced a kill (§3.3), never
missing — because the key vanishing under a present state turns an ordinary
iteration into a crash on the rarest path. Erroring with
`SESSION_NOT_FOUND` instead would collapse the tri-state that exists precisely so
a caller can tell "definitely gone" from "could not ask" — the whole point of
§3.5. `info` is the only door onto that distinction, so it must preserve it.

**Run** (`run`): `{"exit_code": 0, "output": "…"}`.

`run` with **no target** creates a throwaway session for the run and kills it
afterwards (behavior spec §6.10). `run --detach` with no target is a `USAGE`
error, since nothing would remain to poll.

**Stop** (`stop`): `{"outcome": "gone" | "graceful" | "killed"}`.

All three are **successes**, and the distinction is the payload's whole reason to
exist: `gone` means there was nothing to stop, `graceful` means the session took
the interrupt, `killed` means it did not and was terminated. A caller reconciling
state treats all three as "not running now"; a caller reporting to a human wants
to say which happened.

**Wait** (`wait`): the capture that satisfied the wait, plus which line did it.

```json
{ "text": "…", "meta": { "alt_screen": false, "scroll_position": 0 },
  "line": "$ make build", "matched": true }
```

`line` and `matched` are omitted when nothing matched — a capture that timed out
carries its `text` and no claim about it. Matching is per line, never against the
whole screen as one string (behavior spec §7.2), so `line` is the specific line
the pattern hit rather than a slice of the screen.

**Detached run**: `start` returns `{"command_id": "…"}`; `poll` returns
`{"status": "pending" | "completed" | "died", "exit_code": 0, "output": "…", "reason": "…"}`.

**`exit_code` is omitted unless `status` is `completed`** (behavior spec §6.7) —
never a fake zero. Consumers branch on `status` first.

The identifier is spelled **`command_id` coming back and `id` going in** on the
MCP door: `start_run` returns the first, `poll_run` takes the second. Both are
shipped and therefore fixed (§7), so neither is renamed — that would break every
client that already pairs them. `poll_run` also accepts **`command_id`** as an
alias for `id`, so a caller can hand back exactly what `start_run` returned;
when both are sent, `id` wins. The CLI takes the id positionally and has no such
split.

**View row** (`view create`, `view ls`):

```json
{ "name": "olympus-view-build-a1b2", "base": "build", "id": "$9", "attached": false }
```

`base` is the session the view looks onto. A view's lifetime is independent of
its base's, but the window and pane are shared (behavior spec §9.2), so a view
row is not a session row and does not carry `liveness` or `cwd` — ask the base
for those.

`view create` takes `--name`, `--no-mouse` and `--window`; `create_view` takes
`base`, `name`, `no_mouse` and `window`; Go takes `WithViewName`, `WithoutMouse`
and `WithViewWindow`. `window` pins the view to one of the base's windows, by
index or by name, instead of the window the base is showing; a window the base
does not have is `SESSION_NOT_FOUND` and nothing is created (behavior spec
§9.4). The row does not report the window — a view keeps its own current window
and can be moved after creation, so the answer would be stale the moment it was
read; ask tmux.

**Focus result** (`view focus`, `focus_view`):

```json
{ "view": "olympus-view-build-a1b2", "col": 52, "row": 3, "pane": "%1" }
```

`pane` is the id of the pane selected under the cell, or empty when the cell
was on a border or outside every pane — a result, not an error (behavior spec
§9.6). `view focus` takes `--col` and `--row`, both required and 0-based;
`focus_view` takes `view`, `col` and `row`. The active pane is shared with the
base, so the base follows.

**Server row** (`servers`):

```json
{ "name": "work", "socket_path": "/tmp/tmux-501/work", "running": true, "default": false,
  "dir": "/tmp/tmux-501" }
```

`name` is what `--server` selects by. `running` is measured, not inferred from
the socket file. `default` marks the row the backend addresses when nothing
selects one, which on tmux and herdr is not the server Olympus itself defaults
to (behavior spec §17.2). `dir` is omitted where the backend has none to
report. What a row is differs by backend — a tmux socket name, a herdr named
session, zmx's one directory — and is specified in behavior §13.2.

**Stopped server** (`servers stop`):

```json
{ "name": "work", "outcome": "killed" }
```

`outcome` is `gone` (it was not running) or `killed`; both are successes.

**Doctor** (`doctor`):

```json
{
  "resolved": { "backend": "zmx", "reason": "default", "socket_or_dir": "/tmp/zmx-501",
                "pinned": false },
  "backends": [
    { "name": "zmx", "installed": true, "version": "0.6.0", "floor": "0.6.0",
      "below_floor": false,
      "isolation": "shared daemon in the default directory for this user; these sessions appear in your own `zmx list` alongside everything else",
      "capabilities": { "native_scrollback": true, "views": false, "remain_on_exit": false,
                        "server_env": false, "control_keys": false,
                        "spawn_sizing": false, "spawn_command": true,
                        "session_status": false, "tracks_alt_screen": false, "servers": true } },
    { "name": "herdr", "installed": true, "version": "0.8.2", "floor": "0.8.2",
      "below_floor": false,
      "isolation": "socket at /tmp/olympus-herdr/herdr.sock; its configuration and saved layout live beside it, invisible to your own herdr",
      "capabilities": { "native_scrollback": false, "views": false, "remain_on_exit": false,
                        "server_env": false, "control_keys": true,
                        "spawn_sizing": false, "spawn_command": false,
                        "session_status": true, "tracks_alt_screen": false, "servers": true },
      "managed_options": { "update.manifest_check": "false", "update.version_check": "false" } },
    { "name": "tmux", "installed": true, "version": "3.7b", "floor": "3.3",
      "below_floor": false,
      "isolation": "private socket \"olympus\"; these sessions do not appear in a plain `tmux ls`",
      "capabilities": { "native_scrollback": false, "views": true, "remain_on_exit": true,
                        "server_env": true, "control_keys": true,
                        "spawn_sizing": true, "spawn_command": true,
                        "session_status": true, "tracks_alt_screen": true, "servers": true },
      "managed_options": { "default-command": "", "history-limit": "50000" } }
  ],
  "install_hints": []
}
```

`floor` is the oldest version of that backend Olympus is supported against, and
`below_floor` is that comparison already made for the reported version.
`isolation` says, in one sentence, where that backend's sessions live and who
else can see them — which socket or directory answers, and whether the sessions
show up in the user's own plain listing.

`managed_options` is every option Olympus pins on servers **it starts**. On tmux
that overrides the operator's own configuration; on herdr it does not, because
that backend's configuration directory moves with its socket, so the file being
written is one Olympus owns. It is disclosed either way. `resolved.pinned` says whether the
server answering right now is one of them, and `resolved.effective_options`
reports what those options are actually set to there — which on a server Olympus
merely found is whatever that server was given, and is the only thing that
decides how a run behaves. A private socket is not a private
configuration — tmux fixes a server's settings at boot from `tmux.conf`, so the
operator's file reaches Olympus's sessions — and these two are pinned back
because the run protocol's exit marker and the meaning of a capture's line count
depend on them. Nothing cosmetic is pinned: keybindings, prefix and theme are
left alone, and nothing at all is pinned on a server that was already running:
`set-option -g` reaches every session on a server, so a caller who named one
session would otherwise have all the operator's others changed with it. Behavior
spec §17.5 has the measurements and the rule.

On herdr the two pinned entries are `update.version_check` and
`update.manifest_check`, both false: they turn off a background network check a
freshly started server would otherwise make, which has nothing to do with
driving a terminal and which nobody asked for. Nothing is pinned on a server
that was already answering — `resolved.pinned` is false there, and that server
never read a file Olympus wrote.

**Pointing `--socket-path` at a herdr server that is already running is a
supported mode.** It is how you drive a box's own headless herdr, or an
operator's, and read and attach to panes other tools created. Olympus never
starts, reconfigures or stops such a server: a request to stop one it did not
start is refused with `CONFLICT`, because stopping takes every pane on the
server down including every one you never named. Close the sessions you own
instead. Behavior spec §2.9.1.

`reason` names the resolution rule that applied (`flag`, `env`, `default`,
`fallback`), satisfying the disclosure requirement of behavior spec §0.4.

A backend entry carries `problem` when it is on PATH but could not be run — the
case a version-manager shim left behind by an uninstalled tool produces, where a
lookup succeeds and every call fails. Resolution is a single lookup with no
subprocess (behavior spec §0.2) and cannot tell the difference; the diagnostic
can, and saying so is its job. Without it, `installed: true` with no `version`
leaves a reader to guess between not-runnable, too-slow-to-answer, and
never-asked.

---

## 6. The ergonomic Go surface

Settled. The `Backend` interface exists, so this is now a contract rather than a
sketch.

```go
ol, err := olympus.Open(olympus.WithBackend("tmux"), olympus.WithSocket("ci"))
defer ol.Close()

s, err := ol.Session(ctx, "build", olympus.In("/repo"), olympus.Size(120, 40))

res, err := s.Exec(ctx, "go test ./...")         // res.ExitCode, res.Output
job, err := s.Start(ctx, "make deploy")          // job.Poll(ctx)

s.Type(ctx, "vim main.go")                       // places text, never submits
s.Submit(ctx)                                    // the terminator, alone
s.Send(ctx, "vim main.go")                       // verified: type, confirm, submit
s.Press(ctx, backend.KeyCtrlC)
s.Paste(ctx, text)

screen, err := s.Screen(ctx, olympus.WithColors())
hit, err := s.WaitFor(ctx, `\$ $`)

if errors.Is(err, olympus.ErrNotFound) { … }
```

Principles this surface satisfies:

- **Options, never positional booleans.** `Screen(ctx, WithColors())` rather than
  `Capture(ctx, targets, true, false)`. Unreadable call sites are the specific
  failure being corrected.
- **`Session` is ensure-semantics**, matching the `start` verb: create, reuse, or
  replace-if-dead. There is no separate create-versus-open decision for a caller
  to get wrong. `Open` is the non-creating variant, for a caller that must not
  bring a session into being by asking about it.
- **`Open` performs the §0.2 preflight**, so a missing backend fails there with
  an actionable error rather than at the first operation.
- **Typed errors and codes both**, per §3. The sentinels are re-exported from the
  root package, so branching on a failure never requires importing the
  mechanical layer.
- The mechanical `backend.Backend` interface stays public for anyone writing a
  backend, and `backend/backendtest` is exported so they can prove it against the
  same conformance suite. `Olympus.Raw` reaches it, at the cost of bypassing
  every default and lock this layer decides.

### 6.1 Where the two send paths differ

`Send` and `SendAtomic` are not variants of one operation, and the doors MUST NOT
offer a flag that combines them (§4.7):

| | `Send` | `SendAtomic` |
|---|---|---|
| Confirms the text landed | yes | no |
| Retry-safe across invocations | no — a retry re-types before checking | yes |
| Multi-line | yes | rejected: no unambiguous submit point |
| Lock scope | send → verify → submit, as one section | both writes |

### 6.2 Degraded results carry warnings, not errors

Operations that mean materially less on the resolved backend return a real
answer plus `Warnings` (§0.8). They are never errors: failing them outright would
make the default backend refuse work it can genuinely do. `Warnings` is
deliberately not serialized on the result type itself — the doors place it in the
envelope (§2), so there is one shape rather than two.

### 6.3 What the library does not decide

`Diagnose` takes no handle and returns no error, because a diagnostic that fails
when nothing is installed is useless at exactly the moment it is most needed
(§0.6). Every default in behavior §17.3 is a constant in this package, read by
the CLI and MCP doors rather than redeclared by them.

---

## 7. Stability

Semver, with these commitments:

- **Semver-bound**: the envelope shape, `data` field names and types, error codes
  and their exit codes, MCP tool names and parameter names, CLI verb names and
  flag names.
- **Additive only**: new fields, new codes, new verbs, new flags. A shipped field
  is never repurposed or removed within a major version.
- **Not stable**: human-readable output (§2.2), stderr wording, and anything
  under `docs/terminal-behavior.md` marked *(backend-local)*.

`version` reports one literal, shared by the CLI verb, the MCP tool, and the MCP
server identity, so no two doors can disagree about what is running.

The release **stamps** it with the tag at link time. It is a package variable
for that reason alone and must be treated as read-only by callers. Compiling it
in would mean every published binary reporting the development placeholder
whatever tag it was cut from — which breaks the one check a client has.

A binary built without that stamp — `go install …@tag` — reads the module
version from its own build info at start and reports that instead; only a
build from a working tree, with no tag to read, reports the development
placeholder.
