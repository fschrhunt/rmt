# Changelog

## Unreleased

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
