# Adding a backend

Olympus drives a multiplexer it does not embed. Four ship: zmx, tmux, meja and
herdr. The interface they implement is public, and so is the conformance suite
that judges them, so a fifth is a normal contribution, not a fork.

This is the route, written from what the third and fourth backends cost rather
than from what the interface looks like.

---

## 1. Spike before you write code

Measure the multiplexer by hand before implementing anything. Write down what
surprised you: that list is your capability declaration.

### Why

The interface fits almost anything. Then one structural difference makes half
of it behave unlike the other backends, and nothing in `backend.Backend` hints
at it.

meja is the example. Through 0.0.25 every input command it accepts needs an
attached client, while observation works headlessly. The integration is built
around a transient headless client per injection, chosen after measuring the
cost: 68 ms cold, 23 ms warm.

### Probe at the moment of use

Attempt an operation and fall back only when refused, rather than encoding what
the backend does today.

#### Why

meja 0.0.26 dropped the client requirement for ordinary input. The integration
survived unchanged because it attempts the injection and attaches a client only
on refusal. A probed capability costs one failed call when it moves; a
hardcoded one costs a rewrite.

### What to measure

- **Start a session, send text, capture it back.** By hand, from a shell.
- **Send a control byte to `cat -v`** and read what arrives. A backend that
  accepts a key and drops it is worse than one that rejects it: the caller sees
  success and waits for an effect that never comes.
- **Resize a client and ask the session `tput cols; tput lines`.** Expect a
  different answer: tmux and meja both reserve a row for a status line.
- **Kill the server and list sessions.** No server running must be an empty
  list, never an error (§3.3).
- **List sessions on a server nobody has used yet.** It may not be empty.
  herdr's server opens a workspace of its own at start, so "everything the
  multiplexer knows" and "everything Olympus created" differ, and the second
  needs a marker to select on.
- **Try to spawn a command.** herdr cannot choose a pane's process. If yours
  cannot, declare it through a capability and refuse the field (§2.3.1). Typing
  the argv into a shell instead echoes the command line and reinterprets every
  metacharacter.
- **Run something that repaints in place:** an editor, not a `printf` of escape
  sequences. A synthetic alt-screen test never presses a control key and never
  repaints, which are the paths that break.

---

## 2. Isolation is a hard requirement

**Tests must never touch the operator's live sessions.** This is non-negotiable
5 in `CLAUDE.md`, and reviewers check it first.

Session-name prefixes are not enough. A test needs a server nobody else
addresses, and how you get one is backend-specific:

| Backend | Isolation |
|---|---|
| tmux | a socket at a private path inside a directory the test owns |
| zmx | `ZMX_DIR` pointed at a private temp dir; it has no socket flag |
| meja | `-S <path>`, never `-L <profile>` |
| herdr | a private socket path, and the configuration and state directories moved with it |

If your backend offers no way to isolate a server, say so in the pull request.
A backend that cannot be tested without touching the operator's sessions cannot
ship.

### A named tmux socket is not enough

Killing a server does not unlink its socket, so a named one accumulates in the
directory shared with the operator's own servers. Put it inside a directory the
test owns, so it disappears with the test.

### State may not follow the socket

Check where the backend keeps state, not only where its socket is. One flag
rarely moves everything.

#### Why

- **meja** keeps session recovery files beside the socket. A named profile
  would leave persisted sessions in the operator's store, restored on their
  next start.
- **herdr** keeps the unnamed session's layout in its configuration directory,
  chosen from the environment with no reference to the socket. A test server on
  a private socket would overwrite `~/.config/herdr/session.json`, destroying
  the operator's saved workspaces while every check on names and sockets passes.

### Find leaks by looking, not by reasoning

Start a server pointed at a private everything, run one session, then `find` the
directories it was supposed to be isolated from. Whatever appears there is what
your isolation does not cover.

### Derive state from the socket

Derive the state location from the socket path rather than exposing a second
option for it. herdr's `WithSocketPath` does this.

#### Why

A pairing of two options that can be half-applied will be, and the caller who
half-applies it has no way of knowing.

---

## 3. Write the tests first, against the shipped suite

`backend/backendtest` is exported for this. It is the definition of correct, and
it was written before the backends it tests.

```go
func TestConformance(t *testing.T) {
	requireYourBackend(t)
	backendtest.Run(t, backendtest.Config{
		New: newIsolated,
		Expect: backendtest.Expectations{
			InterruptShellBacked: backendtest.InterruptStops,
			InterruptExecSpawned: backendtest.InterruptIneffective,
		},
	})
}
```

- **`New` must build a backend on a private server**, per section 2. The suite
  runs cases in parallel and each builds its own, so isolation also makes the
  concurrency safe.
- **`Expectations` are declarations you are held to, not switches that skip.**
  Declaring `InterruptIneffective` asserts the session survives the interrupt.
  A backend that gains the ability fails the suite and updates its declaration,
  where a skip would have let the change pass unnoticed.

### Skip when the binary does not run

Skip loudly when your binary is absent, and check that it runs rather than that
it exists:

```go
if err := exec.Command("yourmux", "version").Run(); err != nil {
	t.Skip("yourmux is not installed or not runnable")
}
```

#### Why

`exec.LookPath` succeeds against a version-manager shim left by an uninstalled
tool, which then fails every call. The result is a wall of broken cases instead
of one honest skip.

