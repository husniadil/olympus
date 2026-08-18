# Adding a backend

Olympus drives a multiplexer it does not embed. Three ship — zmx, tmux, meja —
and the interface they implement is public, along with the conformance suite
that judges them. A fourth is a normal contribution, not a fork.

This is the route, written from what the third one actually cost rather than
from what the interface looks like.

---

## 1. Before you write any code: spike it

The interface will fit almost anything. That is the problem — it fits, and then
one structural difference makes half of it behave unlike the others.

meja is the example. Through 0.0.25 every INPUT command it accepts requires an
attached client and refuses outright without one; observation works headlessly,
driving does not. Nothing in `backend.Backend` hints at that, and no amount of
reading its help would have replaced measuring it. The integration is built
around a transient headless client per injection, which is a design, not an
adapter — and it was chosen only after the cost was measured (68 ms cold, 23 ms
warm).

There is a second lesson on top of the first. meja 0.0.26 dropped that rule for
ordinary input, and the integration survived unchanged because it ATTEMPTS the
operation and attaches a client only when refused — a shape that asks the
backend what is true instead of encoding what was true when it was written. A
capability probed at the moment of use costs one failed call; the same
capability hardcoded costs a rewrite when it moves.

So before implementing:

- **Start a session, send text, capture it back.** By hand, from a shell.
- **Send a control byte to `cat -v`** and read what arrives. This is how you
  learn whether control keys actually reach the pane. A backend that accepts a
  key and drops it is worse than one that rejects it: the caller sees success and
  waits for an effect that will never come.
- **Resize a client and ask the session `tput cols; tput lines`.** Expect the
  answer to differ from what you asked — tmux reserves a row for its status line,
  meja does too. Learn the offset now, not from a failing test later.
- **Kill the server and list sessions.** No server running must be an empty list,
  never an error (§3.3).
- **Run something that repaints in place** — an editor, not a `printf` of escape
  sequences. A synthetic alt-screen test proves nothing about editors: it never
  presses a control key and never repaints, which are exactly the paths that
  break.

Write down what surprised you. That list is your capability declaration.

---

## 2. Isolation is a hard requirement, and it is the easiest thing to get wrong

**Tests must never touch the operator's live sessions.** Not "should" — this is
non-negotiable #5, and the reviewers will check it first.

Session-name prefixes are **not** sufficient. What is required is a server nobody
else addresses, and how you get one is backend-specific:

| Backend | Isolation |
|---|---|
| tmux | a socket at a private **path** inside a directory the test owns |
| zmx | `ZMX_DIR` pointed at a private temp dir — it has no socket flag at all |
| meja | `-S <path>`, never `-L <profile>` |

Two traps that have already been paid for:

- **A named socket is not enough for tmux.** Killing a server does not unlink its
  socket, so a named one accumulates in the directory shared with the operator's
  own servers. Put it inside a directory the test owns, so it disappears with the
  test.
- **Where your backend keeps *state* may not follow where you pointed its
  socket.** meja keeps session recovery files beside the socket, so a named
  profile would leave persisted sessions in the operator's own store, to come
  back on their next restore. Check this explicitly; do not assume one flag moves
  everything.

If your backend offers no way to isolate a server, say so in the pull request
rather than working around it. A backend that cannot be tested without touching
the operator's sessions cannot ship, and that is a finding about the backend, not
a problem to be clever about.

---

## 3. Write the tests first, against the shipped suite

`backend/backendtest` is exported for exactly this. It is not a convenience — it
is the definition of correct, and it was written before the backends it tests.

```go
func TestConformance(t *testing.T) {
	requireYourBackend(t)
	backendtest.Run(t, backendtest.Config{
		New: newIsolated,
		Expectations: backendtest.Expectations{
			InterruptShellBacked: backendtest.InterruptStops,
			InterruptExecSpawned: backendtest.InterruptIneffective,
		},
	})
}
```

Two things about that config:

- **`New` must build a backend on a private server**, per section 2. The suite
  runs its cases in parallel and each builds its own, so isolation is what makes
  concurrency safe as well as what keeps the operator's sessions untouched.
- **`Expectations` are declarations you are held to, not switches that skip.**
  Declaring `InterruptIneffective` asserts the session *survives* the interrupt.
  A backend that quietly gains the ability fails the suite and gets to update its
  declaration — which is the point. A skip would have let the improvement pass
  unnoticed.

Skip loudly when your binary is absent, and **check that it runs rather than that
it exists**:

```go
if err := exec.Command("yourmux", "version").Run(); err != nil {
	t.Skip("yourmux is not installed or not runnable")
}
```

A `exec.LookPath` succeeds against a version-manager shim left behind by an
uninstalled tool, which then fails every call. That produces a wall of broken
cases instead of one honest skip. This has happened here.

---

## 4. Capabilities are measured, never assumed

`Capabilities()` is a static declaration with no context and no subprocess
(§13). Callers **branch on it** instead of catching `UNSUPPORTED`, so a wrong
answer is not a cosmetic error — it routes real logic down the wrong path.

