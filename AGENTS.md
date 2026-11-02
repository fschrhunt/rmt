# Developing rmt

rmt connects native program terminals to backends on another device. It is a small Go
program; keep native program interfaces intact, preserve running work and credentials, and avoid
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

- `src/cmd/rmt/main.go`: command entry point.
- `src/cli/`: command dispatch, local prompt, and pairing.
- `src/client/`: host connections and native application launches.
- `src/hosts/`: host lifecycle and program supervision.
- `src/programs/`: native OpenCode and Codex interfaces.
- `src/shell/`: host command protocol and local command parsing.
- `src/config/`: user configuration.
- `src/network/`: paired HTTPS and private device credentials.
- `src/services/`: automatic user service installation.
- `*_test.go`: offline behavior tests next to the code they cover.
- `README.md`: installation, usage, security boundaries and operational limitations.

## Conventions

- Run `./x check` before finishing changes. Tests protect meaningful behavior and run offline;
  never depend on a live Tailscale network, real programs, or model accounts.
- Treat authentication, command execution, proxy behavior, and running host services as part of
  the user-facing contract. Do not restart installed services while testing without a reason.
- Update the README and `CHANGELOG.md` when behavior changes. Keep comments accurate and focused
  on purpose and contract.
