# Door contract

`docs/terminal-behavior.md` specifies how Olympus drives a multiplexer. This
document specifies what Olympus **exposes**: the vocabulary, output shapes and
stability guarantees shared by all three doors.

The behavior spec is the authority on mechanics. Where it constrains a door
(§0.4, §0.8, §5.3, §12), this document restates the constraint concretely and
never contradicts it. Every section here is binding.

---

## 1. One vocabulary, three doors

Every operation has exactly **one** name, one set of options and one result
shape. The CLI verb, the Go method and the MCP tool are three spellings of the
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
| follow output live | `watch` | *(none: streaming)* | `Watch` |
| run a command | `run` | `run_command` | `Exec` |
| run in a throwaway session | `run` (no target) | `run_command`, throwaway set | `RunOnce` |
| start a detached run | `run --detach` | `start_run` | `Start` |
| poll a detached run | `poll` | `poll_run` | `Job.Poll` |
| attach a terminal | `attach` | *(none: interactive)* | `Attach` |
| steer the server's focus onto a target | `focus` | `focus_session` | `Focus` |
| rename a session, window, tab or pane | `rename` | `rename_session` | `Rename` |
| read an exit marker | `exit-status` | `exit_status` | `ExitStatus` |
| create a view | `view create` | `create_view` | `CreateView` |
| scroll a view | `view scroll` | `scroll_view` | `ScrollView` |
| focus a pane in a view by cell | `view focus` | `focus_view` | `FocusView` |
| list views | `view ls` | `list_views` | `Views` |
| read a server env key | `server-env` | `server_env` | `ServerEnv` |
| list servers | `servers` | `list_servers` | `Servers` |
| start a server | `servers start` | `start_server` | `StartServer` |
| stop a server | `servers stop` | `stop_server` | `StopServer` |
| list the clients on a server, and what each shows | `clients` | `list_clients` | `Clients` |
| list agents in panes | `agents` | `list_agents` | `Agents` |
| list the agent vocabulary | `kinds` | `list_kinds` | `Kinds` (a package-level function) |
| what this backend can do | `capabilities` | `capabilities` | `Capabilities` |
| environment diagnosis | `doctor` | `doctor` | `Diagnose` |
| version | `version` | `version` | `Version` (a package variable) |
| serve the MCP door | `mcp` | *(is the door)* | none |

The MCP column is the 34 names of `ToolNames` in `internal/mcp/tools.go`. The
CLI also carries cobra's generated `completion` and `help`, which are not
operations.

### The doors translate; they do not decide

A default, validation rule or result field invented at one door is a second
contract. Defaults live in the ergonomic layer (behavior spec §17.3), and doors
pass them through.

### Only `attach` and `watch` are door-specific

`attach` is interactive and needs a terminal. `watch` is a stream. MCP is
request/response over stdio, so it exposes neither, and those two are the ONLY
operations it does not. Everything else in the table is reachable from every
door.

A door lacking an operation for any other reason is a bug, not a design choice.
Check it mechanically rather than by eye: the table is the authority, and a tool
missing from it is as much a defect as a tool missing from the server.

### `attach` flags

`attach` is CLI-only, so its flags have no MCP column. Not every one applies on
every backend, and an inapplicable one is refused as `UNSUPPORTED` rather than
ignored (behavior spec §8.7, §8.9).

| Flag | Go option | Meaning | Backends |
|---|---|---|---|
| `--viewer` | `AsViewer` | Read-only: no input, no resize. | tmux, zmx; refused on meja and herdr |
| `--keep-others` | `KeepOtherClients` | Co-attach instead of displacing prior clients. | all; meja and herdr's session client carry a notice where it cannot be honoured (§8.4); nothing to do under `--bare` on tmux |
| `--client` | `WithSessionClient` | The multiplexer's own session client (sidebar, tabs, selection, scroll, copy), steered onto the target first: a workspace is focused, a tab is focused within it, a pane is zoomed within its tab (§8.10). With `--server` it attaches that named session, otherwise the server on the resolved socket. | herdr only |
| `--bare` | `AsBare` | A plain pane, no chrome. On herdr, the session client with its chrome hidden (implies `--client`). On tmux, a throwaway view onto the session, killed when the attach ends; the target may be `<session>:<window>`. On a herdr server that advertises `client_view_focus`, the client is launched onto the target's workspace and moved by a tag of its own, and the server's focus is not steered (§8.10). | herdr, tmux |
| `--view` | `BareViewName` | With `--bare` on tmux, the view's name, which must begin with `olympus-view-` (§17.1). A caller can then `view scroll` and `view focus` it while attached, since an attach has no channel to report a generated name back. | tmux; usage elsewhere |
| `--client-tag` | `BareClientTag` | With `--bare` on a herdr server that advertises `client_view_focus`, the tag the client is launched with instead of a generated one: 1 to 128 bytes, no control characters. A caller can then find it with `clients --tag` while attached (§5 "Client row", behavior §13.5). | herdr; usage elsewhere, without `--bare`, and on a herdr server without `client_view_focus` |
| `--no-mouse` | `BareWithoutMouse` | With `--bare` on tmux, create the view without mouse reporting, for a client that keeps its own selection and scrolls through `view scroll`. | tmux; usage elsewhere |
| `--cols`, `--rows` | `AttachSize` | Initial size when stdin is not a terminal. | all |

### 1.1 Verbs are named for intent, not mechanism

`screen` rather than `capture-pane`, `wait` rather than `expect`, `stop` rather
than `kill-session`, `press` rather than `send-keys`. A person guessing a verb
should land on the right one.

#### Why

A name borrowed from the multiplexer splits the vocabulary. Pressing keys was
once `key` on the CLI, `send_keys` on MCP and `Press` in Go: three words for one
operation, two of them the multiplexer's words rather than the caller's.

