# remote

Use a native local OpenCode or Codex terminal interface with an agent running on one of your own
Tailscale devices:

```sh
remote mini opencode
remote server codex
```

The local interface connects to a host service that starts agents in the host's `~/dev`. There is
no SSH or remote terminal in the session. Arguments after the agent are passed to its native
client, for example `remote mini opencode --continue`.

Both machines need Tailscale, and the host needs OpenCode v2 and Codex installed. The local machine
also needs the chosen agent installed. The devices must belong to the same Tailscale user.

To run the host manually, start `remote serve` on it before connecting. If a host service is
already running, stop that service before starting another one. The existing mac and mini hosts
use the `com.fschr.remote` LaunchAgent; server uses the `remote.service` user service. Service
installation is currently a manual, machine-specific setup.

Traffic is restricted to Tailscale, and requests are checked against the host owner's Tailscale
user identity. Agent backends listen on loopback. OpenCode v2 resolves its starting directory on
the client, so a temporary localhost bridge translates the startup `~/dev` location to the host's
`~/dev`. Other paths are passed through to OpenCode unchanged. The bridge exists only while the
local OpenCode client is running.

`~/dev` is the starting workspace, not a security sandbox. Agent permissions and sandbox settings
still apply. The service holds running backends while clients disconnect; session browsing and
resuming are handled by the native agent. A host restart stops active backend processes. Codex's
remote app-server transport is experimental. Claude Code is not supported.

## Development

Run `./x check` for formatting, vet, and tests. `./x build` creates `dist/remote` without replacing
the installed binary or restarting a running host service. See [CONTRIBUTING.md](CONTRIBUTING.md)
for the development workflow.
