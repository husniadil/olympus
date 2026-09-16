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

This is the most useful thing you can include: it says which backend answered
and why, its version, and what it can do.

**Reproduction**

The commands you ran. Reproduce it off your live sessions:

- tmux, meja or herdr: `--socket-path ./some/dir/sock`
- zmx: a private `ZMX_DIR`

Use a socket path rather than a name, because killing a server does not unlink
its socket and a named one is left behind.

**Does the spec cover it?**

If a rule in `docs/terminal-behavior.md` says what should happen, quote the
section. If nothing covers it, say so: that is a gap worth knowing about.