### `poll` is a top-level verb, not a subcommand of `run`

#### Why

`run poll <target> <id>` would reserve `poll` as a session name. A session
literally named `poll` would become unaddressable by `run`, because subcommand
resolution wins. Keeping `poll` top-level costs nothing and removes the trap.

### `view` and `servers` are the only subcommand groups

Their operations act on views and on servers rather than on sessions, and each
shares a noun.

`servers` lists when bare, since listing is what a caller reaches for first.
`servers start [name]` and `servers stop <name>` are the noun's two writes.

#### Why start takes its name optionally and stop requires it

Without a name, start means the backend's own default server, and every caller
after a reboot means that one. Without a name, stop would take down a guess.

### 1.2 Targets are positional, everywhere

Every operation addressing a session takes it as the first positional argument.
No operation takes the target as a flag.

Operations addressing nothing take no positional: `ls`, `self`, `servers`,
`clients`, `agents`, `kinds`, `capabilities`, `doctor`, `version`, `mcp`. A few
take the target optionally: `panes`, `status` (the calling session when
omitted), `run` (a throwaway session when omitted) and `view ls` (every base).

Session names are ordinary positionals, so no verb name is reserved as a session
name. See §1.1.

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

| Field | Rule |
|---|---|
| `ok` | Always present, and the only field a consumer needs to branch on. |
| `backend` | The **resolved** backend, never the requested one (behavior spec §0.4). Present on failure as well as success, because a failure is when knowing which backend answered matters most. Omitted only when the failure came before any backend was resolved, such as a `USAGE` error from argument checking. |
| `data` | The per-operation payload. Absent for operations with no payload. An object or an array, never a bare scalar. |
| `warnings` | Omitted when empty, never `null`. Carries degraded-operation disclosure (behavior spec §0.8) for the structured doors, where stderr is not available. |
| `error` | Present exactly when `ok` is false. Carries a code from the behavior spec's §12 vocabulary. |
| `error.typed` | `true` on an `AGENT_BLOCKED` whose text was typed before the agent started waiting, so it may still be in the input box (behavior spec §7.5). Omitted otherwise. |

**Empty collections serialize as `[]`, never `null`.** This applies to `data`
when it is a list, and to every list-valued field inside it.

### 2.1 Why an envelope rather than a bare payload

Olympus has two cross-cutting fields, the resolved backend and the warnings, and
both must appear on every operation, including failures.

Bare per-operation payloads (`{"sent":"demo"}`) with errors as a separate shape
would make each operation remember to add them. The envelope makes it
structural. The cost is one level of nesting (`jq .data.name` rather than
`jq .name`), paid once.

### 2.2 Human output is a separate contract

Without `--json`, output is formatted for reading: aligned tables for lists,
plain text for screens and command output. Colour is permitted when stdout is a
TTY and forbidden when it is not. None is currently emitted.

**Human output is not stable and MUST NOT be parsed.** It may change in any
release, and scripts use `--json`. The `--help` of every operation that prints a
table says so.

`-q` suppresses non-essential human output. It has no effect on `--json`.

### 2.3 Streams are separate

| Stream | Carries |
|---|---|
| stdout | The data channel: the payload, the envelope, captured screen content, command output. |
| stderr | The narration channel: degraded-operation warnings (behavior spec §0.8), attach-steal notices (§8.5), throwaway-session cleanup failures (§6.10). |

Nothing diagnostic ever goes to stdout, and no payload ever goes to stderr. A
consumer piping stdout into a parser never has to filter it.

### `attach` and `watch` have no `--json` form

Asking for one is a `USAGE` error. Use `screen` for a capture that can be
parsed, and `info` to ask about a session.

#### Why

Their output IS the terminal rather than a description of it, so no envelope can
hold it:

- `attach` hands stdout to the multiplexer's client, which then owns it. Every
  byte the session draws goes there, and so does the client's own failure text.
  `open terminal failed: …` reaches stdout with stderr empty, and no layer above
  can take those bytes back.
- `watch` writes the raw output stream, escape sequences included. An envelope
  would mean buffering until the stream ends, which a follower must not do.

Refusing keeps the stream rule absolute, and costs the caller nothing, since
that output was never parseable either way. The MCP door has no attach tool at
all for the same reason: a stdio transport has no terminal to hand over.

---

## 3. Errors and exit codes

The code vocabulary and process exit codes are specified in behavior spec §12,
declared in `backend/errors.go`, and are **semver-bound**: never repurposed,
never removed, only added to.

| Code | Exit | Meaning |
|---|---|---|
| `USAGE` | 2 | Input the caller could have validated, including an unknown backend name. |
| `SESSION_NOT_FOUND` | 3 | A target session or pane that does not exist. |
| `BACKEND_UNAVAILABLE` | 4 | A selected backend that cannot be reached. |
| `TIMEOUT` | 5 | An operation that did not complete or match within its budget. |
| `CONFLICT` | 6 | A lock or attach slot held by someone else. |
| `UNSUPPORTED` | 7 | A backend with no concept for the operation at all. |
| `AGENT_BLOCKED` | 8 | A send refused because the target's agent is waiting on a person, or shows something open over its input box. Nothing was submitted: nothing was typed, or the prompt opened after typing and the terminator was never sent. |
| `UNEXPECTED` | 1 | Anything else: Olympus broke, and retrying will not help. |

### Every error reaches the envelope

This includes argument-parsing errors. The CLI MUST intercept its framework's
own flag validation rather than letting it print and exit (behavior spec §12.2).
A caller never needs to know which layer caught a failure to know whether it is
machine-readable.

