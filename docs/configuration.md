# Configuration

`yllmd` uses YAML configuration.

## Operating modes

`yllmd` and `yllmctl` support two mutually exclusive operating modes. User mode
is the default.

```text
yllmd -mode user
yllmctl -mode user models families

yllmd -mode daemon
yllmctl -mode daemon status
```

User mode keeps configuration, models, logs, and state beneath `~/yllmd`:

```text
~/yllmd/config.yaml
~/yllmd/models/
~/yllmd/logs/
~/yllmd/state/
```

Daemon mode uses platform-specific system paths:

| Platform | Configuration | Models |
| --- | --- | --- |
| Linux | `/etc/yllmd` | `/var/lib/yllmd/models` |
| FreeBSD | `/usr/local/etc/yllmd` | `/var/db/yllmd/models` |
| macOS Homebrew (Apple Silicon) | `/opt/homebrew/etc/yllmd` | `/opt/homebrew/var/lib/yllmd/models` |
| macOS Homebrew (Intel) | `/usr/local/etc/yllmd` | `/usr/local/var/lib/yllmd/models` |

Pass `-config` to `yllmd` or `-socket` to `yllmctl` to override the selected
mode's default path.

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
    model_type: llm
    tier: fast
    resident: true
```

`model_type` selects the workload family for the model. The current release accepts `llm` and `code`; future releases may add other families such as image, audio, or video. `tier` is the model level and should use `fast`, `balanced`, or `deep` for the standard local routing surface.

If only one local model is configured, all local model requests may use that model depending on `unavailable_tier_policy`.

`yllama-runner` is started with the configured `model_path`, `runtime.context_tokens`, and `runtime.threads` values. Generation settings such as `max_tokens`, `temperature`, and `top_p` are passed as runner startup flags for each effective resident session.

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
