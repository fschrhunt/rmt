# Developing rmt

rmt connects native agent terminals to backends on another Tailscale device. It is a small Go
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

- `main.go`: host, native clients, proxy and Tailscale authentication.
- `sessions.go`: discovery, animal handles and reconnect selection.
- `config.go`, `setup.go` and `service.go`: host configuration, interactive setup and services.
- `*_test.go`: offline behavior tests with fake agents and Tailscale responses.
- `README.md`: installation, usage, security boundaries and operational limitations.

## Conventions

- Run `./x check` before finishing changes. Tests protect meaningful behavior and run offline;
  never depend on a live Tailscale network, real agents, or model accounts.
- Treat authentication, workspace selection, proxy behavior, and running host services as part of
  the user-facing contract. Do not restart installed services while testing without a reason.
- Update the README and `CHANGELOG.md` when behavior changes. Keep comments accurate and focused
  on purpose and contract.
