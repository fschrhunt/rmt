# Contributing

rmt is a small Go program. Keep the native program interfaces intact and favor simple changes.
Go 1.24 or newer, Git, a POSIX shell, and ShellCheck are needed for development. The checks do not
require a Tailscale connection, real programs, or model credentials.

```sh
./x hooks  # once per clone
./x check  # formatting, vet, offline tests, and ShellCheck; CI runs the same checks
./x build  # writes dist/rmt without replacing the installed binary
```

`./x` defaults to `check` and delegates to `scripts/dev.sh`. Run `./x help` for the other
commands. Use `./x dev` to run this checkout. Rebuilding does not restart an installed host service.

`main.go` starts the CLI; implementation and tests live in packages under `src/`. See
[AGENTS.md](AGENTS.md) for the package map and development conventions.

Tests should protect meaningful behavior and run offline. Never include credentials, prompts,
or other private data in issue reports. Update the README and changelog when behavior changes.

GitHub Actions checks pull requests on Linux and macOS. A weekly workflow proposes compatible
Go dependency updates after running checks; Dependabot proposes GitHub Actions updates separately.
Review and merge these pull requests normally. If an update branch exists without its automation
PR, inspect it before deleting it and retrying the workflow.

For bug reports, include the operating system, command, expected behavior, and what happened.
Redact private paths, prompts, tokens, and other sensitive details before sharing output.

## Releases

Move the release's entries from `Unreleased` into a nonempty `## vX.Y.Z - YYYY-MM-DD`
changelog section, leaving `Unreleased` for future changes. Merge that change to `main` and
run `./x check`. From an up-to-date `main`, create and push an annotated tag:

```sh
git tag -a v1.2.3 -m v1.2.3
git push origin v1.2.3
```

Use a new stable semantic version with no leading zeros; prerelease and build suffixes are not
supported. The [release workflow](.github/workflows/release.yml) rejects tags outside `main`'s
history, commits without passing main CI, missing or duplicate changelog sections, and empty notes,
then runs `./x check`.
It builds `rmt_vX.Y.Z_{darwin,linux}_{amd64,arm64}.tar.gz`, each containing an executable `rmt`
and `LICENSE`, and verifies their contents, build targets, and `checksums.txt` before publishing
a GitHub release with that version's changelog notes using the automatic `GITHUB_TOKEN`.
The formula job uses the repository's `RELEASE_TOKEN` Actions secret to open a PR containing
`rmt.rb`; review and merge that PR after CI passes. The token needs access to this repository
with Contents and Pull requests read/write. Keep it in GitHub Actions secrets, never in the
repository. The formula job skips older releases and refuses to overwrite an existing release
branch; if a retry reports an existing branch, inspect that branch and PR before changing it.
Rerun a failed workflow after resolving its cause; an existing release is never overwritten.
Do not move published tags.

If this repository is private, releases and downloads require GitHub access to the repository;
they cannot serve as anonymous public downloads. Download assets from the release page or with
authenticated `gh release download v1.2.3 --repo fschrhunt/rmt`. After downloading the chosen
archive and `checksums.txt`, check its SHA-256 against the matching line (`sha256sum` on Linux,
`shasum -a 256` on macOS), then extract it and install `rmt` in a permanent directory on `PATH`.
macOS binaries are unsigned and not notarized. Installing a release does not restart an existing
host service.

By contributing you agree that your work is licensed under the project's MIT license.
