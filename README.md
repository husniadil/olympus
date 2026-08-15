# Olympus

A terminal you can drive from code.

Olympus creates, drives, observes and tears down real terminal sessions — the
kind that survive you closing your laptop — and exposes that through three equal
doors: a **Go package**, a **CLI**, and a **stdio MCP server**.

It does not embed a multiplexer. It drives one you already have: `zmx` by
default, [`tmux`][tmux] as the alternative.

[tmux]: https://github.com/tmux/tmux

---

## Quickstart

You need Go 1.26+ and at least one multiplexer.

```sh
go install github.com/husniadil/olympus/cmd/olympus@latest
```

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
olympus wait build '\$\s*$'           # block until the prompt comes back
olympus screen build api docs         # several sessions in one call
olympus watch build                   # follow output as it is produced
olympus panes                         # every pane, across every session
olympus capabilities                  # what this backend can do
olympus run 'go build ./...'          # no target: a throwaway session
olympus attach build                  # hand this terminal over
```

That is a sample, not the whole surface — `olympus --help` lists every verb, and
each has its own `--help` explaining the parts that are not obvious.

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

---

## Things worth knowing early

**Sessions belong to a backend.** A session created on zmx is invisible from
tmux and vice versa. They never migrate and never merge. `olympus doctor` always
tells you which backend answered, and so does every `--json` envelope.

**The two backends are not equivalent.** zmx is the default and the less capable
of the two — no views, no corpse-on-exit, no server environment. Operations that
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
it will time out. Drive a REPL or a full-screen program with `send`, `key` and
`wait` instead, and read it with `screen`. Patterns are matched per line, and
should not require a trailing space: write `^>>>\s*$`, not `^>>> $`, because
whether that space survives into a capture differs by backend.

**Driving a full-screen program needs tmux.** Both backends *show* you one
correctly — a repaint is captured as it currently looks. But zmx does not
reliably deliver control keys, so an editor opened there can be typed into and
read, and never saved or exited: Ctrl-O and Ctrl-X simply do not arrive.
`olympus capabilities` reports this as `control_keys`, and `olympus doctor`
shows it for both backends. Everything else — commands, REPLs, reading output —
works the same on either.

**Where sessions live differs.** On tmux, Olympus uses its own socket, so its
sessions do not show up in a plain `tmux ls`. On zmx there is no socket
equivalent — sessions are global to your daemon and appear in your own
`zmx list`. `olympus doctor` states which is in effect.

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
make build     # ./bin/olympus
make install   # go install ./cmd/olympus
make test      # the gate: gofmt, go vet, go test
```

Tests need at least one backend installed, and never touch your live sessions:
the tmux tests use a private socket and the zmx tests a private `ZMX_DIR`. The
zmx leg skips loudly when zmx is absent rather than failing the suite.

A third-party backend can prove itself against the same conformance suite the
shipped ones run — `backend/backendtest` is exported for exactly that.

---

## Status

Pre-1.0. The `--json` envelope, the error-code vocabulary, the CLI verb and flag
names, and the MCP tool and parameter names are semver-bound once released:
additive only, never repurposed or removed within a major version. Human-readable
output is not stable and should not be parsed.

## License

MIT. See [LICENSE](LICENSE).
