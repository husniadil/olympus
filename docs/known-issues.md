# Known issues and limits

What is still outstanding or deliberately out of scope. Each entry is the
current state, not a plan.

## Attach by a human is tested by hand

`attach` is covered end to end against a PTY the test owns. The tests check
that the terminal is handed back as it was found, that both resize routes reach
the pane, and that the slot is arbitrated across real processes, including a
holder killed with SIGKILL.

What only a person exercises is a human attaching: keystroke feel, detach keys,
and how a full-screen program looks to someone watching it.

## The conformance suite does not run on macOS in CI

CI runs on Linux only. The darwin build is checked by the cross-compile
`go vet` inside `make test-full`, but the suite against real backends on macOS
runs only on a developer machine. The meja failure below has only been seen
there.

## MCP is stdio only

The MCP door speaks stdio and nothing else (behavior §15). There is no remote or
multi-client transport, and none is planned.

## A herdr session client can boot a server that just died

A path-addressed attach on herdr runs plain `herdr`, which starts a server when
none answers on its socket. Olympus asks the server once more immediately
before the client is built and refuses as not-found when it is gone, but a
server that dies between that check and the client starting is booted again,
under the configuration the client was given. herdr's client has no option to
attach only, so the window cannot be closed from Olympus.

## The herdr status token is not scoped to Olympus

herdr keeps one token map per pane, and whoever reports a token only orders the
reports. Another tool that writes a token named `status` is read back by
`status` as Olympus's own, and `status --set` or its clear overwrites that
tool's value. Scoping the token is a change to a reserved name (behavior
§17.1), so it waits for a contract decision.

## A failed MCP call still carries a structured result

A tool that fails returns `isError` with the error in its text content, and its
`structuredContent` names the resolved backend. The SDK also fills the rest of
the structured result from the tool's zero value, so a failed `run_command`
carries `exit_code: 0` beside the error. A client must read `isError` before
`structuredContent`. Dropping the zero data means publishing a separate output
schema for failures, which waits on the failure-envelope decision.

## An undiagnosed meja failure on macOS

Every meja case in the root package intermittently fails at once with meja's
own `command requires an attached client`. It comes in bursts of a run or two,
then not again for dozens. It has only been seen on macOS.

From meja 0.0.26, ordinary input no longer takes the client path, so the burst
cannot occur there. That is a bypass on one leg, not a fix. The path still runs
on the 0.0.25 floor, still runs for copy mode, and `Follow` attaches a client on
every version.

### What is measured

- A transient client normally becomes usable in 25 to 50ms. The failures sit at
  the 5s budget, a 150x outlier, so something blocks rather than slows.
- Raising the budget to 30s halved the failures but did not remove them. The
  budget is not merely too tight.

### Falsified hypotheses

Each was proposed as the answer and ruled out by measurement:

- socket path length
- `-race`
- test parallelism
- an unanswered `DECRQM ?69` query from the client (answering it changes
  nothing, 15 samples each way, indistinguishable)

### A second, distinct client failure

meja also reports `target client disconnected`. It is not the same fault.

- `requires an attached client` is a refusal: the command did not run, so a
  retry is safe.
- `target client disconnected` is a loss mid-flight: whether the command ran is
  unknown, so a retry could deliver it twice. It is reported distinctly and not
  retried.

The two are not the edges of one client-lifetime window. Across five phases (no
client, settled client, client killed, killed and the server caught up, PTY
closed), only the first produced a message. A session whose client has died or
whose PTY is closed stays drivable, so teardown timing did not provoke the
disconnect.

The `§5.6 following` test also fails intermittently on its own: twice in
twenty-four full-package runs, once as the disconnect while submitting and once
as a stream that never carried output it should have. It attaches its own
client, which the injection path then borrows.

Overlapping injections into ONE session drop each other's commands on the
0.0.25 floor. Each attaches a transient client, and 0.0.25 routes a session's
commands through its one current client (`commandClientValue` in meja's
`internal/server/command.go`), so one injection's teardown drops another's
command in flight with the disconnect. The concurrent paste case failed this way
on Linux in CI: it ran six pastes into the same session at once, and failed 5 of
40 runs locally. With each session's pastes in turn it passed 100 of 100 with
two sessions concurrent, and 20 of 20 with the six it now runs.

That changed the test, not the backend, and the race is still reachable. The
per-session write lock (§11.1) serializes Olympus's own writers, but
`--no-lock` (`WithoutLock`) bypasses it, and a follow or attach client, or a
human's, is a current client the lock does not cover. The fix belongs in
`withClient`, and it is open. It does not explain the `§5.6 following`
disconnect either, which injects one command at a time.

### What is captured now

The client's output and whether its process was alive are captured at the
moment of failure and carried into the error (`backend/meja/clientdiag.go`), so
the next burst reports its own evidence. The server's side cannot be captured:
meja has no verb that lists clients, and `#{session_attached}` comes back
unsubstituted.
