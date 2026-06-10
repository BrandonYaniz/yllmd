# Client Protocol

`yllmd` uses JSON Lines over a Unix domain socket. Each message is one UTF-8 JSON object followed by a newline.

The daemon does not expose HTTP or HTTPS.

## Generate request

```json
{"type":"generate","id":"req-001","provider":"local","model":"balanced","stream":true,"input":{"kind":"messages","messages":[{"role":"system","content":"Answer clearly."},{"role":"user","content":"Summarize this."}]},"settings":{"temperature":0.2,"max_tokens":800},"queue":{"policy":"wait","timeout_ms":60000}}
```

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
{"type":"completed","id":"req-001","finish_reason":"stop","usage":{"input_tokens":100,"output_tokens":80}}
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
