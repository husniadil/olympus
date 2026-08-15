---
name: Bug report
about: Something behaved differently from what the spec says
labels: bug
---

**What happened, and what you expected instead**

**`olympus doctor` output**

<details>

```
paste it here
```

</details>

This is the single most useful thing you can include: it says which backend
answered and why, its version, and what it can do.

**Reproduction**

The commands you ran. If you can reproduce it against a private socket
(`--socket some-name`) or a private `ZMX_DIR`, that keeps your live sessions out
of it.

**Does the spec cover it?**

If a rule in `docs/terminal-behavior.md` says what should happen, quoting the
section helps a lot. If nothing covers it, say so — that is a gap worth knowing
about.
