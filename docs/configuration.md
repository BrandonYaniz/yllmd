# Configuration

`yllmd` uses YAML configuration.

## Duration values

Duration values use Go-style duration strings:

- `10s`
- `5m`
- `15m`
- `1h`
- `1h30m`

The idle cooldown is measured from the point when the queue is empty and no request is active. The timer resets whenever new traffic arrives.

## Socket

```yaml
server:
  socket_path: /var/run/yllmd/yllmd.sock
  socket_mode: "0660"
  socket_group: yllm
```

Access is controlled by filesystem permissions.

## Queue

```yaml
queue:
  policy: fifo
  max_depth: 128
  default_timeout: 2m
```

Requests are processed first in, first out. Future versions may add priority queues.

## Local models

```yaml
local_models:
  fast:
    catalog_id: qwen2_5_1_5b_instruct_q4
    tier: fast
    resident: true
```

If only one local model is configured, all local model requests may use that model depending on `unavailable_tier_policy`.

## Remote providers

Remote provider configuration is reserved for future releases. Remote generation is not implemented in the current local-only release surface, and `routing.default_provider` must be `local`.

When remote providers are implemented, credentials should be supplied through environment variables or protected files.

```yaml
remote_providers:
  openai:
    enabled: false
    api_key_env: OPENAI_API_KEY
```

Remote providers are disabled by default.
