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

## Update policies

- `manual`, never check or install automatically.
- `notify`, check and report availability.
- `download`, download and verify but do not activate.
- `auto`, download, verify, and activate during a safe idle window.

The recommended default is `notify`.
