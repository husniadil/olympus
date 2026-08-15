**What this changes, and why**

**Does it change anything the spec covers?**

- [ ] No spec change needed
- [ ] `docs/terminal-behavior.md` amended in this commit, and the change is
      explained above
- [ ] `docs/api.md` amended in this commit

**Checklist**

- [ ] `make test` passes
- [ ] Tests were written before the code they cover
- [ ] Nothing touches a live tmux server or zmx daemon: private socket, private
      `ZMX_DIR`
- [ ] If this fixes a race, there is a test that FAILS with the fix reverted
- [ ] No semver-bound name changed, or the change is called out above