### An MCP operation failure is a tool error carrying the code

It is never a JSON-RPC protocol error (behavior spec §15.6). Its first text
content is `CODE: message`, and an `AGENT_BLOCKED` marked `typed` adds a text
content reading `typed: true`.

### The Go door returns typed errors

`errors.Is` works against the exported sentinels `ErrUsage`, `ErrNotFound`,
`ErrUnavailable`, `ErrTimeout`, `ErrConflict`, `ErrUnsupported` and `ErrBlocked`. The code is
also readable from the error value with `CodeOf`, and `ExitCode` maps it to its
exit status. `TypedOf` reads the envelope's `error.typed`.

### 3.1 The two exit-code deviations

Restated from behavior spec §12.1 because they are door-visible. Both are also
documented in the affected verb's `--help`.

| Operation | Exit status |
|---|---|
| `run`, human path | The *command's own* exit code, so it composes in a pipeline like running the command directly. Infrastructure failures still use the table. |
| `run --json` | `0` for any successful protocol run. The command's exit code is in `data.exit_code`. |
| `attach` | The underlying attach client's code once the presence gate passes, so an exit of `3` is not necessarily `SESSION_NOT_FOUND`. |

---

## 4. Global options

These are the root command's persistent flags, plus the environment variables
that stand in for them.

| CLI | Environment | Applies to |
|---|---|---|
| `--backend <name>` | `OLYMPUS_BACKEND` | all (`zmx`, `tmux`, `meja`, `herdr`) |
| `--socket <name>` | `OLYMPUS_SOCKET` (MCP door only) | tmux backend only |
| `--socket-path <path>` | `OLYMPUS_SOCKET_PATH` (MCP door only) | tmux, meja and herdr backends |
| `--zmx-dir <dir>` | `ZMX_DIR` (read by zmx itself) | zmx backend only |
| `--server <name>` | `OLYMPUS_SERVER` (MCP door only) | tmux, herdr and zmx backends; exclusive with the three above |
| `--json` | none | all |
| `--no-lock` | none | operations that take the write lock |
| none | `OLYMPUS_LOCK_WAIT` | operations that take the write lock |
| `-q` / `--quiet` | none | human output only |

Precedence is flag over environment over default, per behavior spec §0.1. An
unknown backend name is `USAGE`, not `UNEXPECTED`.

### The addressing environment is the MCP door's

`OLYMPUS_SOCKET`, `OLYMPUS_SOCKET_PATH` and `OLYMPUS_SERVER` are read by the MCP
door alone. The CLI honours only `OLYMPUS_BACKEND` from the environment. On the
CLI the addressing options are the flags `--socket`, `--socket-path` and
`--server`.

The Go door takes these as options to `Open`: `WithBackend`, `WithSocket`,
`WithSocketPath`, `WithZmxDir`, `WithServer`, `WithoutLock` and `WithLockWait`.
The MCP door takes them from its process environment, since a stateless request
carries no session configuration.

### `ZMX_DIR` is zmx's own variable

The zmx binary reads it itself, so it applies whether or not Olympus passes it.
Setting it in the environment moves every session, which is what makes it usable
for isolation (behavior §2.9). The MCP door does not forward it.

### `--server` selects a server by name

The name is one of the rows `servers` lists. It is resolved into the backend's
own address (behavior spec §13.2): a tmux socket name, a herdr named session's
socket, zmx's one `default`.

| Case | Result |
|---|---|
| Given with `--socket`, `--socket-path` or `--zmx-dir` | `USAGE` |
| Unknown name | `SESSION_NOT_FOUND` |
| On meja | `UNSUPPORTED` |

### An addressing option the resolved backend cannot use is `USAGE`

It is never a silent no-op. The message names the option, the backend, and what
that backend does take.

#### Why

Each addressing option exists to isolate, to put a server somewhere the caller
controls. Dropping one quietly lands the caller on the shared default while they
believe they are alone on a private one.

### `OLYMPUS_LOCK_WAIT`

It overrides how long a writer waits for a contended session before reporting
`CONFLICT`. The default is 10s. It is a duration string, read at call time, and
is ignored when it does not parse or is not positive.

### `self` takes no addressing option

`self` answers where the calling process *is*, not what it would address.
Honouring `--backend` or `--socket` there would let a caller's configuration
contradict the truth. It is a package-level `Self(ctx)` in Go for the same
reason: a handle cannot change which session its own process is sitting in.

---

## 5. Payload shapes

Field names are `snake_case` in JSON, and identical across CLI and MCP except
where an entry below says otherwise. They are semver-bound once shipped.

### Status (`status`)

| Field | Type | Notes |
|---|---|---|
| `session` | string | The session the status belongs to. |
| `status` | string | Empty when the session has never reported one. |

The shape is the same in all three modes (read, `--set`, `--wait`), so a caller
needs one parser rather than three.

The value is **opaque**. Olympus stores and returns it exactly as given, defines
no vocabulary of states, and matches `--wait` exactly rather than as a pattern.
What counts as busy or blocked belongs to the program in the session, not to the
terminal.

Backends that cannot carry a status refuse both the read and the write with
`UNSUPPORTED`, and `capabilities` reports it as `session_status`. Behavior spec
§13.1.

#### With no target

`status` addresses the session the calling process is running in, and takes
that session's *backend and server* from the same answer, not from the defaults.

A reporter that resolved its name but not its server would write onto a
different backend entirely on any isolated setup. The waiter would then time out
against a session that never heard anything.

### Identity (`self`)

| Field | Type | Notes |
|---|---|---|
| `inside` | bool | Always present. False is an answer, not a failure. |
| `backend` | string | Omitted when outside, and when nested. |
| `session` | string | The name another program would use to reach this process. |
| `scope` | string | The socket or directory that session lives on. |
| `nested` | array | Every backend claiming this process, set only when more than one does. |

