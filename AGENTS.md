# Developing rmt

rmt connects native agent terminals to backends on another device. It is a small Go
program; keep native agent interfaces intact, preserve running work and credentials, and avoid
configuration or abstractions beyond what a change needs.

## Commands

```sh
./x check                 # formatting, vet, offline tests, and ShellCheck; CI runs the same checks
./x test ./... -run NAME  # a focused Go test
./x build                 # build dist/rmt without replacing an installed binary
./x dev --help            # run this checkout
./x hooks                 # install the pre-commit hook once per clone
```

## Where things live

- `main.go`: command entry point.
- `src/cli/`: command dispatch, interactive setup, pairing, and terminal output.
- `src/client/`: host connections and session discovery.
- `src/host/`: host lifecycle and agent supervision.
- `src/agent/`: native OpenCode and Codex interfaces and session APIs.
- `src/session/`: session data, names, and selection.
- `src/config/`: user configuration and workspace settings.
- `src/network/`: paired HTTPS, private device credentials and optional Tailscale integration.
- `src/service/`: macOS and Linux user service installation.
- `*_test.go`: offline behavior tests next to the code they cover.
- `README.md`: installation, usage, security boundaries and operational limitations.

## Conventions

- Run `./x check` before finishing changes. Tests protect meaningful behavior and run offline;
  never depend on a live Tailscale network, real agents, or model accounts.
- Treat authentication, workspace selection, proxy behavior, and running host services as part of
  the user-facing contract. Do not restart installed services while testing without a reason.
- Update the README and `CHANGELOG.md` when behavior changes. Keep comments accurate and focused
  on purpose and contract.
