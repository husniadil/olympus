# Contributing

Thanks for looking. A few things about this codebase will save you time.

## Build

You need Go 1.26.5 or newer, on macOS or Linux.

```sh
make build      # ./bin/olympus
make install    # go install ./cmd/olympus
make doc        # man pages and shell completions into .gendoc
make clean      # bin, dist, .gendoc, coverage.out
```

`make doc` generates from the command tree, so the pages cannot describe a
surface the binary no longer has.

## The gate

```sh
make test       # the loop: seconds, no multiplexer needed
make test-full  # the gate: everything, with -race. What CI runs.
```

- **`make test`** runs on every edit: a `gofmt` check, `go vet`, and every case
  that does not drive a real terminal.
- **`make test-full`** adds every case that drives a terminal, runs with
  `-race`, and runs `go vet` for the other supported platform.

A green `make test` is not a green gate. Nothing is committed on it alone.

Formatting is checked, not applied, so the fix lands in the commit that caused
it.

### Missing backends skip

Both targets pass with no multiplexer installed. A backend that is absent, or on
PATH but not runnable, skips loudly rather than failing.

#### Why

A version-manager shim left behind by an uninstalled tool satisfies a PATH
lookup and fails every call. Checking that the binary runs turns a wall of
broken cases into one honest skip.

## CI

CI (`.github/workflows/ci.yml`) runs `make test-full` on `ubuntu-latest` in two
legs:

| Leg | tmux | zmx | meja | herdr |
|---|---|---|---|---|
| Newest | system package | 0.7.0 | 0.0.26 | 0.8.2 |
| Floor | 3.3, built from source | 0.6.0 | 0.0.25 | 0.8.2 |

herdr's floor and newest release are the same version, so both legs run 0.8.2.

- macOS is not in the matrix. The darwin build is still checked by the
  cross-compile `go vet` inside `make test-full`, but the conformance suite on
  macOS runs only on a developer machine.
- A backend that cannot be installed skips its leg with a `::warning::` rather
  than passing quietly.

### Why every version is pinned

An `@latest` install moved meja from 0.0.25 to 0.0.26, which stopped requiring
an attached client for ordinary input. The gate changed subject without a
commit. The floor leg is also what keeps the transient-client path covered,
since the newest meja no longer takes it.

## The specification comes first

[`docs/terminal-behavior.md`](docs/terminal-behavior.md) is normative. Read the
relevant section before touching a backend, the run protocol, input injection,
attach, or locking.

If implementation shows a rule is wrong, incomplete, or unimplementable, change
it in the same commit as the code that proved it, and say what moved.

### Why

The obvious implementation of most of its rules is wrong, and each rule says
why. A spec that drifts from the code is worse than no spec, because it is still
believed.

## Development is test-first

Failing test, then the code that makes it pass. The conformance suite in
`backend/backendtest` was written before the backends it tests.
[`docs/adding-a-backend.md`](docs/adding-a-backend.md) is the route for a new
backend.

Two habits matter:

- **Assert the substituted output, never the typed string.** A terminal echoes
  what you type. Use `printf 'marker-%d\n' 42` and assert on `marker-42`.
- **Race-shaped fixes need reproducing tests.** Revert the fix and watch the
  test fail before you believe it.

## Tests must never touch your live sessions

Session-name prefixes do not protect anyone. Each backend needs a server nobody
else addresses:

| Backend | Isolation |
|---|---|
| tmux | a socket at a private path inside a directory the test owns (`--socket-path`, `tmux.WithSocketPath`) |
| zmx | `ZMX_DIR` set to a private temp dir, for the backend and for every raw verification call |
| meja | a socket path (`-S`, `meja.WithSocketPath`), never a profile name (`-L`) |
| herdr | a private socket path (`herdr.WithSocketPath`), with the configuration and state directories moved with it |

### Why

- **tmux:** killing a server does not unlink its socket, so a named socket
  accumulates in the directory shared with your own servers.
- **zmx:** there is no socket flag. Every session lands on the one shared daemon.
- **meja:** it keeps session recovery files beside the socket. A named profile
  would leave test sessions in your own store, to reappear on your next restore.
- **herdr:** it keeps a session's saved layout in its configuration directory,
  not beside the socket. A private socket alone still overwrites your
  `~/.config/herdr/session.json`.

Spec §2.9 has the full rules.

## Dependency budget: three libraries

cobra, creack/pty, and the official MCP Go SDK. Adding or swapping one is a
deliberate decision, recorded in `CLAUDE.md` in the same commit that makes it.

The terminal-attribute handling in `internal/engine/termios.go` is standard
library for that reason.

## What is semver-bound

- The `--json` envelope shape, `data` field names and types
- Error codes and their exit codes
- MCP tool and parameter names
- CLI verb and flag names

Additive only: a shipped name is never repurposed or removed within a major
version. Marshalling tests pin these, so a diff there is a decision, never a
refactor's side effect.

Human-readable output is not stable.

## Commits

Small, working increments, so each step is reviewable and revertible on its
own. Explain why, not what.

## Releasing

A release is cut by pushing a `v*` tag. `.github/workflows/release.yml` then:

1. Installs tmux, zmx, meja and herdr, and fails if any is missing.
2. Runs `make test-full`.
3. Checks the working tree is clean.
4. Runs goreleaser (`.goreleaser.yaml`), which builds darwin and linux on amd64
   and arm64 and stamps the tag into `Version`.

Add the release's entry to [`CHANGELOG.md`](CHANGELOG.md) before tagging.

### Why tags only

Every door reports the version stamped from the tag. A release built from
anything else would publish binaries that report the wrong version.
