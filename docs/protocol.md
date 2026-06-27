# Client Protocol

`yllmd` uses JSON Lines over a Unix domain socket. Each message is one UTF-8 JSON object followed by a newline.

The daemon does not expose HTTP or HTTPS.

## Generate request

```json
{"type":"generate","id":"req-001","provider":"local","model":"balanced","input":{"kind":"messages","messages":[{"role":"system","content":"Answer clearly."},{"role":"user","content":"Summarize this."}]},"settings":{"temperature":0.2,"max_tokens":800,"output":{"format":"json","delivery":"stream"}},"queue":{"policy":"wait","timeout_ms":60000}}
```

Generate requests support two independent output options:

- `settings.output.format`: `json`, `text`, or `raw`. `raw` is an alias for `text`. Defaults to `json`.
- `settings.output.delivery`: `stream` or `complete`. Defaults to `stream`.

The legacy `settings.stream` and top-level `stream` booleans are still accepted when `settings.output` is omitted. The legacy top-level `output_format` field is also accepted as a compatibility alias, but new clients should use `settings.output`.

This creates four response modes:

- JSON stream: `{"output":{"format":"json","delivery":"stream"}}` returns JSON Lines events as generation progresses.
- JSON completed: `{"output":{"format":"json","delivery":"complete"}}` returns JSON Lines control events and a terminal `completed` event with full `text`.
- Text stream: `{"output":{"format":"text","delivery":"stream"}}` returns raw text chunks as they are produced.
- Text completed: `{"output":{"format":"text","delivery":"complete"}}` delays the response until generation finishes, then returns the raw output text.

Text responses are one-shot raw responses for generate requests. The daemon closes the connection after the terminal output or error line.

## Accepted response

```json
{"type":"accepted","id":"req-001","queue_position":1}
```

## Started response

```json
{"type":"started","id":"req-001","provider":"local","model":"balanced"}
```

## Delta response

```json
{"type":"delta","id":"req-001","text":"The issue appears to be"}
```

## Completed response

```json
{"type":"completed","id":"req-001","finish_reason":"stop","usage":{"input_tokens":100,"output_tokens":80},"text":"Full generated text."}
```

## Error response

```json
{"type":"error","id":"req-001","code":"model_unavailable","message":"Requested model is not available."}
```

## Cancel request

```json
{"type":"cancel","id":"req-001"}
```

## Health request

```json
{"type":"health","id":"health-001"}
```

## Health response

```json
{"type":"health","id":"health-001","status":"ok","loaded_model":"fast","queue_depth":0}
```

## Model names

Clients may request abstract model tiers:

- `fast`
- `balanced`
- `deep`

Clients may also request configured provider-specific aliases when enabled by configuration.

## Provider names

Supported provider values:

- `auto`
- `local`

For the current local-only release surface, only `local` is implemented. `auto` resolves to the configured default provider, which must be `local`.

The protocol reserves these provider names for future releases:

- `openai`
- `gemini`
- `anthropic`

## Model actions

The `models` request supports:

- `list`, list configured models.
- `versions`, list installed versions for one configured model.
- `install`, install a local GGUF file as a version.
- `activate`, switch `current` to an installed version while the daemon is idle.
- `rollback`, restore the previous activation while the daemon is idle.