#### Outside a session exits `0`

It reports `inside: false`. A caller told "nowhere" can act on it, whereas one
handed an error must guess whether the error meant nowhere or could-not-tell.

#### Nested leaves the address empty

When `nested` is set, `backend`, `session` and `scope` are all empty. The
environment cannot say which session is inner: both sets of variables are
present, and inheritance looks identical either way.

This operation exists to tell another program where to reply. A confident wrong
address delivers that reply to somebody else's terminal, silently.

### Session row (`start`, `new`, `ls`, `info`)

```json
{
  "name": "build",
  "id": "$3",
  "attached": false,
  "dead": false,
  "liveness": "present",
  "cwd": "/repo",
  "outcome": "created",
  "focused": true
}
```

| Field | Notes |
|---|---|
| `liveness` | The backend-owned tri-state (behavior spec §3.2). |
| `outcome` | Only on `start`: `created`, `reused` or `reaped`. |
| `focused` | Only on herdr below 0.9.0. Marks the workspace EVERY session client on that server displays (behavior §8.10). |

`focused` is absent from herdr 0.9.0. There each client keeps its own view, and
the server's focus says where the next client will land rather than what the
running ones show. It is also absent on backends whose clients each show their
own session (§3.4). A listing where no row carries the flag means the backend
cannot say, never that the focus is elsewhere.

### Pane row (`panes`, `info`)

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
  "liveness": "present",
  "window_name": "editor",
  "title": "build log",
  "pid": 90720
}
```

| Field | Notes |
|---|---|
| `window_name` | The window (tmux) or tab (herdr) the pane sits in, as `rename` sets it. Omitted where the backend has no such name or none has been given. |
| `title` | The pane's own title (tmux) or label (herdr), as `rename` sets it. Omitted the same way. |
| `pid` | The pane's own process id, on tmux and zmx. Omitted on meja and herdr (behavior §3.4). It is the root `agents` walks to find an agent under the pane's shell. |
| `current_path`, `current_command` | Mean different things per backend (behavior spec §3.4), and trigger warnings on zmx and herdr per §0.8. |

On herdr, `current_command` is populated for a listing that named a target and
left empty for a whole-server listing. `attached` is always false there, because
no per-terminal client count exists. `created_at` is derived from the pane's
terminal id, which is the only creation time herdr publishes.

#### herdr: a session is a workspace, a window is a tab

A pane is a pane (behavior spec §3.6). A session's `name` is the workspace's
label where it has one, and its `id` (`w25`) where it has not.

herdr labels a workspace from its directory when nobody names it, so several
workspaces opened in one directory carry one label. The `id` beside the `name`
is how a caller addresses one exactly.

A pane row's `session_name` and `session_id` are its workspace's, and
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

#### Panes and windows are reported, never created

No verb, tool or method makes either. Every session Olympus creates is
single-window and single-pane.

tmux, meja and herdr have both concepts. zmx has neither, and its row is
synthesized from the session: `pane_id` is the session's name and
`window_index` is always 0.

#### A pane id is a target

`pane_id` works as a target anywhere a session name does, in each backend's own
spelling: `%7` on tmux, `7` on meja, `w1:p2` on herdr, the session's name on
zmx.

On tmux and meja it addresses the **session that owns the pane**, not the pane.
After a second window exists, an operation still runs against the session's
active window. Behavior spec §10.1 explains why precision there would cost the
write lock.

#### On herdr a target is pane-precise

`w1:p2` acts on that pane, `w1:t2` on the pane that tab is showing, and `w1` or
a label on the pane the workspace is showing. `stop` closes the level named,
with everything in it.

A herdr session may not be NAMED like any of the three ids, and creation rejects
one that is. A workspace with an empty label is named by its id, so the shapes
have to stay apart. The write lock is keyed on the target as given; the trade is
recorded in behavior spec §10.1.

### Screen (`screen`)

One or more targets in a single call:

```json
{
  "screens": { "build": "…" },
  "meta": { "build": { "alt_screen": false, "scroll_position": 0 } }
}
```

An alt-screen target IS captured. Its visible grid is the only way to observe a
full-screen application, and `alt_screen: true` tells a caller there is no
scrollback behind it (behavior spec §5.3). A history request against such a
target is dropped, with a warning.

Both maps are always objects, never `null`, including on the zero value a
failure returns.

### Presence (`info`)

`info` carries the tri-state presence answer and **MUST NOT error on an absent
target**:

```json
{ "state": "present", "session": { }, "panes": [ ], "capabilities": { }, "prefix": "C-b" }
```

| Field | Notes |
|---|---|
| `state` | `present`, `absent` or `error` (behavior spec §3.5). |
| `session`, `panes` | Omitted when the target is not present. When it **is** present, `panes` is always an array, empty if a listing raced a kill (§3.3), never missing. |
| `capabilities` | The resolved backend's capability set, as `capabilities` reports it. |
| `prefix` | The server's prefix key, as on a server row. Omitted where there is none. |

#### Why `panes` never vanishes under a present state

A missing key turns an ordinary iteration into a crash on the rarest path.

#### Why an absent target is not an error

Erroring with `SESSION_NOT_FOUND` would collapse the tri-state that exists so a
caller can tell "definitely gone" from "could not ask" (§3.5). `info` is the only
door onto that distinction, so it must preserve it.

### Acknowledgement (`type`, `send`, `press`, `paste`)

The MCP tools return `{"target": "build"}`, the session the text or keys went
to.

The CLI returns `target` too, plus fields per verb: `submitted` on `type`,
`send` and `paste`, `atomic` on `send`, `keys` on `press`, and `bytes` on
`paste`. This is a divergence between the doors, not a second contract to build
on. Branch on `ok` and `target`.

### Run (`run`)

`{"exit_code": 0, "output": "…"}`.

`run` with **no target** creates a throwaway session for the run and kills it
afterwards (behavior spec §6.10). `run --detach` with no target is a `USAGE`
error, since nothing would remain to poll.

### Detached run (`run --detach`, `poll`)

`run --detach` and `start_run` return `{"command_id": "…"}`. `poll` and
`poll_run` return
`{"status": "pending" | "completed" | "died", "exit_code": 0, "output": "…", "reason": "…"}`.

**`exit_code` is omitted unless `status` is `completed`** (behavior spec §6.7).
It is never a fake zero, so consumers branch on `status` first. `output` and
`reason` are omitted when empty.

#### `command_id` out, `id` in

On the MCP door the identifier is spelled `command_id` coming back and `id` going
in: `start_run` returns the first, `poll_run` takes the second. Both are shipped
and therefore fixed (§7). Renaming either would break every client that already
pairs them.

`poll_run` also accepts `command_id` as an alias for `id`, so a caller can hand
back exactly what `start_run` returned. When both are sent, `id` wins. The CLI
takes the id positionally and has no such split.

### Exit marker (`exit-status`)

`{"found": true, "exit_code": 0}`. `exit_code` is omitted when `found` is false,
which legitimately means the command has not finished yet.

### Server environment (`server-env`)

`{"key": "PATH", "present": true, "value": "…"}`. `value` is omitted when
`present` is false.

### Stop (`stop`)

`{"outcome": "gone" | "graceful" | "killed"}`.

All three are **successes**, and the distinction is the payload's whole reason to
exist:

| Outcome | Meaning |
|---|---|
| `gone` | There was nothing to stop. |
| `graceful` | The session took the interrupt. |
| `killed` | It did not, and was terminated. |

A caller reconciling state treats all three as "not running now". A caller
reporting to a human wants to say which happened.

### Focus and rename (`focus`, `rename`)

`focus` returns `{"target": "w1:p2"}`. `rename` returns
`{"target": "w1:t2", "name": "logs"}`. Both echo the target as given.

### Wait (`wait`)

The capture that satisfied the wait, plus which line did it:

```json
{ "text": "…", "meta": { "alt_screen": false, "scroll_position": 0 },
  "line": "$ make build", "matched": true }
