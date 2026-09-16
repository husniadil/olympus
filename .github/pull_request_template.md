**What this changes, and why**

**Does it change anything the spec covers?**

- [ ] No spec change needed
- [ ] `docs/terminal-behavior.md` amended in this commit, and the change is
      explained above
- [ ] `docs/api.md` amended in this commit

**Checklist**

- [ ] `make test-full` passes, not just `make test`, which skips every case
      that drives a real terminal
- [ ] Tests were written before the code they cover
- [ ] Nothing touches a live session: a private socket path for tmux and meja,
      a private `ZMX_DIR` for zmx, a private socket path with its configuration
      and state directories for herdr
- [ ] If this fixes a race, there is a test that FAILS with the fix reverted
- [ ] No semver-bound name changed, or the change is called out above
