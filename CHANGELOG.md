# Changelog

## Unreleased

- Support paired HTTPS over reachable IP networks without requiring Tailscale; retain the existing
  Tailscale connection path for current users.
- Organize source by command, host, agent, session, network, and service responsibilities.
- Authenticate local agent bridges and Codex's loopback WebSocket backend.
- Restart failed agents independently and include agent errors in host diagnostics.
- Refuse ambiguous session selection when a device could not be checked.
- Offer background service installation during setup on macOS and Linux.
- Add an MIT license and the planned GitHub module path.
- Add interactive setup for the host workspace, enabled agents, and optional native sign-in.
- Rename the product and executable to `rmt`, and allow each host to configure its workspace.
- Start the local OpenCode client from any directory without requiring a local `~/dev`.
- Use simple animal names for remote session handles while keeping earlier handles usable.

- List saved OpenCode and Codex sessions across reachable hosts and reconnect using stable short names.

- Run native OpenCode and Codex interfaces against a host on your Tailscale network, starting in the host's `~/dev`.
