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

The commands you ran. Reproducing it off your live sessions keeps them out of
it, and how depends on the backend: `--socket-path ./some/dir/sock` for tmux or
meja, a private `ZMX_DIR` for zmx. A socket PATH rather than a name, because
killing a server does not unlink its socket and a named one is left behind.

**Does the spec cover it?**

If a rule in `docs/terminal-behavior.md` says what should happen, quoting the
section helps a lot. If nothing covers it, say so — that is a gap worth knowing
about.
