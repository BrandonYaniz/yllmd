# yllmd

`yllmd` is a local-first LLM broker daemon. It exposes a Unix domain socket for local applications, manages request queueing, routes requests to a local model runner, and controls local model lifecycle.

The daemon is designed for systems where multiple local tools need access to a shared LLM service without each tool loading and managing its own model.

## Goals

- Provide one local interface for LLM requests.
- Use Unix domain sockets instead of TCP or HTTP for local clients.
- Support FIFO request queueing.
- Manage local model loading, unloading, cooldowns, updates, and rollback.
- Route requests to local models by workload type and level through one request shape.
- Keep provider credentials in daemon-owned configuration.
- Keep application-specific prompting outside the daemon.

## Non-goals

- `yllmd` is not a log analyzer.
- `yllmd` does not build prompts for applications.
- `yllmd` does not expose HTTP or HTTPS.
- `yllmd` does not provide a web UI.
- `yllmd` does not train or fine-tune models.

## Architecture

```text
client applications
  -> Unix domain socket
  -> yllmd
  -> FIFO scheduler
  -> local provider
  -> local runner
```

Local inference is handled by an external runner process, such as `yllama-runner`, over binary stdio frames.

## Status

Pre-release. The current implementation targets a local-only release surface. Versions use `YY.MM.DD.NN` for development and beta builds, and only versions tagged with `-Release` should be considered release builds. Remote provider adapters and automatic catalog refresh checks are planned but not part of the current release surface.

Implemented:

- Unix domain socket server using JSON Lines.
- FIFO request queueing and cancellation.
- Local runner process integration over binary stdio frames.
- Curated model discovery, verified download, update, inventory, activation, rollback, and deletion through `yllmctl`.
- Local model version activation through a `current` symlink.
- Model routing by `model_type` (`llm` or `code`) and level (`fast`, `balanced`, or `deep`).

Not yet implemented:

- OpenAI, Gemini, and Anthropic generation adapters.
- Automatic catalog refresh and update checks.
- Runtime configuration reload.
- Packaged installers.

## License

BSD 3-Clause License.
