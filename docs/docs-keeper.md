# The docs keeper

`docs/api.md` and `skills/olympus/SKILL.md` are read by people and by agents
that never open the source, so a sentence in them that has stopped being true is
taken as fact. The keeper is an agent that audits them against the source and
fixes what has provably drifted. This is its procedure. The agent definition
(`.claude/agents/docs-keeper.md`) points here and holds one rule of its own: it
spawns no subagents.

It runs before a release tag, on request, and every twentieth commit that
touches Go source outside tests. That cadence decides when a run happens. Step 2
below decides how wide one is.

## What it is not

The keeper does not own the docs. Whoever changes code still changes the docs in
the same commit (`CLAUDE.md`, "Docs are part of the change"), and
`internal/cli/docs_test.go` catches what can be caught mechanically: a
documented command that no longer parses, and an api §1 table that disagrees
with the command tree or the served MCP tools. The payload tests
(`payload_test.go`, `internal/cli/envelope_test.go`) hold the JSON shapes. The
keeper reads for what no test can: a sentence that parses and links fine and
says something the code no longer does.

## What it may edit

| Document | Keeper |
|---|---|
| `docs/api.md` | Audits and edits |
| `skills/olympus/SKILL.md` | Audits and edits |
| `docs/known-issues.md` | Audits and edits |
| `docs/terminal-behavior.md` | Reports only |
| `README.md`, `CONTRIBUTING.md`, `CLAUDE.md`, `docs/adding-a-backend.md`, this file | Out of scope |

`docs/terminal-behavior.md` is normative: code follows it, and a disagreement
between the two is a decision about which one is wrong. The keeper reports such
a disagreement and changes neither. The out-of-scope documents carry reasons and
rules, which people decide.

## Why it converges

An agent asked whether documentation is good always finds something, so it is
never asked that. It may report a finding only of these four kinds, each with
evidence, and each with a limit on how much it may change:

| Kind | Meaning | Evidence | Edit allowed |
|---|---|---|---|
| FALSE | The code contradicts a sentence | file:line of the contradicting code | Replace that sentence in place; one clause more if a condition is needed |
| MISSING | A verb, flag, tool, parameter, field, error code or warning a caller meets has no sentence | file:line where it is defined | One paragraph or one table row, 12 lines at most, in the section that owns it |
| BROKEN | A command that does not parse, a name or path that does not exist, a link or section reference to nothing | The parse error or the missing name | Change the token, one line |
| STALE ISSUE | `docs/known-issues.md` lists as outstanding something the code now does | file:line and the commit that did it | Remove or narrow that entry |

Not findings: wording, order, length, tone, a true sentence that could be put
better, a design choice it disagrees with. No finding, no edit. A new section is
never a keeper edit; it is reported for a person.

Two report-only kinds are never edits:

- CODE: a document is right about something the code now gets wrong. The line,
  the code's file:line and the commit that did it.
- SPEC: `docs/terminal-behavior.md` and the code disagree, and the history does
  not settle which one is the intent.

Three rules make a second run a no-op. The last audited commit is written to
`docs/.audited` by the keeper's own commit. A run whose findings are empty
changes nothing but that one line. And a run whose range holds only commits that
change nothing but `docs/.audited` makes no commit at all.

## Source of truth

When a document and the code disagree, trust in this order: the code, the
tests, commit messages and the behavior spec, the document. Git history is read
only to settle such a disagreement. If it shows the document was the intent and
the code is the mistake, the keeper reports CODE and leaves the document alone;
documenting a bug as behavior is the worst edit it can make.

## A run

1. Read `docs/.audited`. The range is that commit to HEAD. If the file is
   missing, the scope is every document it may edit. If every commit in the
   range changes only `docs/.audited`, stop: report no findings and commit
   nothing.
2. Scope: every editable document that quotes an identifier, flag, tool, field,
   error code or command word that the range's removed lines held, together with
   `docs/api.md` and `skills/olympus/SKILL.md` whenever the range touched
   `internal/cli/`, `internal/mcp/` or a root package file. On request, and at
   the every-twentieth-commit cadence, the scope is every editable document.
3. For each document in scope, for each sentence that states a fact about
   Olympus: find the fact in source. Record a finding only of the kinds above,
   with its evidence. Read `docs/terminal-behavior.md` only for the sections a
   finding leads to, and report what disagrees there as SPEC.
4. Apply the edits, each within its limit.
5. Run `make test`.
6. Write HEAD to `docs/.audited` only when every document in scope was read
   whole against the source. A run that stops short commits its edits with
   `docs/.audited` unchanged and names, in the report and the commit body, the
   documents it did not read. An advanced `docs/.audited` is a claim that the
   range was audited.
7. Commit with one line per finding in the body: kind, document, the evidence.
