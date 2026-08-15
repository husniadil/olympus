# Contributing

Thanks for looking. A few things about this codebase will save you time.

## The specification comes first

[`docs/terminal-behavior.md`](docs/terminal-behavior.md) is normative, not
documentation written after the fact. **The obvious implementation of most of
its rules is wrong**, and each rule says why. Read the relevant section before
touching a backend, the run protocol, input injection, attach, or locking.

If implementation shows a rule is wrong, incomplete, or unimplementable as
written — change it, in the same commit as the code that proved it, and say what
moved. A spec that drifts from the code is worse than no spec, because it is
still believed.

## Development is test-first

Failing test, then the code that makes it pass. The conformance suite in
`backend/backendtest` was written *before* the backends it tests, so a backend
is developed against an executable definition of correct rather than against
prose.

Two habits matter more than they look:

- **Assert the substituted output, never the typed string.** A terminal echoes
  what you type, so asserting on a literal proves only that it was typed. Use
  `printf 'marker-%d\n' 42` and assert on `marker-42`.
- **Race-shaped fixes need reproducing tests.** A test that passes once against
  the fix proves nothing. Revert the fix and watch the test fail before you
  believe it.

## Tests must never touch your live sessions

This is not negotiable, and it is easy to get wrong:

- **tmux**: put the socket at a private PATH inside a directory the test owns
  (`--socket-path` / `tmux.WithSocketPath`), so it disappears with the test.
  A NAME works too but leaves the socket behind — killing a server does not
  unlink it — and those accumulate in the directory shared with your own
  servers.
- **zmx**: there is no socket flag at all. Session-name namespacing does **not**
  protect anyone: every test session still lands on the one shared daemon. Set
  `ZMX_DIR` to a private temporary directory, for the backend *and* for every
  raw verification call.

## The gate

```sh
make test
```

gofmt, `go vet`, and the full suite. It is what CI runs.

## Dependency budget: three libraries

cobra, creack/pty, and the official MCP Go SDK. That is the whole list. Adding
or swapping one is a deliberate decision, recorded in `CLAUDE.md` in the same
commit that makes it — the terminal-attribute handling in
`internal/engine/termios.go` is about forty lines of standard library precisely
because a fourth dependency was not worth it.

## What is semver-bound

The `--json` envelope shape, `data` field names and types, error codes and their
exit codes, MCP tool and parameter names, CLI verb and flag names. Additive
only: a shipped field is never repurposed or removed within a major version.
There are marshalling tests pinning these — a diff there should be a decision,
never a refactor's side effect.

Human-readable output is explicitly *not* stable.

## Commits

Small, working increments rather than one large drop, so each step is reviewable
and revertible on its own. Explain **why**, not what — the diff already says
what.
