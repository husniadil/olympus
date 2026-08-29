# Olympus

A terminal you can drive from code. Olympus creates, drives, observes and tears
down real terminal sessions on top of a multiplexer it does not embed — zmx by
default, tmux as the alternative, then meja and herdr — and exposes that through
three equal doors: a Go package, a CLI, and a stdio MCP server.

## Commands

- `make test` — the fast loop, seconds: everything that does not need a
  multiplexer. Run it on every edit.
- `make test-full` — **the gate**, and what CI runs: the above plus every case
  that drives a real terminal, with `-race` and a cross-compile check of the
  other supported platform. Run it before every commit.
- `make build` / `make install` — `./cmd/olympus`.
- `make doc` — man pages and shell completions into `.gendoc`, generated from the
  command tree so they cannot describe a surface the binary no longer has. The
  release runs the same generator and ships the output in the archive.
- `make clean` — `bin`, `dist`, `.gendoc`, `coverage.out`.

Both test targets check `gofmt` and run `go vet` first, so either can fail on
formatting before a single test runs. The formatting is checked rather than
applied on purpose: the fix belongs in the commit that caused it.

The split is not a reduction — `test-full` runs everything — but it does mean a
green `make test` is not a green gate. Nothing is committed on it alone.

Tests skip rather than fail when a backend is absent, and the whole suite passes
with none installed at all. Each leg skips loudly when its binary is missing or
not runnable. All four carry a version floor, reported by `doctor` rather than
enforced on the hot path (behavior §0.5, `floors` in `doctor.go`): tmux 3.3, zmx
0.6.0, meja 0.0.25, herdr 0.8.2. CI pins the backend versions and runs a floor leg — `@latest`
once moved meja from 0.0.25 to 0.0.26 and took a structural change with it, so
the gate changed subject without a commit. "On PATH" and "runnable" are checked
separately: a version-manager shim satisfies a lookup and fails every call.

macOS and Linux only. tmux, zmx, meja and herdr are Unix programs — herdr has a
Windows build, but Olympus's attach path is termios and flock all the way down.

## The specification comes first

[`docs/terminal-behavior.md`](docs/terminal-behavior.md) is the normative spec
for how Olympus drives a multiplexer — not documentation written after the fact.
The obvious implementation of most of its rules is wrong.

[`docs/api.md`](docs/api.md) is the companion contract for what Olympus
*exposes*: one vocabulary across three doors, the structured envelope, error and
exit codes, payload shapes, and stability guarantees.

**Read the relevant section before touching a backend, the run protocol, input
injection, attach, or locking.** If you deliberately change a specified
behavior, update the spec in the same commit — a spec that drifts from the code
is worse than no spec, because it is still believed.

## Non-negotiables

1. **Neutrality.** Names in this repo describe terminals, not whatever is
   driving them. No exported identifier, file, or package name refers to a
   specific consumer, product, or vendor. Scope is names, not comments:
   explaining that a submit is paced because a particular REPL treats
   text-plus-terminator as a paste is accurate prose, not a violation.

2. **Dependency budget: three libraries** — cobra, creack/pty, and the official
   MCP go-sdk (`github.com/modelcontextprotocol/go-sdk`, pinned v1.7.0). Adding
   or swapping one is a deliberate decision, recorded here in the same commit
   that makes it.

3. **The `--json` shape and the error-code vocabulary are semver-bound.** A
   shipped field or code is never repurposed or removed; only new ones are
   added. The vocabulary is spec §12; the commitment itself is api §7.

   The same section governs `Version` in `version.go`. It is the one literal
   every door reports and the one a consumer floor-checks against, so it is a
   `var` for the linker, not for callers: the release stamps the tag into it
   through goreleaser's `-X github.com/husniadil/olympus.Version`. When nothing
   stamped it, `init` falls back to the module version `debug.ReadBuildInfo`
   recorded, so a `go install …@v0.1.1` build does not report the development
   placeholder; a checkout build reads `(devel)` and keeps the placeholder.

4. **No HTTP server, no daemon, no persistent state.** MCP is stdio only,
   targeting revision `2026-07-28`, whose own request model is stateless — see
   spec §15. The statelessness is load-bearing in both directions: §6.7 explains
   why a run registry is ruled out rather than merely absent.

5. **Tests must NEVER touch the operator's live sessions.** tmux tests put the
   socket at a private PATH inside a directory the test owns, so it disappears
   with the test — killing a server does not unlink its socket, and a named
   socket would accumulate in the directory shared with the operator's own
   servers. zmx has no socket flag at all, so its tests must set `ZMX_DIR` to a
   private temp dir. meja tests must use a socket PATH (`-S`), never a profile
   name (`-L`): meja stores a server's session recovery files beside its socket,
   so a named profile leaves persisted test sessions in the operator's own store
   to reappear on their next restore. herdr needs a private socket path AND the
   configuration and state directories moved with it, because it keeps a
   session's saved layout in its configuration directory rather than beside its
   socket — a private socket alone still overwrites the operator's
   `~/.config/herdr/session.json`. Session-name namespacing alone is not
   sufficient for any of them. See spec §2.9.

6. **Both audiences are first-class.** A human typing a verb and reading prose,
   and a program parsing stdout, are both supported callers. A change that
   serves one at the other's expense needs a reason.

## Working agreements

- **Development is test-first.** Failing test, then the code that makes it pass,
  then refactor. The conformance suite is written *before* the backends it
  tests, so a backend is developed against an executable definition of correct
  rather than against prose.
- **The specs are amendable.** If implementation shows a rule in
  `docs/terminal-behavior.md` or `docs/api.md` is wrong, incomplete, or
  unimplementable as written, change it — in the same commit as the code that
  proved it. The specs lead, but they are not immune to evidence.
- **Commit at checkpoints.** Small, working increments rather than one large
  drop, so each step is reviewable and revertible on its own.

[`docs/adding-a-backend.md`](docs/adding-a-backend.md) is the contributor route
for a fourth backend: spike first, isolation, the conformance suite as the
definition of correct, and the lessons that each cost a day here.

[`docs/roadmap.md`](docs/roadmap.md) has the ordered phases and what "done"
meant for each. All nine are implemented and green, so read it as the record of
how the work was done — and for its Status section, which is the short honest
list of what is genuinely still outstanding.

## Layering

```
doors:      Go package (olympus) · CLI (cmd/olympus) · MCP (internal/mcp)
                              │
ergonomic:  package olympus — Session handle, options, defaults, typed errors
                              │
mechanical: package backend — the interface backends implement
                    ├── backend/tmux
                    ├── backend/zmx
                    ├── backend/meja
                    ├── backend/herdr
                    └── backend/backendtest — the conformance suite
```

Defaults are decided **once**, in the ergonomic layer. A door that invents its
own default has introduced a second contract. The mechanical layer stays
explicit and complete; that is where the spec's rules live.

`backend/backendtest` is exported on purpose: a third-party backend should be
able to prove itself against the same suite the shipped ones run.

The MCP door's surface is pinned the same way: `ToolNames` in
`internal/mcp/tools.go` lists the 25 tools, exported and asserted against both
the registered set and the spec's own table, so a tool cannot silently appear or
vanish. Adding or renaming one means the list, the registration, and the api §1
table move together.

[`skills/olympus/SKILL.md`](skills/olympus/SKILL.md) teaches an agent harness
when Olympus beats a plain shell and which verb fits which situation. It is a
shipped surface: a verb or MCP tool that changes has to be reflected there too.
