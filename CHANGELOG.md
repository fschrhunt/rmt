# Changelog

Written by each release, newest first; the same notes are on
[GitHub Releases](https://github.com/fschrhunt/rmt/releases).

## Unreleased

- Give ordinary commands a remote terminal from the usual prompt. Keyboard input, resize,
  and screen output work without a separate mode; OpenCode and Codex keep their native local UIs.
  Update both client and host for shell protocol 11.

- Don't register a second background service when an rmt host is already running under a different
  service name.
- Add read-only `rmt status` for local hosting and known hosts. Clarify connection and native
  launch errors, and keep rmt's prompts and diagnostics out of piped stdout.
- Highlight shell input locally, make bare `clear` clear the screen, and complete escaped paths
  and symlinks to directories.

## v0.1.1 - 2026-11-22

- Upgrade both the client and host to use shell protocol 10. Replacing an installed binary
  does not restart a running host service.
- Set up local background hosting on the first connection attempt. Later connections leave
  existing services alone. `rmt host disable` opts out persistently; `rmt host enable` turns
  hosting back on without changing pairings.
- Resolve aliases within command submission, avoid dropping output on live connections, and
  cap how long Tab completion can hold up local input. Closing a tab normally releases its shell
  and native attachment immediately.
- Update `golang.org/x/sys` to v0.50.0 and the `actions/setup-go` workflow action to v7.1.0.

## v0.1.0 - 2026-11-21

- Add a release dry run and verify published installs on macOS and Linux with `rmt --version`.
- Add `[prompt] style = "pure" | "inherit"` so clients can use rmt's stock appearance or
  inherit Pure's colors and symbols from the local user's zsh settings. rmt keeps control of the
  layout and shows remote machine and repository information.
- Render rmt's stock local prompt with the host user and short name, abbreviated path, git branch
  and state, slow-command time, and an error-colored symbol. Add an authenticated host git-status
  endpoint and bump the shell protocol to 9.
- Fix remote Tab completion with an empty prefix, preserve shell paths that end in a newline,
  and restore terminal settings on shutdown.
- Add paired loopback client–host validation and opt-in shell startup and command latency
  benchmarks; document native terminal checks and limits of the protocol fixtures.
- Attest release archives and their checksum manifest with GitHub build provenance, verify the
  manifest before proposing Homebrew updates, and publish only the four checksummed archives.
- Refuse HTTP downgrades when installing from an HTTPS release URL.
- Propose Go dependency and GitHub Actions updates in one weekly pull request.
- Require Go 1.27.3 or newer for builds and audit reachable vulnerabilities in CI; macOS builds
  require macOS 13 or newer.
- Add a release preparation helper and check workflow syntax and release archives in CI.
- Prepare a checksum-verified installer and Homebrew formula updates for release distribution.
- Move extension examples under `docs/examples/`.
- Publish verified macOS/Linux amd64/arm64 archives and SHA-256 checksums from stable version
  tags on `main`, with GitHub release notes from this changelog.
- Move the entry point to the repository root and settings and pairing state to `~/.rmt`.
- Load trusted Lua extensions for launch preparation and custom HTTP or WebSocket program
  integrations. Application flags pass through by default instead of assuming a worktree helper.

- Start ordinary commands in the host's configured shell and load zsh or bash startup settings;
  prevent startup files from consuming the command stream and report unsupported interactive
  programs instead of attempting them without a terminal.
- Let supported native launches use host aliases and argument expansion, and use a host worktree
  helper's explicit preparation mode before starting OpenCode or Codex locally.
- Preserve an existing shell when reconnection encounters a busy or temporarily unavailable host.
- Keep an active native client proxy available if its prompt connection drops, and allow Ctrl-C
  to interrupt worktree preparation.

- Attach to the native OpenCode service and Codex managed app-server without stopping them when
  a terminal disconnects. Let remote shell commands already in progress finish on disconnect.

- Start the local host service automatically on first use on macOS and systemd-based Linux.
- Keep shell state and running commands during short network interruptions; bound buffered output
  and bind reconnects to the original paired device.
- Check supported program capabilities on the client and host, and organize packages as hosts and programs.

- Use TOML for editable settings and retain pairing identities when host names change.
- Show the host directory, print an exit message, restore the directory on reconnect without
  replaying commands, and reject incompatible protocol versions.
- Preserve shell exit status, rate-limit pairing attempts, and report unavailable backends.

- Rename the `agents` configuration field to `programs` and remove old compatibility paths.

- Replace workspace and session management commands with `rmt <host>`, a locally edited prompt
  running commands in a persistent shell on the host, starting in its home directory.
- Launch native OpenCode and Codex interfaces in the current remote directory.
- Pair automatically on first connection using a fingerprint and an owner-only one-time code;
  configure host addresses, listeners, and programs in JSON.
- Reload the private device registry so removing a paired client revokes active access.
- Move the command entry point to `src/cmd/rmt`.
