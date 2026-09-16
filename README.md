# Olympus

A terminal you can drive from code.

Olympus creates, drives, observes and tears down real terminal sessions, the
kind that survive you closing your laptop. It does not embed a multiplexer. It
drives one you already have, and exposes that through three equal doors: a
**Go package**, a **CLI** and a **stdio MCP server**.

## What it does

- **Keeps sessions alive.** A session outlives the command that made it. Come
  back to it from another shell, another process, or tomorrow.
- **Runs commands and reports their exit code.** `run` waits for a command and
  returns its status and output, or detaches and lets you poll.
- **Drives interactive programs.** Type, send, press keys, paste, and wait for
  a pattern on screen, against a REPL or a full-screen program.
- **Reads screens.** One or several sessions in one call, with scrollback.
- **Finds coding agents in panes.** `agents` lists which agent runs where, and
  whether it is working, idle or blocked on a person.
- **Hands a terminal to a person.** `attach` gives the live session to whoever
  is at the keyboard.
- **Says what it cannot do.** `doctor` and `capabilities` report each backend's
  limits, and a degraded operation warns instead of failing quietly.

## Requirements

- **macOS or Linux.**
- **Go 1.26.5 or newer**, to install with `go install`. A release archive needs
  no Go.
- **At least one multiplexer.** Olympus picks the first one installed, in this
  order:

| Backend | Version floor |
|---|---|
| [zmx](https://github.com/neurosnap/zmx) (default) | 0.6.0 |
| [tmux](https://github.com/tmux/tmux) | 3.3 |
| [meja](https://github.com/garindra/meja) | 0.0.25 |
| [herdr](https://herdr.dev) | 0.8.2 |

`--backend` or `OLYMPUS_BACKEND` picks one explicitly. The floors are reported
by `olympus doctor`, not enforced.

## Install

```sh
go install github.com/husniadil/olympus/cmd/olympus@latest
```

Or take an archive from
[GitHub Releases](https://github.com/husniadil/olympus/releases). Each is built
for darwin and linux on amd64 and arm64, and carries the binary, its man pages
and its shell completions.

## Quickstart

Check what Olympus found:

```sh
olympus doctor
```

It prints which backends are installed, which one answers and why, where its
sessions live, and what each can do. It never fails: when nothing is installed,
explaining that is its job.

Then start a session and run something in it:

```sh
olympus start build
olympus run build 'echo hello from a real terminal'
olympus screen build
olympus stop build
```

`build` is a name you choose.

## Three doors, one vocabulary

Every operation has one name, one set of options and one result shape. The CLI
verb, the Go method and the MCP tool are three spellings of the same thing.
[`docs/api.md`](docs/api.md) §1 has the full table.

### The CLI

```sh
olympus start build --dir /repo
olympus send build 'make test'        # types it, confirms it landed, submits it
olympus wait build 'ok|FAIL'          # block until the output says something
olympus screen build api docs         # several sessions in one call
olympus run 'go build ./...'          # no target: a throwaway session
olympus agents                        # coding agents in panes, with status
olympus attach build                  # hand this terminal over
```

`olympus --help` lists every verb, and each verb's `--help` explains the parts
that are not obvious. Add `--json` to any verb for a stable envelope:

```sh
olympus ls --json | jq '.data[].name'
```

### The Go package

```go
ol, err := olympus.Open()
defer ol.Close()

s, err := ol.Session(ctx, "build", olympus.In("/repo"))

res, err := s.Exec(ctx, "go test ./...")
fmt.Println(res.ExitCode, res.Output)

job, err := s.Start(ctx, "make deploy")   // detached
status, err := job.Poll(ctx)

if errors.Is(err, olympus.ErrNotFound) { … }
```

`Session` is create-or-reuse, so there is no separate "does it exist yet" step.

### The MCP server

```json
{
  "mcpServers": {
    "olympus": { "command": "olympus", "args": ["mcp"] }
  }
}
```

`olympus mcp` serves over stdio only. It targets MCP revision `2026-07-28` and
still answers clients that use the older `initialize` handshake.

## Things worth knowing

### Sessions belong to a backend

A session created on zmx is invisible from tmux, and the reverse. Sessions
never migrate. `olympus doctor` and every `--json` envelope say which backend
answered.

### The backends are not equivalent

| | zmx | tmux | meja | herdr |
|---|---|---|---|---|
| Views | no | yes | no | no |
| Corpse on exit | no | yes | no | no |
| Server environment | no | yes | no | no |
| Control keys | no | yes | yes | yes |
| Start on a command | yes | yes | yes | no |

zmx is the default and the least capable. herdr panes run the shell its own
configuration names, so `start <name> -- <command>` is refused there rather
than typed into a shell.

`olympus doctor` shows the full capability matrix, and `olympus capabilities`
shows it for the backend in use.

### Typing and submitting are separate

`type` places text without pressing Enter. `send` confirms the text landed on
screen, then submits it.

### A failing command is not an error

`olympus run` reports the command's own exit code. With `--json` it is in
`data.exit_code` and the process exits 0. Without `--json` the process exits
with the command's status, so it composes in a pipeline.

### `run` needs a shell

`run` marks a command's start and end with shell syntax, so pointed at a REPL
it times out. Drive a REPL or a full-screen program with `send`, `press` and
`wait`, and read it with `screen`.

### Wait for the program, not your prompt

`wait` matches per line. A pattern like `'\$\s*$'` matches only your own prompt
and fails under zsh, fish or a themed prompt. Match what the command prints, and
never require a trailing space: `^>>>\s*$`, not `^>>> $`.

### Full-screen programs need control keys

Every backend captures a full-screen program as it currently looks. zmx does
not deliver control keys reliably, so an editor there can be typed into but not
saved or exited. `olympus capabilities` reports this as `control_keys`.

### Where sessions live differs

- **tmux** and **herdr**: Olympus uses its own socket, so its sessions do not
  appear in your own `tmux ls` or herdr.
- **zmx**: sessions are global to your daemon and appear in `zmx list`.
- **meja**: your default profile, so sessions appear in `meja ls`, unless you
  pass `--socket-path`.

`olympus doctor` states which is in effect.

### Servers are the level above sessions

`olympus servers` lists the servers a backend can see, and `--server <name>`
points any verb at one. `olympus servers stop <name>` takes one down with every
session on it. meja cannot enumerate its servers.

### Agents are found in panes

`olympus agents` lists the coding agents running in panes, on every backend,
with status `working`, `idle`, `blocked` or `unknown`. `olympus kinds` lists the
agents it knows and the executables each is matched on.

### herdr maps onto sessions, windows and panes

A herdr workspace is a session, a tab is a window and a pane is a pane. Point
`--socket-path` at a herdr you already run to list, drive and attach to its
panes. See spec [§3.6](docs/terminal-behavior.md#36-herdr-workspace--tab--pane).

### Your tmux config still applies

A private socket is not a private configuration: your `tmux.conf` reaches
Olympus's sessions. On servers it starts, Olympus pins only `default-command`
and `history-limit`, and `doctor` names both. See spec
[§17.5](docs/terminal-behavior.md#175-a-private-socket-is-not-a-private-configuration).

## Exit codes

| Code | Meaning |
|---|---|
| 0 | success |
| 1 | something unexpected; retrying will not help |
| 2 | usage: one corrected argument fixes it |
| 3 | the session or pane does not exist |
| 4 | the backend could not be reached |
| 5 | timed out |
| 6 | someone else holds the session |
| 7 | the backend has no such concept |

Two verbs differ, and say so in their `--help`: `run` reports the command's exit
code, and `attach` reports the multiplexer client's.

## Learn more

| If you want to | Read |
|---|---|
| Know how Olympus drives a multiplexer | [docs/terminal-behavior.md](docs/terminal-behavior.md) |
| Know the contract the three doors share | [docs/api.md](docs/api.md) |
| Add a backend | [docs/adding-a-backend.md](docs/adding-a-backend.md) |
| Build, test or contribute | [CONTRIBUTING.md](CONTRIBUTING.md) and [CLAUDE.md](CLAUDE.md) |
| See what is still outstanding | [docs/known-issues.md](docs/known-issues.md) |
| See what changed in each release | [CHANGELOG.md](CHANGELOG.md) |

## If you are an AI agent helping someone with Olympus

- **Run `olympus doctor` first.** It says which backend answers and what that
  backend cannot do. Do not assume tmux behavior on zmx.
- **Read [skills/olympus/SKILL.md](skills/olympus/SKILL.md)** for which verb
  fits which situation, and the traps. With Claude Code, copy the directory to
  `~/.claude/skills/olympus/`.
- **Use `--help`** on the verb you need. It matches the installed build.
- **Use `--json`** whenever you parse output. Human output may change in any
  release.
- **Before changing code**, read [CLAUDE.md](CLAUDE.md) and the section of
  [docs/terminal-behavior.md](docs/terminal-behavior.md) you touch.

## Status

Pre-1.0. The `--json` envelope, the error codes, the CLI verb and flag names and
the MCP tool and parameter names are semver-bound: additive only, never
repurposed or removed within a major version. Human-readable output is not
stable.

## License

MIT. See [LICENSE](LICENSE).

Agent status manifests under `internal/agentstate/manifests/` are herdr's,
Apache 2.0, © herdr authors, with the license text beside them and the vendored
commit recorded in [NOTICE](NOTICE).