```

`line` and `matched` are omitted when nothing matched: a capture that timed out
carries its `text` and no claim about it.

Matching is per line, never against the whole screen as one string (behavior
spec §7.2). `line` is the specific line the pattern hit rather than a slice of
the screen.

### View row (`view create`, `view ls`)

```json
{ "name": "olympus-view-build-a1b2", "base": "build", "id": "$9", "attached": false }
```

`base` is the session the view looks onto. A view's lifetime is independent of
its base's, but the window and pane are shared (behavior spec §9.2). A view row
is therefore not a session row and does not carry `liveness` or `cwd`: ask the
base for those.

#### Creating a view

| Door | Options |
|---|---|
| CLI `view create` | `--name`, `--no-mouse`, `--window` |
| MCP `create_view` | `base`, `name`, `no_mouse`, `window` |
| Go `CreateView` | `WithViewName`, `WithoutMouse`, `WithViewWindow` |

`window` pins the view to one of the base's windows, by index or by name,
instead of the window the base is showing. A window the base does not have is
`SESSION_NOT_FOUND`, and nothing is created (behavior spec §9.4).

The row does not report the window. A view keeps its own current window and can
be moved after creation, so the answer would be stale the moment it was read.
Ask tmux.

#### Scrolling a view

`scroll_view` returns `{"target": "<view>"}`. The CLI's `view scroll` returns
`{"view": "<view>", "lines": 10}`, another divergence between the doors.

### Focus result (`view focus`, `focus_view`)

```json
{ "view": "olympus-view-build-a1b2", "col": 52, "row": 3, "pane": "%1" }
```

`pane` is the id of the pane selected under the cell. It is empty when the cell
was on a border or outside every pane, which is a result, not an error (behavior
spec §9.6).

`view focus` takes `--col` and `--row`, both 0-based. `focus_view` takes `view`,
`col` and `row`. The active pane is shared with the base, so the base follows.

### Server row (`servers`)

```json
{ "name": "work", "socket_path": "/tmp/tmux-501/work", "running": true, "default": false,
  "dir": "/tmp/tmux-501", "prefix": "C-b" }
```

| Field | Notes |
|---|---|
| `name` | What `--server` selects by. |
| `running` | Measured, not inferred from the socket file. |
| `default` | The row the backend addresses when nothing selects one. On tmux and herdr this is not the server Olympus itself defaults to (behavior spec §17.2). |
| `dir` | Omitted where the backend has none to report. |
| `prefix` | The key that introduces the server's own bindings, in tmux's spelling whichever backend answered (`C-b`, `C-Space`, `M-a`, `F19`). Omitted where the backend has none, or a stopped server's cannot be asked (behavior §13.3). |

What a row is differs by backend: a tmux socket name, a herdr named session,
zmx's one directory. Behavior §13.2 specifies it.

`info` carries the same `prefix` for a present session. That is how a caller
holding a target on a backend whose servers cannot be listed (meja) still learns
it.

### Started server (`servers start`)

```json
{ "name": "default", "outcome": "started" }
```

`outcome` is `running` (it was already up and was left alone) or `started`. Both
are successes.

Nothing is created on the server. What comes up with it is whatever the backend
restores of its own accord, which on herdr is the panes that session was running
(behavior §13.4).

### Stopped server (`servers stop`)

```json
{ "name": "work", "outcome": "killed" }
```

`outcome` is `gone` (it was not running) or `killed`. Both are successes.

### Client row (`clients`)

```json
{ "id": "7", "tag": "browser-1", "session_id": "w2", "window_id": "w2:t3",
  "pane_id": "w2:p4", "zoomed": true, "view_applied": true }
