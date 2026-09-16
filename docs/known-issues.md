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

### What is captured now

The client's output and whether its process was alive are captured at the
moment of failure and carried into the error (`backend/meja/clientdiag.go`), so
the next burst reports its own evidence. The server's side cannot be captured:
meja has no verb that lists clients, and `#{session_attached}` comes back
unsubstituted.