---

## 4. Capabilities are measured, never assumed

`Capabilities()` is a static declaration with no context and no subprocess
(§13). Every field must be something you measured in section 1.

If your backend cannot do something, return `UNSUPPORTED` and declare it false.
The declaration lets a caller avoid the error; the error gives a caller who
ignored the declaration a truthful answer.

### Why

Callers branch on capabilities instead of catching `UNSUPPORTED`. A wrong answer
routes real logic down the wrong path.

### Name the mechanism, not the symptom

A capability was once shipped saying a backend's capture showed stale frames.
Repaints were captured correctly; what that backend dropped was the control key
that would have caused one. Capabilities are semver-bound, so a misdiagnosis
there is permanent.

### Check whose audience a rule names

A rule inherited from another backend can carry an assumption yours does not
share. Before adopting a rationale, check that the audience it names applies.

---

## 5. Map errors onto the shared vocabulary

Codes are semver-bound (§12): `USAGE`, `SESSION_NOT_FOUND`,
`BACKEND_UNAVAILABLE`, `TIMEOUT`, `CONFLICT`, `UNSUPPORTED`, `UNEXPECTED`.

### USAGE against UNEXPECTED

If one corrected argument fixes it, it is `USAGE`. `UNEXPECTED` tells a program
that retrying will not help and nothing it controls is at fault.

#### Why

This matters most where backends disagree. A session name one backend refuses
is one the others accept, so the caller really is being told about their input.

### An absent server is an absent session

For a target-addressed operation, a missing server becomes
`SESSION_NOT_FOUND`.

#### Why

A caller holding a session name can match nothing against a socket path. When
the server is gone, every session on it is gone too.

### Do not collapse things that share a code

meja's "requires an attached client" shares `BACKEND_UNAVAILABLE` with an absent
server, but the session is there. Treating it as absence would report a live
session as missing.

---

## 6. Wire it into the three doors

Defaults are decided once, in the ergonomic layer. Adding a backend touches:

| File | What to add |
|---|---|
| `resolve.go` | the preference order, which addressing options apply, and an install hint (`installHint`) |
| `olympus.go` | the arm of `open` that builds the handle and records its lock scope, and the arm of `resolveTarget` that spells a pane id |
| `doctor.go` | a version floor (`floors`) and the `buildBackend` arm the diagnostic uses to probe it and report where its sessions live |
| `warnings.go` | any degraded-operation disclosure |

Nothing in the CLI or MCP command definitions changes. A flag for your backend
alone is a sign the option belongs in the ergonomic layer's addressing table.

### Why

A door that invents its own default has introduced a second contract.

### Compare surfaces mechanically

Diff flag sets against tool names with a script rather than by eye. A
twenty-line script once found seven missing MCP parameters and a whole verb in a
surface a careful read had called complete.

---

## 7. Neutrality

No exported identifier, file, or package name refers to a specific consumer,
product, or vendor. The scope is names, not comments: explaining that a submit
is paced because a particular REPL treats text-plus-terminator as a paste is
accurate prose.

---

## 8. What a reviewable pull request contains

- [ ] The conformance suite green, and the spike notes behind each
      `Capabilities()` field
- [ ] Isolation proven, with the mechanism named, including where the backend
      keeps state
- [ ] A version floor, stated as the version every measurement was taken
      against; support below it is best-effort
- [ ] Error mapping, with the `USAGE`/`UNEXPECTED` split defensible case by case
- [ ] `make test-full` green. `make test` skips every case that drives a
      terminal, so it says nothing about a backend
- [ ] Spec amendments in the same commit as the code that proved them needed

If implementing your backend shows a rule in `docs/terminal-behavior.md` is
wrong, incomplete or unimplementable, change it and say what moved. A spec that
has drifted from the code is worse than none, because it is still believed.

---

## Traps that cost a day

Each was found by a failure, not by reading.

- **A verified send proves the text landed, not that it ran.** Capturing
  straight after one races the shell's expansion. Wait for the substituted
  output.
- **Trailing whitespace is not portable.** tmux preserves a prompt's trailing
  space; zmx normalizes it away. Write `^>>>\s*$`, never `^>>> $`.
- **A prompt is not guaranteed to start a line.** A program that paints by
  cursor positioning can leave its prompt appended to the shell's echo, so an
  anchored pattern fails against text on screen (§7.3.1).
- **Format output may be sanitized, depending on version.** tmux 3.5a escapes a
  `0x1f` field separator into the four characters `\037` and turns a tab into
  `_`; 3.7b passes both through. Parse defensively.
- **Cancelling a context kills the child, not its grandchildren.** They inherit
  the output pipe, and the read blocks on the pipe. Set `WaitDelay` on every
  command, or a cancelled call can hang past its own deadline.
- **Check before declaring "I cannot test that".** Run `command -v` first.
  Driving a real editor end to end found two defects a synthetic test could not.
- **A unit test that reaches the create path may start a real server.** Assert
  against the validator, not through the operation, and check `ps` after a test
  run.
- **Ask the backend a real question to decide whether it is up.** A socket file
  survives its server, and a server mid-boot accepts a connection before it can
  serve. The cheapest request the backend parses is the honest probe, and it
  makes an idempotent "ensure a server" safe on every create.
