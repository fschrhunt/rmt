# Contributing

rmt is a small Go program. Keep the native agent interfaces intact and favor simple changes.
Go 1.24 or newer, Git, a POSIX shell, and ShellCheck are needed for development. The checks do not
require a Tailscale connection, real agents, or model credentials.

```sh
./x hooks  # once per clone
./x check  # formatting, vet, offline tests, and ShellCheck; CI runs the same checks
./x build  # writes dist/rmt without replacing the installed binary
```

`./x` defaults to `check`. Run `./x help` for the other commands. Use `./x dev` to run this
checkout. Rebuilding does not restart an installed host service.

Tests should protect meaningful behavior and run offline. Never include credentials, prompts,
or other private data in issue reports. Update the README and changelog when behavior changes.

For bug reports, include the operating system, command, expected behavior, and what happened.
Redact private paths, prompts, tokens, and other sensitive details before sharing output.

By contributing you agree that your work is licensed under the project's MIT license.
