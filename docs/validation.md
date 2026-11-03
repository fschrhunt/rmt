# Reliability validation and measurement

Run `./x check` for the offline suite. The client–host interaction tests can also be repeated
under the race detector:

```sh
GOPROXY=off GOSUMDB=off ./x test ./src/hosts -run '^TestClient' -race -count=10 -timeout=120s
```

These tests pair through real TLS handlers, save a temporary client profile, and use the
production client, shell and program handlers. They check:

- A saved pairing rejects a changed host certificate before sending an HTTP request.
- A connection dropped after command output resumes the same shell, preserves state and the
  nonzero exit status, and executes the command once. Closing the client expires that session.
- Losing the host listener and connection reports an unknown command outcome without replay.
- An interrupt sent by the client reaches a ready child command, returns its trapped exit
  status, and leaves the same shell and its environment usable.
- OpenCode streams HTTP output through both authenticated bridges, translates the startup
  directory, and cancels the upstream request when the native client disconnects.
- Codex exchanges WebSocket messages through both bridges and the private daemon socket,
  requires the local attachment credential, and keeps the device credential out of the daemon.
- Both native launch paths preserve prepared arguments and a nonzero native exit status,
  close the local bridge listener on exit, and leave the command prompt usable.
- Dropping the host after each native fixture acknowledges received data breaks the active
  HTTP stream or WebSocket before its deadline. The fixture then exits, its exit status reaches
  the caller, and the local listener closes. Native disconnect also ends the upstream stream.

All identities, profiles, service responses and native executable fixtures are temporary.
Listeners use loopback or private Unix sockets. The tests do not install programs, start the
installed rmt service, use model accounts, or contact non-loopback networks.

## Opt-in measurements

With dependencies already cached and the required Go toolchain installed:

```sh
GOPROXY=off GOSUMDB=off ./x test ./src/hosts -run '^$' \
  -bench '^BenchmarkPaired(ShellStartup|CommandRoundTrip)$' -benchtime=100x -count=5
```

Each result reports mean elapsed nanoseconds per operation (`ns/op`):

- `PairedShellStartup`: `client.OpenShell` through receipt of the protocol ready event,
  including config/profile reads, a fresh pinned TLS/WebSocket connection and `/bin/sh` spawn.
  Pairing, host setup and client close are outside the timer. Ready does not mean a native
  application has rendered or that the shell has executed its first command.
- `PairedCommandRoundTrip`: submission of `printf probe` through receipt of its completion
  event, including output delivery to a discard writer, on an existing connection. Pairing,
  connection setup, a warmup command and cleanup are outside the timer. This measures command
  completion latency, not keystroke-to-screen latency.

Compare repeated results on the same machine, Go version and load; record those alongside the
command and output. These are local baseline measurements with no pass/fail latency threshold,
percentile claim, cross-device network delay or reconnection timing claim. They add no runtime
instrumentation and are not run by `./x check`.

The native fixtures validate rmt's bridge contracts, not compatibility with an installed
OpenCode or Codex release or a real TUI. Terminal rendering, resizing and signal handling belong
to the native interface and still need manual checks in a real terminal on macOS and Linux.
Ordinary remote shell commands have no PTY or resize protocol. Real device sleep/wake, host
crashes/restarts, VPN changes, WAN latency and native application recovery remain unvalidated
by these loopback tests.

## Manual native-terminal checks

Use an already paired test host and already installed supported native programs, with a
disposable session and no valuable running work. For each of OpenCode and Codex on macOS and
Linux, record the OS, terminal, rmt and native program versions, and the observed results:

- Launch from a remote project directory; confirm the displayed directory and arguments.
- Resize from approximately 80×24 to 140×40 and back while idle and while output is arriving;
  check redraw, cursor placement, input editing and scrolling.
- Use the program's normal exit action; confirm its exit status and that the rmt prompt accepts
  another command. Check Ctrl-C according to the native program's documented behavior.

These are a manual checklist, not recorded passes. The automated host-loss fixture checks
transport failure propagation; it does not establish that a real native UI exits or reconnects.
