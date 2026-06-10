# Release Process

`yllmd` currently targets a local-only release surface. This project uses date-based versions because it is expected to update continuously.

## Versioning

Use this format:

- Development and beta builds: `YY.MM.DD.NN`
- Release builds: `YY.MM.DD.NN-Release`

`NN` is a zero-padded incrementing number for additional builds on the same day. For example, the first development build on June 9, 2026 is `26.06.09.00`; the next build that day is `26.06.09.01`.

Everything should be considered development or beta unless the version is tagged with `-Release`.

## Scope

Included:

- `yllmd` daemon over a Unix domain socket.
- `yllmctl` health, status, generation, cancel, model list, model install, and model rollback commands.
- Local runner integration over stdio JSON Lines.
- Local model version activation and rollback.

Excluded:

- Remote generation providers.
- Automatic model update checks and downloads.
- Runtime config reload.
- Packaged installers.

## Prerequisites

- Go 1.22 or newer.
- A Unix-like system with Unix domain sockets.
- `yllama-runner` installed for real local inference.

## Checks

Run these before tagging:

```sh
make release-check VERSION=26.06.09.01-Release
```

The release check builds tarballs for:

- `darwin_amd64`
- `darwin_arm64`
- `linux_amd64`
- `linux_arm64`
- `freebsd_amd64`
- `freebsd_arm64`

Each tarball includes:

- `yllmd`
- `yllmctl`
- `VERSION`
- `README.md`
- `LICENSE`
- `config.example.yaml`
- `docs/`
- `packaging/`

## Tagging

1. Confirm `README.md`, `docs/protocol.md`, and `docs/configuration.md` describe the implemented release surface.
2. Confirm `git status --short` is clean.
3. Create an annotated tag using the release version:

```sh
git tag -a 26.06.09.01-Release -m "Release 26.06.09.01-Release"
```

4. Push commits and tag:

```sh
git push origin main
git push origin 26.06.09.01-Release
```

5. The `Release Artifacts` GitHub Actions workflow runs on `*-Release` tags, validates the tag format, runs `make release-check`, and uploads the tarballs plus checksum file as workflow artifacts.

6. Attach `dist/*.tar.gz` and the matching `dist/checksums_<version>.txt` file to the release.

For development or beta artifacts, omit `-Release`:

```sh
make dist VERSION=26.06.09.01
```
