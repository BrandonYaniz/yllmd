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
