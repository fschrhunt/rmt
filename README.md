# rmt

A local terminal prompt for working on another machine. Commands run on the host; OpenCode and
Codex use their native local interfaces with their backends on the host.

```text
$ rmt mini
mini ~: ls
mini ~: cd dev/my-project
mini ~/dev/my-project: opencode
mini ~/dev/my-project: exit
[rmt is exiting]
```

The prompt starts in the host user's home directory. `cd`, shell variables, and the environment
persist until you disconnect. Pipes and redirection run on the host. Press Ctrl-D or type `exit`
to disconnect; rmt prints `[rmt is exiting]`. Exiting the native program or closing the terminal
disconnects that client. OpenCode and Codex keep control of their own managed services and saved
sessions. The rmt host service stays available for other devices.

## Configuration

Settings live in `~/.config/rmt/config.toml`, or `$XDG_CONFIG_HOME/rmt/config.toml` when
`XDG_CONFIG_HOME` is set. Create the file on each machine as needed. All fields are optional:

```toml
serve = true
listen = "0.0.0.0:47473"
programs = ["opencode", "codex"]
shell = "/bin/sh"

[hosts]
mini = "https://mini.example:47473"
server = "https://192.168.1.10:47473"
```

`serve` controls automatic local host-service installation and defaults to true. Set it to false
before first use on a client-only machine; it does not stop a service that is already running.
`listen` controls the host service's HTTPS listener and defaults to `0.0.0.0:47473`. Bind to a
specific network interface if desired. `programs` selects host backends; both are enabled by default.
`shell` is an absolute path to a POSIX-compatible shell, defaulting to `/bin/sh`. `hosts` maps
local names to reachable HTTPS addresses. Without an entry, `rmt mini` connects to
`https://mini:47473`, using normal DNS or your network's name resolution. Use LAN, VPN, Tailscale,
or any other network that provides a reachable address; rmt does not configure networking or NAT.
Changes to host settings take effect when its service restarts. Host names can be changed while
keeping the same address and paired identity.

## Host service and pairing

Install rmt and the desired programs on each machine. The first time you run `rmt` or
`rmt <host>` on a machine, rmt installs and starts its user host service automatically on macOS
or Linux with systemd. It keeps an existing service intact. Build from source with `./x build`;
install the resulting binary somewhere permanent before first use. The service uses the `PATH`
and non-secret native configuration paths from the first run. Existing service files are not
rewritten. Environment-only credentials are not copied into the service file; use the program's
own host-side authentication or configure the service environment separately. If its user service manager is unavailable,
rmt reports the error while still allowing client connections.

The first `rmt <host>` connection prompts for pairing. On the host, open the private
`~/.config/rmt/pairing.json` file (under `$XDG_CONFIG_HOME` if set), compare its `verify` value with
the one shown locally, and enter its one-time code. Confirm the verification value through a trusted
channel. The code expires and disappears after successful pairing. Subsequent connections use a
saved credential and pinned host identity in the client's private config directory.

A paired device can execute commands with the host user's permissions. On the host,
`devices.json` in the same private directory lists paired devices. Remove a device's entire entry
to revoke it; the running service reloads the file and closes that device's active connections.
Use an editor that saves with an atomic rename. While the file is invalid, new requests are
denied; if it remains invalid for two seconds, active connections are closed. Keep it owner-only. To forget a host on a client, remove its entry from the
private `hosts.json` file; this alone does not revoke its host access.

## Terminal behavior

Input editing and history happen locally; Tab completes remote paths. Command output comes from the host. Ordinary commands run in a
persistent non-interactive POSIX shell, not a remote terminal: programs cannot read interactive
stdin, and full-screen applications are not supported. Known programs such as `vim` and `tmux` are
reported as unsupported, but rmt cannot identify every interactive program. Ctrl-C asks the host
to terminate the current command. If a connection drops during a command, its outcome may be
unknown; rmt does not replay it. After a network interruption, rmt attempts to reconnect to the
same shell. The host retains its shell state and up to 1 MiB of output for 15 seconds; it reports
any lost output. A shell command already running when the connection closes is allowed to finish.
After the grace period, an idle shell is cleaned up. If the rmt host restarts or the session
expires, rmt starts a new shell and tries to restore the last known directory. Supported native applications are `opencode` and `codex`, launched
as standalone commands from the current remote directory. Program flags are passed to the native CLI;
shell expansions, pipes, and wrappers around these application launches are not supported.
The host and client must use the same rmt protocol version. rmt checks that installed programs
support the capabilities needed for their role. On the host, OpenCode uses its native managed
service and Codex uses its native managed app-server daemon; rmt does not stop either on detach.
OpenCode must support its v2 managed-service commands. If its shared service is explicitly disabled,
rmt reports that setting instead of changing it. Its service address must use localhost or an IP
address belonging to the host. Native interfaces handle their own network interruptions and task
state; rmt does not replay or cancel their work.

Run `rmt help` for the short usage. See [CONTRIBUTING.md](CONTRIBUTING.md) for development.
