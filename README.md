# Olympus

A terminal you can drive from code.

Olympus creates, drives, observes and tears down real terminal sessions — the
kind that survive you closing your laptop — and exposes that through three equal
doors: a **Go package**, a **CLI**, and a **stdio MCP server**.

It does not embed a multiplexer. It drives one you already have: [`zmx`][zmx] by
default, [`tmux`][tmux] as the alternative, then [`meja`][meja] and
[`herdr`][herdr].

[zmx]: https://github.com/neurosnap/zmx
[tmux]: https://github.com/tmux/tmux
[meja]: https://github.com/garindra/meja
[herdr]: https://herdr.dev

---

## Quickstart

You need Go 1.26.5+ and at least one multiplexer.

```sh
go install github.com/husniadil/olympus/cmd/olympus@latest
```

Or take a release archive from
[GitHub Releases](https://github.com/husniadil/olympus/releases), built for
darwin and linux on amd64 and arm64; each one carries the binary, its man pages
and its shell completions.

Check what Olympus found:

```sh
olympus doctor
```

That prints which backends are installed, which one answers and why, where its
sessions live, and what each one can do. It never fails — when nothing is
installed, explaining that is exactly its job.

Then start a session and run something in it:

```sh
olympus start build
olympus run build 'echo hello from a real terminal'
olympus screen build
olympus stop build
```

`build` is just a name. The session outlives the command that made it, so you
can come back to it from another shell, another process, or tomorrow.

---

## Three doors, one vocabulary

Every operation has exactly one name, one set of options, and one result shape.
The CLI verb, the Go method and the MCP tool are three spellings of the same
thing.

### The CLI

```sh
olympus start build --dir /repo
olympus send build 'make test'        # types it, confirms it landed, submits it
olympus wait build 'ok|FAIL'          # block until the output says something
olympus screen build api docs         # several sessions in one call
olympus watch build                   # follow output as it is produced
olympus panes                         # every pane, across every session
olympus self                          # which session am I running in?
olympus status --set ready            # from inside: tell whoever is driving
olympus status build --wait ready     # from outside: block until it says so
olympus capabilities                  # what this backend can do
olympus servers                       # the servers behind the sessions
olympus agents                        # which panes a coding agent is running in
olympus --server work ls              # address one of them by name
olympus run 'go build ./...'          # no target: a throwaway session
olympus attach build                  # hand this terminal over
olympus attach build:1 --bare         # one window, no chrome, nobody else moved (tmux)
olympus view create build --window 1  # the same view, kept, to scroll or attach later
olympus view focus <view> --col 52 --row 3  # select the pane under a cell, for clients with the mouse off
```

That is a sample, not the whole surface — `olympus --help` lists every verb, and
each has its own `--help` explaining the parts that are not obvious.

**Wait for the program's output, not for your prompt.** `wait` matches per line,
so `'\$\s*$'` looks like a natural "back at the prompt" pattern — and it is only
your prompt: it never matches under zsh, fish, or anything with a themed or
two-line prompt. Match on something the command itself prints and the pattern
works on everyone's machine.

Add `--json` to any verb for a stable, machine-readable envelope. Human output
is for reading and may change in any release; `--json` will not.

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

`Session` is create-or-reuse, so there is no separate "does it exist yet"
decision to get wrong.

### The MCP server

```sh
olympus mcp
```

stdio only — point an MCP client at it as a subprocess. It targets MCP revision
`2026-07-28` and still serves clients using the older `initialize` handshake, so
both eras work.

```json
{
  "mcpServers": {
    "olympus": { "command": "olympus", "args": ["mcp"] }
  }
}
```

### A skill for agents

`--help` says what each verb does; it cannot say which verb fits which
situation, or name the traps that only appear when an agent strings verbs
together — `run` against a REPL, `wait` on its own prompt, control keys on zmx.
[`skills/olympus/SKILL.md`](skills/olympus/SKILL.md) carries that. It is
written for any harness that loads `SKILL.md` files; with Claude Code, copy the
directory to `~/.claude/skills/olympus/`. It describes the CLI and maps the MCP
tool names onto it, so it serves both doors.

---

## Things worth knowing early

**Sessions belong to a backend.** A session created on zmx is invisible from
tmux and vice versa. They never migrate and never merge. `olympus doctor` always
tells you which backend answered, and so does every `--json` envelope.

**The four backends are not equivalent.** zmx is the default and the least
capable of them — no views, no corpse-on-exit, no server environment, and no
reliable control keys. tmux is the most capable. meja sits between the two, with
control keys but neither views nor a server environment. herdr has control keys
and a session status, and is the one backend that cannot start a session on a
command of your choosing: its panes run the shell its own configuration names,
so `--command` is refused there rather than typed into a shell. Operations that
mean less on the resolved backend say so on stderr rather than failing, so a
successful result is never quietly narrower than you think. The capability
matrix in `olympus doctor` is the one place that lays this out.

**Typing and submitting are separate.** `type` places text without pressing
Enter; `send` confirms the text actually landed on screen and only then submits
it. That distinction is what makes automation against a real terminal reliable
rather than hopeful.

**A failing command is not an error.** `olympus run` reports the command's own
exit code — with `--json` it is in `data.exit_code` and the process exits 0;
without it, the process exits with the command's status so it composes in a
pipeline like running the command directly.

**It drives interactive programs, not just commands.** `run` uses shell syntax
to mark a command's start and end, so it needs a shell — point it at a REPL and
it will time out. Drive a REPL or a full-screen program with `send`, `press` and
`wait` instead, and read it with `screen`. Patterns are matched per line, and
should not require a trailing space: write `^>>>\s*$`, not `^>>> $`, because
whether that space survives into a capture differs by backend.

**Driving a full-screen program needs tmux, meja or herdr.** All four backends *show*
you one correctly — a repaint is captured as it currently looks. But zmx does not
reliably deliver control keys, so an editor opened there can be typed into and
read, and never saved or exited: Ctrl-O and Ctrl-X simply do not arrive.
`olympus capabilities` reports this as `control_keys`, and `olympus doctor`
shows it for every backend. Everything else — commands, REPLs, reading output —
works the same on either.

**Where sessions live differs.** On tmux, Olympus uses its own socket, so its
sessions do not show up in a plain `tmux ls`. On zmx there is no socket
equivalent — sessions are global to your daemon and appear in your own
`zmx list`. `olympus doctor` states which is in effect.

**Servers are the level above sessions.** `olympus servers` lists the ones the
backend can see — tmux's named sockets, herdr's named sessions, zmx's one
directory — with whether each is running, and `--server <name>` points any verb
at one of them by name instead of by socket. `olympus servers stop <name>` takes
one down with every session on it. What a server *is* differs by backend and
the listing says so; meja cannot enumerate its profiles, so it has neither.

**Agents are found in panes, on every backend.** `olympus agents` lists the
coding agents running — which pane, which agent, its directory, and whether
it is `working`, `idle` or `blocked` on a prompt — and each row says how it
was found. On herdr, which watches its panes for agents itself, rows carry
the agent's status natively, what it is working on and its usage bars; on
the other backends a row is a pane whose processes include a known agent
(`claude`, `codex`, `gemini`, `aider`, `opencode`, `goose`, `amp`, `cursor`,
`pi`, `omp`, `copilot`, `devin`, `agy`, `cline`, `droid`, `kimi`, `kiro`,
`kilo`, `hermes`, `qodercli`, `qwen`, `mastracode`, `maki`, `muse`, `grok`) —
found by walking the pane's process tree, so an agent running under the
pane's shell counts — and its status is read off a capture of the pane by
the agent's manifest, one capture per row. `status_source` says which
(`native` or `screen`); what no rule recognises is `unknown` rather than
guessed. `olympus capabilities` reports `agent_status` where the rows can
carry one.

Two more backends are supported and come last in that order, each answering only
when nothing before it is installed, since sessions never migrate between
backends. Both take `--socket-path`, which is the only form offered on either.

[**meja**][meja] keeps a server's saved sessions beside its socket, so a named
profile would write into your own store.

[**herdr**][herdr] needs the path for a sharper reason: it keeps a session's
saved layout in its *configuration* directory rather than beside its socket, so
Olympus moves that directory along with the socket. Without that, a second server
would overwrite your own `~/.config/herdr/session.json` — your saved workspaces —
while touching none of your live sessions. Olympus defaults to a socket of its
own there, so its workspaces never appear in your herdr unless you point
`--socket-path` at your server yourself — which is a supported mode, and the
reason this backend is interesting: it lets you list, read, drive and attach to
panes that other tools created on a herdr you already run. Olympus never starts,
reconfigures or stops a server it found; asking it to stop one is refused,
because that would take every pane on it down.

A herdr *workspace* is a session there, a *tab* is a window and a pane is a
pane — the same shape tmux gets. `olympus ls` lists the workspaces, named by
their label (`demo`) or, where the label is empty, by their id (`w25`); `olympus
panes demo` lists every pane in one, with the tab's number as `window_index`. A
verb aimed at a workspace acts on the pane it is showing; `w25:p8` reaches
exactly that pane, and `w25:t2` the pane that tab is showing. `olympus stop`
closes the level you named, with everything in it. `attach --client` opens
herdr's own client — sidebar, tabs, mouse — focused onto the target, and
`olympus rename w25:p8 build` labels the pane (or a tab, or a workspace) for
every client, and `olympus focus w25:p8` moves the focus later without attaching: every client
on the server shows the one focus, so a caller holding two clients re-steers
when it brings one to the front. You cannot
*create* a session whose name is spelled like a workspace, tab or pane id.

A private socket is not a private configuration: tmux fixes a server's settings
at boot from your `tmux.conf`, so your file reaches Olympus's sessions whichever
socket they are on. That is mostly what you want — attach to one and you get
your own prefix, bindings and theme. Two options are pinned back, because
Olympus's own correctness rests on them: `default-command`, which decides the
shell that writes a run's exit marker, and `history-limit`, which decides what a
capture of N lines can return. `olympus doctor` names both under *what Olympus
overrides in your tmux config*. Nothing cosmetic is touched.

That applies only to servers Olympus starts. Point it at a tmux server you were
already running and it changes nothing there at all: those options are global to
a server, so pinning them would alter every session on it, including the ones
you never asked Olympus about. `doctor` says which case you are in and what the
options are actually set to.

You can put the tmux socket wherever you like with `--socket-path`, rather than
letting tmux choose the directory:

```sh
olympus --backend tmux --socket-path ./.olympus/sock start build
```

That keeps a project's sessions with the project, and the socket disappears when
the directory does. `--socket` takes a plain name instead and lets tmux place
it. The two address different servers, so sessions created under one are not
visible under the other.

---

## Exit codes

| Code | Meaning |
|---|---|
| 0 | success |
| 1 | something unexpected — retrying will not help |
| 2 | usage: one corrected argument fixes it |
| 3 | the session or pane does not exist |
| 4 | the backend could not be reached |
| 5 | timed out |
| 6 | someone else holds the session |
| 7 | the backend has no such concept |

Two verbs deliberately differ, and say so in their own `--help`: `run` reports
the command's exit code, and `attach` reports the multiplexer client's.

---

## Documentation

- [`docs/terminal-behavior.md`](docs/terminal-behavior.md) — the normative spec
  for how Olympus drives a multiplexer. It is written first and the code follows
  it; most of its rules exist because the obvious implementation is wrong.
- [`docs/api.md`](docs/api.md) — the contract the three doors share: vocabulary,
  envelope shape, error codes, payloads, stability guarantees.
- [`docs/roadmap.md`](docs/roadmap.md) — the ordered phases and what "done" means
  for each.

---

## Building from source

```sh
make build      # ./bin/olympus
make install    # go install ./cmd/olympus
make test       # the fast loop, seconds — no multiplexer needed
make test-full  # the gate: adds every case that drives a real terminal
```

Olympus runs on **macOS and Linux**. Its backends are Unix programs and its
attach path is termios and flock all the way down.

Tests never touch your live sessions: the tmux tests use a private socket and
the zmx tests a private `ZMX_DIR`. A backend that is absent — or on PATH but not
runnable, which is what a leftover version-manager shim looks like — skips
loudly rather than failing the suite, and the whole suite passes with no
multiplexer installed at all.

A third-party backend can prove itself against the same conformance suite the
shipped ones run — `backend/backendtest` is exported for exactly that, and
[`docs/adding-a-backend.md`](docs/adding-a-backend.md) is the route from a spike
to a reviewable pull request.

---

## Status

Pre-1.0. The `--json` envelope, the error-code vocabulary, the CLI verb and flag
names, and the MCP tool and parameter names are semver-bound once released:
additive only, never repurposed or removed within a major version. Human-readable
output is not stable and should not be parsed.

## License

MIT. See [LICENSE](LICENSE).

Agent status manifests are herdr's, Apache 2.0, © herdr authors: the files
under `internal/agentstate/manifests/`, with the license text beside them and
the vendored commit recorded in [NOTICE](NOTICE).
