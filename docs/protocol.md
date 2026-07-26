# Client Protocol

`yllmd` uses JSON Lines over a Unix domain socket. Each message is one UTF-8
JSON object followed by a newline; the daemon does not expose HTTP.

## Generation targets

A routed request names a configuration-defined group and optional profile:

```json
{"type":"generate","id":"req-001","provider":"local","target":{"group":"writing","profile":"draft-pass1"},"input":{"kind":"messages","messages":[{"role":"user","content":"Draft the scene."}]},"settings":{"temperature":0.8,"max_tokens":4000,"output":{"format":"json","delivery":"stream"}},"queue":{"policy":"wait","timeout_ms":60000}}
```

Omitting the profile uses the group's `default_profile`. Omitting `target`
entirely uses `routing.default`. An exact request bypasses route fallback:

```json
{"type":"generate","id":"req-002","provider":"local","target":{"model":"fiction-primary"},"input":{"kind":"prompt","prompt":"Draft the scene."}}
```

Exactly one selection mode is allowed: `target.model`, `target.group` with an
optional `target.profile`, or no target. A profile without a group and any
combination of exact-model and routed selection are invalid.

## Generation settings and output

Supported sampling fields are `temperature`, `top_p`, `top_k`, `min_p`,
`presence_penalty`, `repeat_penalty`, `seed`, `max_tokens`, and `stop`. They are
applied per request without reloading a model.

`settings.output.format` is `json`, `text`, or `raw` (`raw` aliases `text`);
`settings.output.delivery` is `stream` or `complete`. Legacy
`settings.stream`, top-level `stream`, and `output_format` remain accepted when
the newer output object is omitted.

JSON mode begins with queue acceptance:

```json
{"type":"accepted","id":"req-001","queue_position":1}
```

The started event records both the resolved route and concrete model:

```json
{"type":"started","id":"req-001","provider":"local","target":{"group":"writing","profile":"draft-pass1"},"model":"fiction-primary"}
```

Operational fallback is explicit and reproducible:

```json
{"type":"started","id":"req-001","provider":"local","target":{"group":"writing","profile":"draft-pass1"},"model":"fiction-review","fallback":true,"fallback_from":"fiction-primary"}
```

Streaming emits `delta` events. Generation ends with `completed`, `cancelled`,
or `error`:

```json
{"type":"completed","id":"req-001","finish_reason":"stop","usage":{"input_tokens":100,"output_tokens":80,"total_tokens":180},"text":"Full generated text."}
```

## Discovery

`{"type":"models","id":"models-1","action":"list"}` returns concrete `models`,
route `groups`, and `default_target`. Each group includes its
`default_profile`; each profile includes its primary `model` and ordered
`fallbacks`. `action:"routes"` returns only routing discovery metadata.

Other model actions are `installed`, `licenses`, `versions`, `install`,
`download`, `update`, `delete`, `activate`, and `rollback`.

## Other requests

- `{"type":"cancel","id":"req-001"}` cancels a queued or active request.
- `health` and `status` report daemon state, loaded model, and queue depth.
- Provider values are `auto` and `local`; only local generation is currently
  implemented.
