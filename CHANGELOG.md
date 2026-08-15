# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project uses
[semantic versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- The mechanical layer: `backend` types, the error vocabulary with its exit
  mapping, target resolution, and the `Backend` interface.
- `backend/backendtest`, the conformance suite, exported so a third-party
  backend can prove itself against the same one the shipped backends run.
- The tmux and zmx backends, both green on the full suite.
- Shared engines: the per-session write lock, verified delivery, idempotent
  ensure, the sentinel run protocol, graceful kill, and exit-marker inspection.
- The ergonomic Go surface: `Open`, `Session`, options, typed errors.
- The CLI, with the structured envelope and the documented exit codes.
- The MCP door, dual-era on the official Go SDK, targeting revision
  `2026-07-28`.
- Attach: PTY ownership, raw mode, two-layer terminal restore, the resize
  protocol, and the attach guard.
- Multi-target capture, applying the door's alt-screen skip rule.
- `panes`, `new` and `capabilities` as verbs and tools in their own right.
- Throwaway runs: `run` with no target creates a session for the run and kills
  it afterwards.
- `paste --enter`, `send --no-enter`, and tunables on every operation that has a
  budget or a window.
- Views: the full server-global setup — hyperlink passthrough, the read-only
  posture, wheel bindings — with the base probed before anything is created.
