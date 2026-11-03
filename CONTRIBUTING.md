# Contributing

rmt is a small Go program. Keep the native program interfaces intact and favor simple changes.
Go 1.27.3 or newer, Git, a POSIX shell, and ShellCheck are needed for development. The checks do not
require a Tailscale connection, real programs, or model credentials.

```sh
./x hooks  # once per clone
./x check  # formatting, vet, offline tests, and ShellCheck; CI runs the same checks
./x build  # writes dist/rmt without replacing the installed binary
```

`./x` defaults to `check`. Run `./x audit` for the network-backed vulnerability scan, or
`./x help` for the other commands. Use `./x dev` to run this
checkout. Rebuilding does not restart an installed host service.

`main.go` starts the CLI; implementation and tests live in packages under `src/`. See
[AGENTS.md](AGENTS.md) for the package map and development conventions.

Tests should protect meaningful behavior and run offline. Never include credentials, prompts,
or other private data in issue reports. Update the README and changelog when behavior changes.

Dependabot opens one weekly pull request for dependency and GitHub Actions updates. Review changes and required checks before merging.

For bug reports, include the operating system, command, expected behavior, and what happened.
Redact private paths, prompts, tokens, and other sensitive details before sharing output.

## Releases

From a clean, up-to-date `main` checkout with Git and authenticated `gh`, run:

```sh
scripts/release.sh v1.2.3  # opens a PR to name the Unreleased changelog section
scripts/release.sh v1.2.3  # after merging and updating main: requests the release
```

The helper keeps the checkout on main, requires passing CI on its exact commit, and asks Actions
to create the tag. Actions checks that main has not moved, validates the notes and CI again,
and pushes the tag using `RELEASE_TOKEN`. A maintainer can also push a valid tag directly.

Use a new stable semantic version with no leading zeros; prerelease and build suffixes are not
supported. The [release workflow](.github/workflows/release.yml) rejects tags outside `main`'s
history, commits without passing main CI, missing or duplicate changelog sections, and empty notes,
then runs `./x check`.
It builds `rmt_vX.Y.Z_{darwin,linux}_{amd64,arm64}.tar.gz`, each containing an executable `rmt`
and `LICENSE`, and verifies their contents, build targets, and `checksums.txt` before publishing
a GitHub release with that version's changelog notes using the automatic `GITHUB_TOKEN`.
The release workflow attests the four archives and `checksums.txt` using GitHub provenance.
The tag and formula jobs use the repository's `RELEASE_TOKEN` Actions secret to create the tag
and open a PR containing `rmt.rb`; review and merge that PR after CI passes. The token needs access to this repository
with Contents and Pull requests read/write. Keep it in GitHub Actions secrets, never in the
repository. The formula job skips older releases and refuses to overwrite an existing release
branch; if a retry reports an existing branch, inspect that branch and PR before changing it.
Before generating the formula, it verifies the downloaded manifest's provenance against the
release workflow, tag, and commit. Review the formula's URLs and hashes before merging.
Rerun a failed workflow after resolving its cause; an existing release is never overwritten.
Do not move published tags.

To verify a downloaded archive (or `checksums.txt`) with a current, authenticated GitHub CLI:

```sh
gh attestation verify rmt_v1.2.3_linux_amd64.tar.gz --repo fschrhunt/rmt \
  --signer-workflow fschrhunt/rmt/.github/workflows/release.yml \
  --source-ref refs/tags/v1.2.3 --source-digest EXPECTED_COMMIT_SHA --deny-self-hosted-runners
```

Replace `EXPECTED_COMMIT_SHA` with the full release commit SHA you trust. This verifies the
artifact and workflow identity; it does not establish that the source is safe. The installer and
Homebrew verify checksums, but do not verify attestations.

If this repository is private, releases and downloads require GitHub access to the repository;
they cannot serve as anonymous public downloads. Download assets from the release page or with
authenticated `gh release download v1.2.3 --repo fschrhunt/rmt`. After downloading the chosen
archive and `checksums.txt`, check its SHA-256 against the matching line (`sha256sum` on Linux,
`shasum -a 256` on macOS), then extract it and install `rmt` in a permanent directory on `PATH`.
macOS binaries are unsigned and not notarized. Installing a release does not restart an existing
host service.

The installer trusts the release URL (including an explicit `RMT_RELEASES` mirror) for both the
archive and manifest. HTTPS URLs cannot redirect to HTTP; setting an HTTP mirror explicitly opts
into plaintext downloads. The installer checks checksums and archive structure, not attestations.

By contributing you agree that your work is licensed under the project's MIT license.