```

One row per client attached to the server, in the server's order. Each says
which session, window and pane the client shows right now, including a pane a
person focused with the client's own keys (behavior §13.5).

| Field | Notes |
|---|---|
| `id` | The server's own number for the client, as a string. |
| `tag` | The name it was launched with: the caller's own from `attach --bare --client-tag`, or the `olympus-client-<16 hex>` a bare attach draws otherwise. Omitted for a client launched with none. |
| `session_id`, `window_id`, `pane_id` | On herdr: the workspace, the tab, and the focused pane of that tab, which is where what the client sends goes. Each is a target every other verb takes. |
| `zoomed` | Whether that tab shows the pane alone. |
| `view_applied` | Whether the client has applied the view the server holds for it, so that what it sends now reaches `pane_id`. |

#### A field the server does not report is omitted

It is never false or empty. `pane_id` and `zoomed` come only from a herdr server
that also advertises `client_view_ack`. `view_applied` comes only for a client
there that acknowledges what it applies. The ids are omitted for a client that
has no view yet.

#### Narrowing to one tag

`clients` takes `--tag <tag>`, `list_clients` takes `tag`, and Go takes
`WithClientTag`. The listing is narrowed to the one client carrying it, and is
still an array.

| Case | Result |
|---|---|
| No client carries the tag | `SESSION_NOT_FOUND` |
| Tag outside 1 to 128 bytes, or carrying a control character | `USAGE` |
| No server running | `[]` |
| Any backend or server other than a herdr server advertising `client_view_focus` | `UNSUPPORTED`, not `[]` |

It is not a field of `capabilities`, which reports static facts of a backend
rather than of one running server. The listing is the probe.

### Agent row (`agents`)

```json
{ "pane_id": "w5F:p1", "session_name": "gamelan", "session_id": "w5F",
  "agent": "claude", "status": "working", "status_source": "native",
  "title": "Stop music on Chrome",
  "cwd": "/Users/husni/github.com/husniadil/gamelan", "detected_by": "herdr",
  "pid": 34398,
  "usage": [{ "label": "5h", "percent": 33 }, { "label": "7d", "percent": 48 }],
  "agent_session": { "source": "herdr:claude", "agent": "claude",
                     "kind": "id", "value": "6d3b1bee-76d2-42e3-9c99-f8a96df213d3" } }
```

and on a backend without detection of its own:

```json
{ "pane_id": "%3", "session_name": "fix", "session_id": "$3",
  "agent": "codex", "status": "blocked", "status_source": "screen",
  "last": "Allow codex to run `rm -rf build`?",
  "cwd": "/Users/husni/github.com/husniadil/gamelan", "detected_by": "command",
  "pid": 77564 }
