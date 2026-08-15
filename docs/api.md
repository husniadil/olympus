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
| send named keys | `key` | `send_keys` | `Press` |
| paste multi-line text | `paste` | `paste_text` | `Paste` |
| read the screen | `screen` | `capture` | `Screen`, `Capture` |
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
| list views | `view ls` | `list_views` | `Views` |
| read a server env key | `server-env` | `server_env` | `ServerEnv` |
| what this backend can do | `capabilities` | `capabilities` | `Capabilities` |
| environment diagnosis | `doctor` | `doctor` | `Diagnose` |
| version | `version` | `version` | `Version` (a constant) |
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

### 1.1 Verbs are named for intent, not mechanism

`screen` rather than `capture-pane`, `wait` rather than `expect`, `stop` rather
than `kill-session`. A person guessing a verb should land on the right one.

**`poll` is a top-level verb, not a subcommand of `run`.** Making it
`run poll <target> <id>` would reserve `poll` as a session name — a session
literally named `poll` becomes unaddressable by `run`, because subcommand
resolution wins. Keeping `poll` top-level costs nothing and removes the trap.

`view` is the one legitimate subcommand group: its three operations act on views
rather than sessions, and share a noun.

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
plain text for screens and command output, colour when stdout is a TTY and never
when it is not.

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
| `--backend <name>` | `OLYMPUS_BACKEND` | all (`zmx`, `tmux`, `meja`) |
| `--socket <name>` | — | tmux backend only |
| `--socket-path <path>` | `OLYMPUS_SOCKET_PATH` | tmux and meja backends |
| `--zmx-dir <dir>` | `ZMX_DIR` | zmx backend only |
| `--json` | — | all |
| `--no-lock` | — | operations that take the write lock |
| `-q` / `--quiet` | — | human output only |

Precedence is flag over environment over default, per behavior spec §0.1. An
unknown backend name is `USAGE`, not `UNEXPECTED`.

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

`current_path` and `current_command` carry different meanings per backend
(behavior spec §3.4) and trigger warnings on zmx per §0.8.

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
`panes` are omitted when the target is not present. Erroring with
`SESSION_NOT_FOUND` instead would collapse the tri-state that exists precisely so
a caller can tell "definitely gone" from "could not ask" — the whole point of
§3.5. `info` is the only door onto that distinction, so it must preserve it.

**Run** (`run`): `{"exit_code": 0, "output": "…"}`.

`run` with **no target** creates a throwaway session for the run and kills it
afterwards (behavior spec §6.10). `run --detach` with no target is a `USAGE`
error, since nothing would remain to poll.

**Detached run**: `start` returns `{"command_id": "…"}`; `poll` returns
`{"status": "pending" | "completed" | "died", "exit_code": 0, "output": "…", "reason": "…"}`.

**`exit_code` is omitted unless `status` is `completed`** (behavior spec §6.7) —
never a fake zero. Consumers branch on `status` first.

**View row** (`view create`, `view ls`):

```json
{ "name": "olympus-view-build-a1b2", "base": "build", "id": "$9", "attached": false }
```

`base` is the session the view looks onto. A view's lifetime is independent of
its base's, but the window and pane are shared (behavior spec §9.2), so a view
row is not a session row and does not carry `liveness` or `cwd` — ask the base
for those.

**Doctor** (`doctor`):

```json
{
  "resolved": { "backend": "zmx", "reason": "default", "socket_or_dir": "/tmp/zmx-501",
                "pinned": false },
  "backends": [
    { "name": "zmx", "installed": true, "version": "0.6.0", "below_floor": false,
      "capabilities": { "native_scrollback": true, "views": false, "remain_on_exit": false,
                        "server_env": false, "control_keys": false,
                        "session_status": false, "tracks_alt_screen": false } },
    { "name": "tmux", "installed": true, "version": "3.7b", "below_floor": false,
      "capabilities": { "native_scrollback": false, "views": true, "remain_on_exit": true,
                        "server_env": true, "control_keys": true,
                        "session_status": true, "tracks_alt_screen": true },
      "managed_options": { "default-command": "", "history-limit": "50000" } }
  ],
  "install_hints": []
}
```

`managed_options` is every option Olympus pins on servers **it starts**,
overriding the operator's own configuration. `resolved.pinned` says whether the
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

`reason` names the resolution rule that applied (`flag`, `env`, `default`,
`fallback`), satisfying the disclosure requirement of behavior spec §0.4.

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
