# Olympus

A terminal you can drive from code. Olympus creates, drives, observes and tears
down real terminal sessions on top of a multiplexer it does not embed — zmx by
default, tmux as the alternative — and exposes that through three equal doors: a
Go package, a CLI, and a stdio MCP server.

## Commands

- `make test` — the gate: gofmt check, `go vet ./...`, `go test ./...`.
- `make build` / `make install` — `./cmd/olympus`.

Tests require at least one backend installed. The tmux leg needs tmux ≥ 3.3; the
zmx leg skips loudly when zmx is absent rather than failing the suite.

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
   added. See spec §12.

4. **No HTTP server, no daemon, no persistent state.** MCP is stdio only,
   targeting revision `2026-07-28`, whose own request model is stateless — see
   spec §15. The statelessness is load-bearing in both directions: §6.7 explains
   why a run registry is ruled out rather than merely absent.

5. **Tests must NEVER touch the operator's live sessions.** tmux tests use a
   private `-L` socket; zmx has no `-L` equivalent, so zmx tests must set
   `ZMX_DIR` to a private temp dir. Session-name namespacing alone is not
   sufficient. See spec §2.9.

6. **Both audiences are first-class.** A human typing a verb and reading prose,
   and a program parsing stdout, are both supported callers. A change that
   serves one at the other's expense needs a reason.

## Working agreements

- **The spec is amendable.** If implementation shows a rule in
  `docs/terminal-behavior.md` is wrong, incomplete, or unimplementable as
  written, change it — in the same commit as the code that proved it. The spec
  leads, but it is not immune to evidence.
- **Commit at checkpoints.** Small, working increments rather than one large
  drop, so each step is reviewable and revertible on its own.

## Layering

```
doors:      Go package (olympus) · CLI (cmd/olympus) · MCP (internal/mcp)
                              │
ergonomic:  package olympus — Session handle, options, defaults, typed errors
                              │
mechanical: package backend — the interface backends implement
                    ├── backend/tmux
                    ├── backend/zmx
                    └── backend/backendtest — the conformance suite
```

Defaults are decided **once**, in the ergonomic layer. A door that invents its
own default has introduced a second contract. The mechanical layer stays
explicit and complete; that is where the spec's rules live.

`backend/backendtest` is exported on purpose: a third-party backend should be
able to prove itself against the same suite the shipped ones run.