Every field must be something you measured in section 1. Two rules learned the
expensive way:

- **Name the mechanism, not the symptom.** A capability was once added saying a
  backend's capture showed stale frames. Wrong: repaints ARE captured correctly;
  what that backend drops is the control KEY that would have caused one. The
  spike that settled it took three commands. Getting this wrong bakes a
  misdiagnosis into a semver-bound field.
- **A rule inherited from another backend can carry an assumption yours does not
  share.** Check whose audience a rationale names before adopting it.

If your backend cannot do something at all, return `UNSUPPORTED` *and* declare it
false. The declaration is how a caller avoids the error; the error is how a
caller who ignored the declaration still gets a truthful answer.

---

## 5. Map errors onto the shared vocabulary

Codes are semver-bound (§12): `USAGE`, `SESSION_NOT_FOUND`, `BACKEND_UNAVAILABLE`,
`TIMEOUT`, `CONFLICT`, `UNSUPPORTED`, `UNEXPECTED`.

The distinction that matters most is `USAGE` against `UNEXPECTED`. `UNEXPECTED`
tells a program that retrying will not help and nothing it controls is at fault.
If one corrected argument fixes it, it is `USAGE` — and this matters most exactly
where backends disagree, because a session name one backend refuses is one the
others accept. There the caller really is being told about their input.

Two more:

- **An absent server must become an absent SESSION** for a target-addressed
  operation. A caller holding a session name can match nothing against a socket
  path. The collapse is sound: when the server is gone, every session on it is
  gone with it.
- **Do not collapse things that merely share a code.** meja's "requires an
  attached client" shares `BACKEND_UNAVAILABLE` with an absent server but means
  something else — the session is there and the client was not — so treating it as
  absence would report a live session as missing.

---

## 6. Wire it into the three doors

Defaults are decided **once**, in the ergonomic layer. A door that invents its own
default has introduced a second contract. Adding a backend should touch:

- `resolve.go` — the preference order, and which addressing options apply
- `doctor.go` — a version floor, an install hint, and where its sessions live
- `warnings.go` — any degraded-operation disclosure

and **nothing** in the CLI or MCP command definitions. If you find yourself
adding a flag for your backend alone, that is a sign the option belongs in the
ergonomic layer's addressing table instead.

Check your work mechanically rather than by eye. A twenty-line script that diffs
flag sets against tool names once found seven missing MCP parameters and a whole
verb that a careful read had declared complete.

---

## 7. Neutrality

No exported identifier, file, or package name refers to a specific consumer,
product, or vendor. The scope is **names**, not comments: explaining that a
submit is paced because a particular REPL treats text-plus-terminator as a paste
is accurate prose, not a violation.

---

## 8. What a reviewable pull request contains

- [ ] The conformance suite green, and the spike notes that justify each
      `Capabilities()` field
- [ ] Isolation proven, with the mechanism named — including where the backend
      keeps state, not only its socket
- [ ] A version floor, with a sentence on what was measured against it. "The
      version every measurement behind this backend was taken against" is the
      honest form; support below it is best-effort because nothing was checked
      there.
- [ ] Error mapping, with the `USAGE`/`UNEXPECTED` split defensible case by case
- [ ] `make test-full` green — the fast `make test` deliberately skips every case
      that drives a terminal, so it cannot tell you anything about a backend
- [ ] Spec amendments in the **same commit** as the code that proved them needed

That last one is the house rule, and it runs both ways: if implementing your
backend shows a rule in `docs/terminal-behavior.md` is wrong, incomplete or
unimplementable, change it and say what moved. The specs lead, but they are not
immune to evidence — and a spec that has drifted from the code is worse than none,
because it is still believed.

---

## Things that will cost you a day if you skip them

Each of these was found by a failure here, not by reading.

- **A verified send proves the text LANDED, not that it RAN.** Capturing straight
  after one races the shell's expansion. Wait for the substituted output.
- **Trailing whitespace is not portable.** tmux preserves a prompt's trailing
  space; zmx normalizes it away. Write `^>>>\s*$`, never `^>>> $`.
- **A prompt is not guaranteed to start a line.** A program that paints by cursor
  positioning can leave its prompt appended to the shell's echo, so an anchored
  pattern fails against text plainly on screen (§7.3.1).
- **Format output may be sanitized, and version-dependently.** tmux 3.5a escapes
  a `0x1f` field separator into the four characters `\037` and turns a tab into
  `_`; 3.7b passes both through. Parse defensively or every listing comes back
  empty on a supported version.
- **Cancelling a context kills the child, not its grandchildren.** They inherit
  the output pipe, and the read blocks on the pipe rather than the process. Set
  `WaitDelay` on every command, or a cancelled call can hang forever past its own
  deadline — measured at 30 s against 3 s.
- **"I cannot test that" deserves one check first.** `command -v` before
  declaring a limit. Driving a real editor end to end found two defects that a
  synthetic test could not have.
