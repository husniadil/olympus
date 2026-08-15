# Implementation roadmap

Ordered phases from an empty module to a releasable v0.1.0. Each phase is a
checkpoint: it ends green, it ends committed, and it ends with the specs updated
if implementation disproved them.

**Development is test-first.** Within every phase the order is: write the failing
test, make it pass, refactor. The conformance suite is written *before* the
backends it tests, so a backend is developed against an executable definition of
correct rather than against prose.

Specs: [`terminal-behavior.md`](terminal-behavior.md) for mechanics,
[`api.md`](api.md) for the exposed contract.

---

## Phase 1 — the mechanical layer

`backend/` — types and the interface backends implement. No backend yet.

- Error vocabulary and codes with their exit mapping (behavior §12).
- Row types: session, pane, capture metadata, capabilities, liveness tri-state,
  probe tri-state (behavior §3, §5.4, §13; shapes per api §5).
- The `Backend` interface itself.
- Target resolution helper (behavior §10).

**Done when**: types compile, the error-to-exit-code mapping is unit-tested
exhaustively, and JSON field names match api §5 exactly — pinned by a
marshalling test, since those names are semver-bound from first release.

## Phase 2 — the conformance suite

`backend/backendtest/` — the executable definition of a correct backend, written
against the interface and exported for third-party backends.

- Subtests per behavior-spec section, each naming the rule it enforces.
- Per-backend and per-session-shape expectations where the spec requires them
  (behavior §2.8.1), rather than one blanket expectation.
- The isolation discipline of behavior §2.9 built into the harness: private tmux
  socket, private `ZMX_DIR`, for the suite *and* any raw verification call.
- The testing requirements of behavior §16: shell warm-up, assert substituted
  output never the typed string, anchor sessions, plausibility assertions.

**Done when**: the suite compiles and runs against a stub, failing for the right
reasons. It cannot pass yet — that is the point.

## Phase 3 — the tmux backend

`backend/tmux/`, developed against the Phase 2 suite.

Covers behavior §1.1–1.2, §2.1–2.2, §2.6–2.7, §3.2–3.4, §4.1–4.4, §4.8, §5.1,
§5.3–5.4, §9, §10.

**Done when**: the full conformance suite is green on tmux, and the tmux-only
tests for the rules the interface cannot observe are green too.

## Phase 4 — the zmx backend

`backend/zmx/`, against the same suite.

Covers behavior §1.3, §2.3–2.5, §2.8.1, §3.1, §4.5–4.7, §5.2, plus every
unsupported-class and degraded-operation answer (§0.8).

**Done when**: the suite is green on zmx with its documented per-backend
expectations, and `SIGINT`-to-the-foreground-pgid is verified against both
session shapes (behavior §2.8.1).

## Phase 5 — shared engines

Backend-agnostic logic above the interface, unit-testable against a fake backend
and then exercised through the suite.

- Per-session write lock and its scope rules (behavior §11).
- Idempotent ensure (§2.6).
- The sentinel run protocol: marker parsing, window growth, detached runs
  (§6). Marker parsing is a pure function and gets exhaustive table tests.
- Verified delivery: normalization, per-line and pair matching, one resend (§7).
- Graceful kill decision engine with injected operations (§2.8).
- Exit-marker inspection (§14).

**Done when**: each engine has unit tests against a scripted fake, and the
end-to-end paths are green on both backends.

## Phase 6 — the ergonomic layer

`olympus` root package: `Open`, `Session`, options, typed errors.

**First task of this phase is to finalize api §6**, which is currently
provisional — the `Backend` interface now exists, so the sketch can become a
contract.

**Done when**: the documented examples compile and run as tests, and every
default in behavior §17.3 is decided exactly once, here.

## Phase 7 — the CLI

`cmd/olympus`, built on the ergonomic layer.

- The verb surface of api §1, positional targets everywhere.
- The envelope of api §2, with argument-parsing errors intercepted into it
  (behavior §12.2) — this needs deliberate wiring, not the framework default.
- Human output: tables, colour only on a TTY, and the `--json`-for-scripts note
  in help.
- `doctor` (behavior §0.6) including the capability matrix.
- Degraded-operation warnings to stderr (behavior §0.8).

**Done when**: golden tests cover both output modes, every documented exit code
is asserted, and no diagnostic reaches stdout.

## Phase 8 — the MCP door

`internal/mcp`, stdio only, on the official Go SDK.

**Done when**: the six conformance assertions of behavior §15.7 pass — including
that a legacy `initialize` still negotiates and serves the same tools.

## Phase 9 — release readiness

- CI: macOS and Linux, tmux matrix, zmx leg skipping loudly when absent.
- README with the three doors at equal billing, opening on a quickstart.
- CONTRIBUTING, SECURITY, CODE_OF_CONDUCT, issue and PR templates.
- Shell completions and man pages generated from the CLI.
- goreleaser, Homebrew tap, `go install`.
- CHANGELOG and the stability commitments of api §7.

**Done when**: a clean checkout on a fresh machine can install, run `doctor`, and
start a session by following only the README.

---

## Standing rules for every phase

- **Test-first.** Failing test, then the code that passes it.
- **The specs are amendable.** If implementation disproves a rule, change the
  spec in the same commit as the code that proved it, and say what moved.
- **Never touch a live daemon or default socket** — private `ZMX_DIR` and private
  `-L` socket, for probes as well as tests, cleaned up afterwards.
- **Race-shaped fixes need reproducing tests** (behavior §16). A test that passes
  once against the fix proves nothing.
- **Commit at checkpoints**, small enough to review and revert alone.
