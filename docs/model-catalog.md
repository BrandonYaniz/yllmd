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
