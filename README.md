# yllmd

`yllmd` is a local-first LLM broker daemon. It exposes a Unix domain socket for local applications, manages request queueing, routes requests to local or remote model providers, and controls local model lifecycle.

The daemon is designed for systems where multiple local tools need access to a shared LLM service without each tool loading and managing its own model.

## Goals

- Provide one local interface for LLM requests.
- Use Unix domain sockets instead of TCP or HTTP for local clients.
- Support FIFO request queueing.
- Manage local model loading, unloading, cooldowns, updates, and rollback.
- Route requests to local models, OpenAI, Gemini, or Anthropic through one request shape.
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
  -> provider router
  -> local runner or remote provider
```

Local inference is handled by an external runner process, such as `yllama-runner`, over stdio JSON Lines. Remote providers are handled through provider adapters.

## Status

Early development.

## License

BSD 3-Clause License.
