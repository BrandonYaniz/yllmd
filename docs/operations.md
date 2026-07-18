# Operations

## Service model

`yllmd` runs as a local daemon. It listens on a Unix domain socket and should normally run under a dedicated service account.

Recommended socket permissions:

```text
owner: yllmd
group: yllm
mode: 0660
```

Applications that need access should run as users in the `yllm` group.

See `docs/installation.md` for tarball installation and service templates for systemd, rc.d, and launchd.

## Model updates

Model files are never replaced in place.

A safe update flow is:

1. Download into a temporary directory.
2. Verify checksum.
3. Write metadata.
4. Move the version into place.
5. Activate by switching the model's `current` pointer.
6. Reload during an idle window.
7. Roll back if loading fails.

Qualified curated artifacts use this flow through `yllmctl models install`.
Downloads are retained as resumable partial files after interruption. A fully
verified staging file is removed after it has been copied into versioned model
storage.

Use `yllmctl models activate <model> -version <id>` to switch to an already installed version without reinstalling the model file. Activation requires the daemon to be idle.

Use `yllmctl models versions <model>` to inspect installed versions, active state, checksum metadata, and storage paths.

Use `yllmctl models update <catalog-variant>` to install the embedded catalog's
latest qualified revision for a model that is already installed. Add
`-activate` to switch to it immediately while the daemon is idle.

Use `yllmctl models updates` to compare all installed curated models with the
embedded catalog. `yllmctl models update -all` downloads every available newer
revision; add `-activate` to switch configured models to those revisions.

Use `yllmctl models delete <model> --version <id>` to remove an inactive version,
or omit `--version` to remove an entire unconfigured installation. Active
versions and rollback targets are protected. Removing a configured model
requires changing its assignment first.

Installed versions include:

```text
model.gguf
manifest.json
checksum.sha256
```

## Update policies

- `manual`, never check or install automatically.
- `notify`, check and report availability.
- `download`, download and verify but do not activate.
- `auto`, download, verify, and activate during a safe idle window.

The recommended default is `notify`.
