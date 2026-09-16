# Olympus

A terminal you can drive from code. Olympus creates, drives, observes and tears
down real terminal sessions on top of a multiplexer it does not embed: zmx by
default, then tmux, meja and herdr. It exposes that through three equal doors:
a Go package, a CLI, and a stdio MCP server.

macOS and Linux only. herdr has a Windows build, but Olympus's attach path is
termios and flock throughout.

## Commands

| Target | What it does |
|---|---|
| `make test` | The fast loop, in seconds: everything that does not need a multiplexer. Run it on every edit. |
| `make test-full` | The gate, and what CI runs: everything, with `-race`, plus a cross-compile `go vet` for linux/amd64 and darwin/arm64. Run it before every commit. |
| `make build` / `make install` | Build or install `./cmd/olympus`. |
| `make doc` | Man pages and shell completions into `.gendoc`, generated from the command tree. The release ships the same output. |
| `make clean` | Remove `bin`, `dist`, `.gendoc`, `coverage.out`. |

- Both test targets check `gofmt` and run `go vet` first. Formatting is checked,
  not applied: the fix belongs in the commit that caused it.
- A green `make test` is not a green gate. Nothing is committed on it alone.
- Tests skip rather than fail when a backend is absent, and each leg skips
  loudly. "On PATH" and "runnable" are checked separately, since a
  version-manager shim satisfies a lookup and fails every call.

### Version floors

Reported by `doctor`, not enforced on the hot path (behavior §0.5, `floors` in
`doctor.go`):

| Backend | Floor |
|---|---|
| tmux | 3.3 |
| zmx | 0.6.0 |
| meja | 0.0.25 |
| herdr | 0.8.2 |

CI pins every backend version and runs a floor leg. See the CI section of
[`CONTRIBUTING.md`](CONTRIBUTING.md) for the matrix and why.

## The specification comes first

- [`docs/terminal-behavior.md`](docs/terminal-behavior.md) is the normative spec
  for how Olympus drives a multiplexer. The obvious implementation of most of
  its rules is wrong.
- [`docs/api.md`](docs/api.md) is the contract for what Olympus exposes: one
  vocabulary across three doors, the envelope, error and exit codes, payload
  shapes, stability guarantees.

Read the relevant section before touching a backend, the run protocol, input
injection, attach, or locking.

If you change a specified behavior, update the spec in the same commit. A spec
that drifts from the code is worse than none, because it is still believed.

## Non-negotiables

### 1. Neutrality

- No exported identifier, file, or package name refers to a specific consumer,
  product, or vendor. Names describe terminals, not whatever drives them.
- The rule covers names, not comments. Explaining that a submit is paced because
  a particular REPL treats text-plus-terminator as a paste is accurate prose.

### 2. Dependency budget: three libraries

- cobra, creack/pty, and the official MCP go-sdk
  (`github.com/modelcontextprotocol/go-sdk`, pinned v1.7.0).
- Adding or swapping one is a deliberate decision, recorded here in the same
  commit that makes it.

### 3. The `--json` shape and error codes are semver-bound

- A shipped field or code is never repurposed or removed. Only new ones are
  added. The vocabulary is spec §12; the commitment is api §7.
- `Version` in `version.go` falls under the same section. It is the literal
  every door reports and a consumer floor-checks against.
  - It is a `var` for the linker, not for callers. The release stamps the tag
    into it with goreleaser's `-X github.com/husniadil/olympus.Version`.
  - When nothing stamped it, `init` falls back to the module version from
    `debug.ReadBuildInfo`, so `go install …@vX.Y.Z` reports the tag. A checkout
    build reads `(devel)` and keeps the placeholder.

### 4. No HTTP server, no daemon, no persistent state

- MCP is stdio only, targeting revision `2026-07-28`, whose request model is
  stateless (spec §15).
- Statelessness holds in both directions: §6.7 explains why a run registry is
  ruled out, not merely absent.

### 5. Tests never touch the operator's live sessions

Session-name namespacing alone is not enough for any backend (spec §2.9).

| Backend | Isolation | Why |
|---|---|---|
| tmux | Socket at a private path in a directory the test owns | Killing a server does not unlink its socket, so a named socket would pile up beside the operator's own. |
| zmx | Private `ZMX_DIR` | zmx has no socket flag. |
| meja | Socket path (`-S`), never a profile name (`-L`) | meja stores session recovery files beside its socket, so a named profile leaves test sessions in the operator's store to reappear on restore. |
| herdr | Private socket path plus private configuration and state directories | herdr keeps a session's saved layout in its configuration directory, so a private socket alone still overwrites the operator's `~/.config/herdr/session.json`. |

### 6. Both audiences are first-class

A human typing a verb and reading prose, and a program parsing stdout, are both
supported callers. A change that serves one at the other's expense needs a
reason.

## Working agreements

- **Test-first.** Failing test, then the code that passes it, then refactor. The
  conformance suite defines correct before a backend is written against it.
- **The specs are amendable.** If implementation shows a rule is wrong,
  incomplete, or unimplementable, change the spec in the same commit as the code
  that proved it.
- **Commit at checkpoints.** Small, working increments, each reviewable and
  revertible on its own.

## Layering

```
doors:      Go package (olympus) · CLI (cmd/olympus) · MCP (internal/mcp)
                              │
ergonomic:  package olympus: Session handle, options, defaults, typed errors
                              │
mechanical: package backend: the interface backends implement
                    ├── backend/tmux
                    ├── backend/zmx
                    ├── backend/meja
                    ├── backend/herdr
                    └── backend/backendtest: the conformance suite
```

- Defaults are decided once, in the ergonomic layer. A door that invents its own
  default has introduced a second contract.
- The mechanical layer stays explicit and complete. That is where the spec's
  rules live.
- `backend/backendtest` is exported so a third-party backend can prove itself
  against the same suite.

### The MCP tool surface is pinned

`ToolNames` in `internal/mcp/tools.go` lists the 34 tools. Two tests in
`internal/mcp/conformance_test.go` hold it:

- `TestTheToolSurfaceIsPinned` compares the served tools with `ToolNames`.
- `TestTheToolNamesMatchTheSpecTable` compares the served tools with a list
  transcribed by hand from the api §1 table. No test reads `docs/api.md` itself.

Adding or renaming a tool means `ToolNames`, the registration, the transcribed
list, and the api §1 table all move together.

## Where to look

| File | What it holds |
|---|---|
| [`docs/terminal-behavior.md`](docs/terminal-behavior.md) | The behavior spec. |
| [`docs/api.md`](docs/api.md) | The exposed contract. |
| [`docs/adding-a-backend.md`](docs/adding-a-backend.md) | The route for a new backend: spike first, isolation, the conformance suite as the definition of correct. |
| [`docs/known-issues.md`](docs/known-issues.md) | What is still outstanding: attach by a human, macOS outside CI, the stdio-only MCP door, the undiagnosed meja flake. |
| [`skills/olympus/SKILL.md`](skills/olympus/SKILL.md) | Teaches an agent harness when Olympus beats a plain shell and which verb fits. A shipped surface: a changed verb or MCP tool is reflected there too. |