```

The listing answers on every backend and is never `UNSUPPORTED`. With no agent
it is `[]`, never null (behavior §3.7).

| Field | Notes |
|---|---|
| `pane_id`, `session_name`, `session_id` | The pane's, as a pane row spells them. |
| `agent` | The agent's canonical name, as `kinds` lists it, or whatever a natively-detecting backend reports. An alias such as `cursor-agent` or `claude-code` is reported under its canonical name. |
| `status` | `working`, `idle`, `blocked` (waiting on a person: a permission prompt, a question) or `unknown`. |
| `status_source` | Where a known status came from: `native`, the backend's own detection; `screen`, read off a capture of the pane by the agent's manifest. Omitted when the status is `unknown`, which means no evidence, never a guess. |
| `detected_by` | How the row was found: `herdr` or `command`. |
| `title` | Omitted where absent. |
| `cwd` | The pane's working directory. |
| `pid` | The agent's own process where one is known. |
| `last` | One line of the agent's own output, only when asked for. |
| `usage` | Omitted where absent. `usage[].percent` is an integer 0 to 100, and `usage[].label` the short label the agent shows (`5h`, `7d`, a model name). |
| `agent_session` | The agent's own conversation, where the backend holds a reference to it. |

`capabilities` reports `agent_status` where rows can carry a status: true on
every backend, native on herdr and screen-derived elsewhere.

#### One status per meaning

A backend that spells a status more than one way has every spelling folded onto
it. herdr says `done` for an idle agent nobody has looked at since it stopped,
and it is reported here as `idle`. Whether somebody has looked is a fact about
the operator rather than about the agent.

#### `detected_by`

| Value | Found by | Carries |
|---|---|---|
| `herdr` | The backend's own detection. | `status` and `title`. |
| `command` | A known agent's name in the pane's processes. | A screen-derived `status`, no `title`, no `usage`. |

A `command` row is found by walking the pane's process subtree from its `pid`,
so an agent running under the pane's shell, or as a `node` script, is listed
under the vocabulary's name. Where the pane has no `pid`, the foreground command
is matched instead. Its status costs one capture of the pane per row per call,
for agents that have a manifest (behavior §3.7).

#### `pid`

On a `command` row it is the process that named the agent. On a `herdr` row it
is the pane's foreground process group leader, read per row. It is omitted where
none is known: a foreground-command match, or a pane herdr could not describe.

It is a handle for the caller. The row claims nothing about what that process
was started with.

#### `last`

What the agent last said, or, where the row is `blocked`, the question it is
waiting on. It is present only where the caller asked for it: `agents --last`,
`Agents(ctx, WithLast())`, `list_agents {"last": true}`.

It costs one capture per row, which is why it is asked for rather than always
sent: a row whose status the backend reported itself was never captured
otherwise. Empty is the honest answer for a screen with nothing to say, an agent
with no manifest, or a capture that failed, and it is omitted then.

#### `agent_session`

What the agent itself takes to pick that conversation up again (`claude --resume
<value>` for a `kind` of `id`), copied exactly as the backend spells it.

| Field | Notes |
|---|---|
| `source` | Who reported it: herdr's integration hooks, as `herdr:claude`. |
| `agent` | The agent it belongs to. |
| `kind` | Whether `value` is an `id` or a `path`. |
| `value` | The reference itself. |

It is omitted where the backend has none: every backend but herdr, and a herdr
pane whose agent never reported. Its absence claims nothing. Olympus neither
stores nor infers a conversation.

### Agent kind row (`kinds`)

```json
{ "name": "claude", "executables": ["claude", "claude-code"], "packages": ["claude-code"], "resume": ["--resume"] }
```

`kinds` answers which agents Olympus knows and by what executables. It is the
canonical vocabulary the `agents` listing reports `agent` in (behavior §3.7),
one row per canonical name, ordered by `name`.

| Field | Notes |
|---|---|
| `name` | The canonical name. |
| `executables` | Every argv0 token the detection table maps to that name, the canonical spelling first and the remaining aliases sorted. A token's base name is matched against it, lowercased and with a wrapper suffix (`.js`, `.cmd`) removed. |
| `packages` | The package directories that identify the agent where no token is named after it. An npm install runs as `node …/@anthropic-ai/claude-code/cli.js`, whose path holds `claude-code`. Omitted for an agent that has none. |
| `resume` | The arguments that open the agent's own list of past conversations. Only the picker, since which conversation is a choice made in the pane. Omitted where Olympus does not know the agent's way. |

Both `executables` and `packages` are derived from the detection tables
themselves rather than restated, so the verb cannot disagree with what `agents`
matches on. A consumer that starts agents reads `resume` here rather than keeping
a copy that covers a few of them.

#### Why `Kinds` is a package-level function

It addresses nothing and resolves no backend. The vocabulary is Olympus's own
table, identical everywhere and readable with no multiplexer installed, which is
the same reason `Self` is one.

#### What the rows cannot show

- muse's versioned launcher (`muse-bin-<version>`) is matched by shape rather
  than by a token, so no row can list it.
- A name a natively-detecting backend reports outside this table can still
  appear on an agent row, since the backend's own detection is not this table.

#### The vocabulary grows; the shape does not change

The row shape is semver-bound like every other payload (§7): a field is only
ever added. The vocabulary the rows CARRY is not a fixed set. Names, executables
and packages are added as agents appear, which is an additive change to the data
and not to the contract.

A consumer must therefore treat an unknown `name` as an agent it has not heard
of, never as an error, and must not hardcode the set. Asking `kinds` is what
makes that unnecessary.

### Doctor (`doctor`)

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
                        "session_status": false, "tracks_alt_screen": false, "servers": true,
                        "session_client": false, "bare": false, "focus": false, "rename": false,
                        "agent_status": true } },
    { "name": "herdr", "installed": true, "version": "0.8.2", "floor": "0.8.2",
      "below_floor": false,
      "isolation": "socket at /tmp/olympus-herdr/herdr.sock; its configuration and saved layout live beside it, invisible to your own herdr",
      "capabilities": { "native_scrollback": false, "views": false, "remain_on_exit": false,
                        "server_env": false, "control_keys": true,
                        "spawn_sizing": false, "spawn_command": false,
                        "session_status": true, "tracks_alt_screen": false, "servers": true,
                        "session_client": true, "bare": true, "focus": true, "rename": true,
                        "agent_status": true },
      "managed_options": { "update.manifest_check": "false", "update.version_check": "false" } },
    { "name": "tmux", "installed": true, "version": "3.7b", "floor": "3.3",
      "below_floor": false,
      "isolation": "private socket \"olympus\"; these sessions do not appear in a plain `tmux ls`",
      "capabilities": { "native_scrollback": false, "views": true, "remain_on_exit": true,
                        "server_env": true, "control_keys": true,
                        "spawn_sizing": true, "spawn_command": true,
                        "session_status": true, "tracks_alt_screen": true, "servers": true,
                        "session_client": false, "bare": true, "focus": true, "rename": true,
                        "agent_status": true },
      "managed_options": { "default-command": "", "history-limit": "50000" } }
  ],
  "install_hints": []
}
```

#### `resolved`

| Field | Notes |
|---|---|
| `backend` | The resolved backend. |
| `reason` | The resolution rule that applied: `flag`, `env`, `default` or `fallback`. This satisfies the disclosure requirement of behavior spec §0.4. |
| `socket_or_dir` | The socket or directory that answers. |
| `pinned` | Whether the server answering right now is one Olympus started and pinned options on. |
| `effective_options` | What the managed options are actually set to on that server. Omitted when empty. |
| `problem` | Why the resolved server could not be asked. Omitted when empty. |

#### A backend entry

| Field | Notes |
|---|---|
| `name`, `installed`, `version` | `version` is omitted when it could not be read. |
| `floor` | The oldest version of that backend Olympus is supported against. |
| `below_floor` | That comparison, already made for the reported version. |
| `isolation` | One sentence: which socket or directory answers, and whether the sessions show up in the user's own plain listing. |
| `capabilities` | What that backend can do. |
| `managed_options` | Every option Olympus pins on servers **it starts**. Omitted where there are none. |
| `problem` | Present when the backend is on PATH but could not be run. |

#### `problem`: on PATH but not runnable

