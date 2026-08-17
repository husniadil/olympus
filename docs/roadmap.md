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

## Status

**All nine phases are implemented and green.** The plan below is kept as the
record of the order the work was done in and what each phase had to satisfy —
not as a description of work still to come.

What is genuinely outstanding, which is a shorter list than the plan and is
deliberately not hidden inside it:

- **`attach` is now covered end to end**, against a PTY the test owns: the
  terminal is handed back as it was found, both resize routes reach the pane,
  and the slot is arbitrated across real processes including a holder killed
  with SIGKILL. What is still exercised only by hand is a HUMAN attaching —
  keystroke feel, detach keys, and how a full-screen program looks to a person.
- **CI runs all three backends on both platforms**, and found defects across
  its first runs — nearly every one a test that was wrong about its environment
  rather than a bug in the code under it. tmux is covered at the
  3.3 floor and at each platform's system version; zmx at 0.6.0 (the floor) and
  0.7.0 (the newest release); meja at 0.0.25. A backend that cannot be
  installed skips its leg with a `::warning::` rather than passing quietly.
- **v0.1.0 is cut**, from a tag and nowhere else: the version every door reports
  is stamped from it, so a build from anything but a tag would publish binaries
  that misreport what they are. The release workflow installs all three backends
  and refuses to proceed if any is missing — the suite skips rather than fails
  when a backend is absent, so an uninstalled one would otherwise let a tag pass
  a gate it never actually ran. Still no Homebrew tap, which would need a tap
  repository of its own; installation is `go install` or the release archives.
- **The MCP door is stdio only**, which is deliberate (behavior §15), so there
  is no remote or multi-client story and none is planned.
- **An undiagnosed intermittent failure on the meja leg, macOS only.** Every
  meja case in the root package fails at once with meja's own `command requires
  an attached client`, in bursts of a run or two, then not again for dozens.

  What is now measured rather than guessed:

  - A transient client normally becomes usable in **25–50ms**. The failures sit
    at the 5s budget — a 150× outlier, so something blocks rather than slows.
  - Raising the budget to 30s **halved the failures but did not remove them**,
    so it is not merely a budget that is too tight.
  - Falsified, each by measurement after being proposed as the answer: socket
    path length, `-race`, test parallelism, and an unanswered `DECRQM ?69`
    query from the client (answering it changes nothing — 15 samples each way,
    indistinguishable).

  - meja has a SECOND client failure, `target client disconnected`, which is
    not the same fault and must not be treated as one: the first is a refusal
    (the command provably did not run, so retrying is safe), the second is a
    loss mid-flight (whether it ran is unknown, so retrying could deliver it
    twice). It is reported distinctly and deliberately not retried.
  - They are NOT two edges of one client-lifetime window, which was the
    obvious guess. Measured across five phases — no client, settled client,
    client killed, killed and the server caught up, PTY closed — only the
    first produced a message at all. A session whose client has died or whose
    PTY is closed stays drivable, so the disconnect could not be provoked by
    teardown timing in any of them.
  - `§5.6 following` fails intermittently on its own, reproduced locally twice
    in twenty-four full-package runs and in two DIFFERENT ways: once as the
    disconnect above while submitting, once as a stream that never carried
    output it should have. It attaches its own client, which the injection
    path then borrows rather than making one.

  The evidence that would settle it existed at the moment of failure and was
  being thrown away: the client's output went to `io.Discard` and nothing
  recorded whether the process was alive. Both are captured now and carried
  into the error, so the next burst diagnoses itself. Note that the server's
  side cannot be captured at all — meja has no verb that lists clients, and
  `#{session_attached}` comes back unsubstituted.

---

## Phase 1 — the mechanical layer

`backend/` — types and the interface backends implement. No backend yet.

- Error vocabulary and codes with their exit mapping (behavior §12).
- Row types: session, pane, capture metadata, capabilities, liveness tri-state,
  probe tri-state (behavior §3, §5.5, §13; shapes per api §5).
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

**The first task of this phase was to finalize api §6.** It had been left
provisional on purpose: its shape depended on the `Backend` interface, which did
not exist until Phase 1. Once it did, the sketch became a contract.

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
- goreleaser and `go install`. (No Homebrew tap: one would need a tap
  repository, and claiming an install route that does not exist is worse than
  offering fewer.)
- CHANGELOG and the stability commitments of api §7.

**Done when**: a clean checkout on a fresh machine can install, run `doctor`, and
start a session by following only the README.

---

## Standing rules for every phase

- **Test-first.** Failing test, then the code that passes it.
- **The specs are amendable.** If implementation disproves a rule, change the
  spec in the same commit as the code that proved it, and say what moved.
- **Never touch a live daemon or default socket** — a private `ZMX_DIR` and a
  private tmux socket path, for probes as well as tests, cleaned up afterwards.
- **Race-shaped fixes need reproducing tests** (behavior §16). A test that passes
  once against the fix proves nothing.
- **Commit at checkpoints**, small enough to review and revert alone.
