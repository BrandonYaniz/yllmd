# Curated model catalog

`yllmctl` presents a curated model catalog in two levels:

1. A model family, such as Microsoft Phi, Google Gemma, or Qwen Coder.
2. One or more user-facing variants within that family.

List families and inspect their variants with:

```text
yllmctl models families
yllmctl models variants qwen-coder
```

Both commands accept `--json`. For the variants command, place it after the
family ID:

```text
yllmctl models variants qwen-coder --json
```

The variants view detects the current machine and shows estimated Q4_K_M
storage and memory requirements. If a hardware probe is unavailable, the CLI
reports the warning and displays compatibility as unknown rather than rejecting
the model. Estimates are replaced with measured requirements when an artifact
is qualified.

A variant represents a meaningful checkpoint or size choice. Quantization is
an artifact implementation detail; the normal workflow will install the
catalog's approved quantization rather than asking users to choose among GGUF
quantization names.

The embedded catalog is currently a draft qualification list. Entries marked
`planned` are not installable. An entry becomes `available` only after an exact
GGUF artifact, immutable upstream revision, SHA-256 checksum, runner version,
prompt format, and memory profile have been verified.

Each family records its publisher, country or countries of origin, license,
commercial-use status, and whether explicit license acceptance is required.
Country of origin means the home country of the organization primarily
responsible for the model; it does not describe training-data provenance or
the nationality of individual contributors.

## Generate a configuration

Create a deterministic configuration by assigning one or more catalog variants:

```text
yllmctl -mode user config create \
  -variant phi4-mini-instruct \
  -variant qwen25-coder-7b-instruct \
  -resident phi4-mini-instruct
```

The command writes `~/yllmd/config.yaml` in user mode and the platform's system
configuration path in daemon mode. Use `-output` to write somewhere else and
`-force` to replace an existing file.

Configured variants must occupy distinct routing roles. For example, two
`code.fast` variants may both be installed later, but only one may be assigned
to that role in a generated configuration. Installing artifacts and assigning
routing roles are deliberately separate operations.

Configuration generation currently warns that draft catalog variants are
planned. It does not make an unqualified artifact installable.

## Qualified artifacts

An available variant must identify one exact artifact with:

- GGUF format and approved quantization;
- upstream repository and immutable full commit revision;
- HTTPS download URL and safe filename;
- exact byte size and SHA-256 digest;
- minimum runner version; and
- prompt-template identifier.

The managed downloader supports resuming partial files. It limits downloads to
the catalog size, verifies the final size and SHA-256 digest, rejects unsafe
filenames and non-HTTPS sources, and only moves a verified file out of staging.
Interrupted `.part` files remain available for a later retry.

## Install qualified variants

Install one qualified variant directly:

```text
yllmctl models install qwen25-coder-7b-instruct
```

Select multiple variants from one family, or every qualified variant:

```text
yllmctl models install qwen-coder \
  -variant qwen25-coder-3b-instruct \
  -variant qwen25-coder-7b-instruct

yllmctl models install qwen-coder -all
```

The CLI validates family membership before contacting the daemon. The daemon
performs each download, emits progress, verifies the artifact, and copies it
into versioned model storage. Catalog installation does not activate a model by
default; pass `-activate` only after the variant is present in the daemon's
configuration.

For a family requiring explicit terms, pass `-accept-license` after reviewing
the license shown by `models variants`. Persistent versioned acceptance records
remain required before any custom-license catalog entry is qualified.

The legacy local-file installation path remains available:

```text
yllmctl models install local-name \
  -file model.gguf -version local-v1 -sha256 <digest>
```

## Delete installed models

Delete one inactive version or an entire unconfigured installation:

```text
yllmctl models delete qwen25-coder-7b-instruct --version <revision>
yllmctl models delete qwen25-coder-7b-instruct
```

Deletion prompts for confirmation. Pass `--yes` for an intentional
non-interactive operation. The daemon refuses to delete active versions,
rollback targets, or an entire model that still has a configuration assignment.
Change the assignment first, then retry. Successful deletion reports the bytes
reclaimed from versioned model storage.
