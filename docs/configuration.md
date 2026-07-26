# Configuration

The configuration separates concrete runnable models from client-facing routes. See
[`config.example.yaml`](../config.example.yaml) for a complete daemon-mode
configuration.

## Operating modes and generation

User mode is the default and stores configuration, models, logs, and state
beneath `~/yllmd`. Daemon mode uses platform-specific system paths.

```text
yllmd -mode user
yllmctl -mode user config create -variant <catalog-id>

yllmd -mode daemon
yllmctl -mode daemon status
```

`yllmctl config create` writes a configuration and refuses to overwrite an
existing file unless `-force` is supplied. It creates routes only for the
selected catalog variants. Catalog `model_type` and `level` values are used as
generator suggestions; they are not restrictions in the configuration schema.

## Concrete models

```yaml
models:
  fiction-primary:
    catalog_id: fiction_model_primary
    aliases: [fiction]
    enabled: true
    backend:
      type: process
      command: /usr/local/libexec/yllama-runner
      transport: stdio
    runtime:
      context_tokens: 32768
      threads: 8
      gpu_layers: -1
```

A model has a stable name, a `catalog_id` or `model_path`, runner settings,
optional aliases, and an optional administrative `enabled` switch. It does not
have an inherent workload type or performance tier. Identifiers are lowercase,
1–64 characters, begin with a letter or digit, and may contain `.`, `_`, and
`-`.

`yllama-runner` is started with the configured model path and runtime settings.
Use `gpu_layers: 0` for CPU-only execution, a positive number for a fixed
offload count, or `-1` for automatic full offload.

## Routing groups and profiles

```yaml
routing:
  default:
    group: llm
    profile: balanced
  unavailable_profile_policy: reject
  unavailable_model_policy: use_fallback
  groups:
    llm:
      default_profile: balanced
      profiles:
        balanced:
          model: general-balanced
          fallbacks: [general-fast]
    writing:
      default_profile: structure
      profiles:
        structure: {model: planner}
        draft-pass1: {model: fiction-primary}
        draft-pass2: {model: fiction-primary}
        continuity:
          model: fiction-review
          fallbacks: [planner]
```

Group and profile names are opaque configuration-defined identifiers. The
standard generated configuration uses groups such as `llm` and `code` and
profiles such as `fast`, `balanced`, and `deep`, but these names are neither
reserved nor required. Several profiles and groups may resolve to the same
concrete model.

Fallbacks are tried in order only for operational unavailability, including a
disabled model or runner startup failure. They are not used for content
quality, schema failures, generation timeouts, or a `length` finish reason.
Exact-model requests never use route fallbacks.

The global default is used when a request has no target. A group-only request
uses that group's `default_profile`; it is rejected if none exists.

## Queue, lifecycle, and sampling

The queue remains FIFO. `max_loaded_models` must be `1`. `resident_model` names
the model restored after idle cooldown and may be omitted to use the globally
default route.

Sampling settings remain per request and do not reload the resident model.
Routing profiles do not contain sampling defaults.

Duration values such as `10s`, `15m`, and `1h30m` use Go duration syntax.

## Remote providers

Remote generation is reserved for future releases. If
`routing.default_provider` is present, it must currently be `local`.
