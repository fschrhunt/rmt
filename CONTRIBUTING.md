# Contributing

rmt is a small Go program. Keep the native agent interfaces intact and favor simple changes.
Go 1.24 or newer, Git, and a POSIX shell are needed for development; the checks do not require a
Tailscale connection or model credentials.

```sh
./x hooks  # once per clone
./x check  # formatting, vet, and tests; CI runs the same checks
./x build  # writes dist/rmt without replacing the installed binary
```

`./x` defaults to `check`. Run `./x help` for the other commands. Use `./x dev` to run this
checkout. Rebuilding does not restart an installed host service.

Tests should protect meaningful behavior and run offline. Never include credentials, prompts,
or other private data in issue reports. Update the README and changelog when behavior changes.
