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
or other private data in issue reports. Update the README when behavior changes; release notes are written when releasing.

Dependabot opens one weekly pull request for dependency and GitHub Actions updates. Review changes and required checks before merging.

For bug reports, include the operating system, command, expected behavior, and what happened.
Redact private paths, prompts, tokens, and other sensitive details before sharing output.

## Releases

From a clean, up-to-date `main` checkout with Git and authenticated `gh`, run:

```sh
.github/release/release.sh --dry-run v1.2.3  # verify archives and formula, show the changelog; publishes nothing
.github/release/release.sh v1.2.3            # write the notes, then release
```

The helper drafts the notes from the pull requests merged since the last release and opens them
in your editor; what you save is published. It requires passing CI on the exact `main` commit
and asks Actions to release it. Actions checks that main has not moved and CI again, adds the
notes to `CHANGELOG.md` in a release commit on top of that main commit, and pushes the tag at it
using `RELEASE_TOKEN`, with a `release/v1.2.3` pull request that brings the changelog to `main`;
merge it after CI passes. `CHANGELOG.md` is never edited by hand. After publishing, the workflow installs the release on macOS and Linux and checks `rmt --version`
before proposing the Homebrew update.

Use a new stable semantic version with no leading zeros; prerelease and build suffixes are not
supported. The [release workflow](.github/workflows/release.yml) rejects tags that aren't a release commit
on top of `main` (changing only `CHANGELOG.md`), commits without passing main CI, and missing or
duplicate changelog sections, then runs `./x check`.
It builds `rmt_vX.Y.Z_{darwin,linux}_{amd64,arm64}.tar.gz`, each containing an executable `rmt`
and `LICENSE`, and verifies their contents, build targets, and `checksums.txt` before publishing
a GitHub release with that version's changelog notes using the automatic `GITHUB_TOKEN`.
CI and releases use `.github/release/package.sh`; from the repository root, run
`.github/release/package.sh v0.0.0 /tmp/rmt-release` to build and verify the same
four archives without publishing. Release preparation and formula helpers also
live in `.github/release/`; `./x shell` includes those scripts in ShellCheck.
The release workflow attests the four archives and `checksums.txt` using GitHub provenance.
The tag and formula jobs use the repository's `RELEASE_TOKEN` Actions secret to push the tag
and changelog branch and open PRs, including one containing `rmt.rb`; review and merge that PR after CI passes. The token needs access to this repository
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
