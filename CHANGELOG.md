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
