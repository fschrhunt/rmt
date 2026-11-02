# rmt

Run the native OpenCode or Codex terminal interface locally while the agent works on another
Tailscale device you own. The interface is local; there is no remote terminal or SSH in the session.

```sh
rmt mini opencode
rmt server codex
```

Arguments after the agent are passed to its native client, for example
`rmt mini opencode --continue`.

## Setup

Install Go 1.24 or newer, build `rmt`, and put the executable on your PATH on each device:

```sh
./x build
mkdir -p "$HOME/.local/bin"
install -m 755 dist/rmt "$HOME/.local/bin/rmt"
```

Some systems already have a different `rmt` program; make sure `~/.local/bin` comes first on PATH.
Both devices need Tailscale and the selected agent installed. OpenCode requires v2; Codex must
support `app-server --ws-auth capability-token` and `--ws-token-sha256`. Devices must belong to
the same Tailscale user, and the tailnet policy must permit TCP ports 47470–47472 between them.

On each host, run interactive setup to choose the workspace and which agents to serve. It checks
for Tailscale and the agent executables, can start their native sign-in flows, and offers to install
and start a per-user host service on macOS or Linux:

```sh
rmt setup
```

The workspace directory must already exist; the default is `~/dev`. `rmt config` shows the
effective settings and config path; `rmt config set ~/projects` changes just the workspace.
Configuration is stored in `$XDG_CONFIG_HOME/rmt/config.json`, or `~/.config/rmt/config.json`
when `XDG_CONFIG_HOME` is unset. No config file is created until settings are saved. Restart an
already running host service when it is safe to apply changed settings. Setup never replaces an
existing service; use `rmt serve` to run a host manually. Only one host service can use the ports
on a device at a time. The service uses the `rmt` executable and PATH present during setup.

On macOS the installed service writes diagnostics to `~/Library/Logs/rmt/host.log`; on Linux use
`journalctl --user -u rmt.service`. Agent error output is included and may contain sensitive
details. A Linux user service normally starts at login; enabling user lingering is a separate
system setting if the host needs to run without a login.

To apply changed settings to a service installed by setup, wait until no agents are working and
run `launchctl kickstart -k gui/$(id -u)/com.rmt.host` on macOS or
`systemctl --user restart rmt.service` on Linux. Restarting stops that host's running agents.

## Find and reconnect to sessions

```sh
rmt ls                         # sessions on reachable hosts
rmt ls mini                    # sessions on one host
rmt connect fox                # reconnect using a name from rmt ls
rmt mini opencode --connect    # reconnect when there is exactly one match
```

Session handles are stable animal names derived from each agent's own session ID, such as `fox`;
they do not change the agent's session title. Earlier handles still work. If names collide,
`rmt ls` shows a host-qualified animal path such as `mini/fox/otter`. It lists native OpenCode and
Codex sessions under each host's configured workspace, including sessions started outside rmt, and
shows active sessions when the agent reports them. Offline devices are reported but cannot be
listed. Devices without a host service are omitted; use `rmt ls <host>` to check a particular host.
Agents may not save a new session until its first message. An unqualified `rmt connect fox`
requires every eligible device to be reachable and checked, because an unchecked device might
have the same name. If some devices cannot be checked, use `rmt connect mini/fox` to select a
host; that host's session scan must still complete successfully.

Closing the local window disconnects the client; it does not stop the host service. Use
`rmt connect <name>` to reopen the native interface. A running task may wait for approval, and an
explicit interrupt can stop it. A host or service restart stops the backend; session history and
resume behavior remain subject to the agent. Automatic reconnect is not implemented.

Traffic is restricted to Tailscale, and requests are checked against the host owner's Tailscale
user identity. Agent backends listen on loopback with per-start credentials. OpenCode resolves its
starting directory on the client, so a temporary localhost bridge maps the client's current directory to the host workspace.
Other paths are passed through unchanged. The authenticated bridge exists only while the OpenCode
client runs. An agent that exits is restarted independently without stopping the other agent.

The workspace is a starting location, not a security sandbox. Agent permissions and sandbox settings
still apply. Codex's remote app-server transport is experimental. Claude Code is not supported.

## Development

Run `./x check` for formatting, vet, and tests. `./x build` creates `dist/rmt` without replacing
the installed binary or restarting a running host service. See [CONTRIBUTING.md](CONTRIBUTING.md)
for the development workflow.