This is the case a version-manager shim left behind by an uninstalled tool
produces: a lookup succeeds and every call fails. Resolution is a single lookup
with no subprocess (behavior spec §0.2) and cannot tell the difference. The
diagnostic can, and saying so is its job.

Without it, `installed: true` with no `version` leaves a reader to guess between
not-runnable, too-slow-to-answer and never-asked.

#### Managed options are disclosed, and pinned only on servers Olympus starts

On tmux, the pinned options override the operator's own configuration. On herdr
they do not, because that backend's configuration directory moves with its
socket, so the file being written is one Olympus owns. They are disclosed either
way.

`resolved.effective_options` on a server Olympus merely found is whatever that
server was given, and is the only thing that decides how a run behaves.

Nothing cosmetic is pinned: keybindings, prefix and theme are left alone.
Nothing at all is pinned on a server that was already running. Behavior spec
§17.5 has the measurements and the rule.

##### Why tmux pins two options

A private socket is not a private configuration. tmux fixes a server's settings
at boot from `tmux.conf`, so the operator's file reaches Olympus's sessions.
`default-command` and `history-limit` are pinned back because the run protocol's
exit marker and the meaning of a capture's line count depend on them.

##### Why nothing is pinned on a running server

`set-option -g` reaches every session on a server. A caller who named one
session would otherwise have all the operator's others changed with it.

##### Why herdr pins two options

`update.version_check` and `update.manifest_check` are both false. They turn off
a background network check a freshly started server would otherwise make, which
has nothing to do with driving a terminal and which nobody asked for.

Nothing is pinned on a herdr server that was already answering.
`resolved.pinned` is false there, and that server never read a file Olympus
wrote.

#### Driving a herdr server Olympus did not start

**Pointing `--socket-path` at a herdr server that is already running is a
supported mode.** It is how you drive a box's own headless herdr, or an
operator's, and read and attach to panes other tools created.

Olympus never starts, reconfigures or stops such a server. A request to stop one
it did not start is refused with `CONFLICT`, because stopping takes every pane on
the server down, including every one you never named. Close the sessions you own
instead. Behavior spec §2.9.1.

---

## 6. The ergonomic Go surface

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

### Options, never positional booleans

`Screen(ctx, WithColors())` rather than `Capture(ctx, targets, true, false)`.
Unreadable call sites are the specific failure being corrected.

### `Session` is ensure-semantics

It matches the `start` verb: create, reuse, or replace-if-dead. There is no
separate create-versus-open decision for a caller to get wrong.

`Olympus.Open(ctx, target)` is the non-creating variant, for a caller that must
not bring a session into being by asking about it. `Create` is the variant that
fails when the name is taken, matching `new`.

### The package `Open` performs the §0.2 preflight

A missing backend fails there, with an actionable error, rather than at the
first operation.

### Typed errors and codes both

Per §3. The sentinels are re-exported from the root package, so branching on a
failure never requires importing the mechanical layer.

### The mechanical layer stays public

The `backend.Backend` interface is public for anyone writing a backend, and
`backend/backendtest` is exported so they can prove it against the same
conformance suite. `Olympus.Raw` reaches it, at the cost of bypassing every
default and lock this layer decides.

### 6.1 Where the two send paths differ

`Send` and `SendAtomic` are not variants of one operation, and the doors MUST NOT
offer a flag that combines them (behavior §4.7). `send_text` with `atomic` and
`no_submit` is refused as `USAGE`. The CLI's `send --atomic` ignores
`--no-enter` and submits, a divergence between the doors.

| | `Send` | `SendAtomic` |
|---|---|---|
| Confirms the text landed | yes | no |
| Retry-safe across invocations | no: a retry re-types before checking | yes |
| Multi-line | yes | rejected: no unambiguous submit point |
| Lock scope | send, verify, submit, as one section | both writes |
| Refuses an agent waiting on a person, as `AGENT_BLOCKED` | yes, before typing and on every capture after | yes, before the write |

### 6.2 Degraded results carry warnings, not errors

Operations that mean materially less on the resolved backend return a real
answer plus `Warnings` (behavior §0.8). They are never errors: failing them
outright would make the default backend refuse work it can do.

`Warnings` is not serialized on the result type itself. The doors place it in
the envelope (§2), so there is one shape rather than two.

### 6.3 What the library does not decide

`Diagnose` takes no handle and returns no error. A diagnostic that fails when
nothing is installed is useless at exactly the moment it is most needed
(behavior §0.6).

Every default in behavior §17.3 is a constant in this package, read by the CLI
and MCP doors rather than redeclared by them.

---

## 7. Stability

Semver, with these commitments:

| Class | Covers |
|---|---|
| **Semver-bound** | The envelope shape, `data` field names and types, error codes and their exit codes, MCP tool names and parameter names, CLI verb names and flag names. |
| **Additive only** | New fields, new codes, new verbs, new flags. A shipped field is never repurposed or removed within a major version. |
| **Not stable** | Human-readable output (§2.2), stderr wording, and anything in `docs/terminal-behavior.md` marked *(backend-local)*. |

### One version literal

`version` reports one literal, `Version` in `version.go`, shared by the CLI verb,
the MCP tool and the MCP server identity. No two doors can disagree about what
is running.

### The release stamps it

The release stamps the tag into `Version` at link time. It is a package variable
for that reason alone, and callers must treat it as read-only.

A binary built without that stamp (`go install …@tag`) reads the module version
from its own build info at start and reports that instead. A build from a git
checkout reports the version the Go toolchain derives from the repository,
`+dirty` included where the tree has changes. Only a build with no version
control information to read reports the development placeholder.

#### Why

A compiled-in literal would make every published binary report the development
placeholder whatever tag it was cut from. That breaks the one check a client
has.
