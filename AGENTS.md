# Developing remote

Remote connects native agent terminals to a backend on another Tailscale device. Keep the local
agent interface intact, keep the program small, and preserve existing work and credentials.

- Run `./x check` before finishing changes; `./x build` builds to `dist/remote` without replacing
  the installed binary. `./x hooks` installs the pre-commit hook.
- Tests run offline and do not depend on a live Tailscale network, real agents, or model accounts.
- Treat authentication, workspace selection, proxy behavior, and running host services as part of
  the user-facing contract. Do not restart installed services while testing without a reason.
- Update the README and `CHANGELOG.md` when behavior changes. Keep comments accurate and focused
  on purpose and contract.
