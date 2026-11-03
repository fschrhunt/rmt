# Changelog

## Unreleased

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
