# rmt

Run the native OpenCode or Codex terminal interface locally while the agent works on another
device you own. Connect over a reachable LAN, VPN, or forwarded address. The interface is local;
there is no remote terminal or SSH in the session.

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
Both devices need the selected agent installed. OpenCode requires v2; Codex must support
`app-server --ws-auth capability-token`, `--ws-token-sha256`, and `--remote-auth-token-env`.

On each host, run interactive setup to choose the workspace, agents, and whether to allow paired
HTTPS connections. It checks for the agent executables, can start their native sign-in flows, and
offers to install and start a per-user host service on macOS or Linux:

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

You can also edit the host config directly. For example:

```json
{
  "workspace": "~/dev",
  "agents": ["opencode", "codex"],
  "listen": "0.0.0.0:47473"
}
```

`workspace` must exist. Choose at least one supported agent; omitting `agents` enables both.
Omitting `listen` disables paired HTTPS. The host reads settings when it starts, so restart its
service when safe after editing. The legacy Tailscale ports and pairing-code lifetime are fixed.

## Pair devices

When prompted by `rmt setup`, enable paired HTTPS. The default listener is `0.0.0.0:47473`;
choose a different bind address or port if needed. Once the host service is running, generate a
single-use code on the host:

```sh
rmt code
```

On the client, pair a name with the host's reachable address:

```sh
rmt pair mini https://HOST:47473
rmt mini opencode
```

Compare the certificate fingerprint shown on the client with `rmt code` on the host before
accepting it, then enter the code. The code expires after 15 minutes and is valid for one device.
Keep it private. `rmt code` creates a new code without restarting the host or its agents. The
host's identity and paired-device registry, and the client's host credentials, are stored in
owner-only files beside the rmt config. Back up the host identity if it needs to stay trusted.

On a client, `hosts.json` contains paired names, addresses, certificate fingerprints, and device
tokens. You can edit a host's `url` if its address changes; keep its verified fingerprint and
token unchanged. Treat this file as a secret. On a host, `identity.pem` contains its private TLS
identity and `devices.json` holds paired-device token hashes. Keep those files private; use the
commands below to manage devices.

Use `rmt hosts` to list local paired hosts and `rmt unpair mini` to remove a local profile.
Removing a profile does not revoke access on the host. On the host, `rmt devices` lists paired
clients and `rmt revoke <device-id>` revokes one and closes its active connections.

The address must already be reachable and the host's firewall must allow the chosen TCP port.
Pairing does not set up NAT traversal, a relay, or firewall rules. You can use a LAN address,
DNS name, Tailscale address, another VPN, or a securely forwarded address without changing rmt.

Existing Tailscale setups continue to work without pairing: devices must belong to the same
Tailscale user and their tailnet policy must permit TCP ports 47470–47472. A paired host name
takes precedence if it matches a Tailscale device name.

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

Paired connections use TLS pinned to the verified host certificate and a separate credential per
device. The optional legacy Tailscale listeners authenticate requests against the host owner's
Tailscale identity. Agent backends listen on loopback with per-start credentials. Native client
bridges run locally with separate credentials while their client is open. OpenCode's bridge maps
the client's current directory to the host workspace; other paths pass through unchanged. An agent
that exits is restarted independently without stopping the other agent.

The workspace is a starting location, not a security sandbox. Agent permissions and sandbox settings
still apply. Codex's remote app-server transport is experimental. Claude Code is not supported.

## Development

Run `./x check` for formatting, vet, tests, and ShellCheck. `./x build` creates `dist/rmt` without replacing
the installed binary or restarting a running host service. See [CONTRIBUTING.md](CONTRIBUTING.md)
for the development workflow.
