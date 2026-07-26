#!/bin/sh
set -eu

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
TMP_BASE=/tmp
if [ -d /private/tmp ]; then
	TMP_BASE=/private/tmp
fi
TMP_DIR=$(mktemp -d "$TMP_BASE/yllmd-smoke.XXXXXX")
SOCKET_PATH="$TMP_DIR/yllmd.sock"
CONFIG_PATH="$TMP_DIR/config.yaml"
DAEMON_LOG="$TMP_DIR/yllmd.log"

cleanup() {
	if [ "${DAEMON_PID:-}" != "" ]; then
		kill "$DAEMON_PID" 2>/dev/null || true
		wait "$DAEMON_PID" 2>/dev/null || true
	fi
	rm -rf "$TMP_DIR"
}
trap cleanup EXIT INT TERM

cat >"$CONFIG_PATH" <<EOF
server:
  socket_path: $SOCKET_PATH
  socket_mode: "0600"
  socket_group: ""

queue:
  policy: fifo
  max_depth: 8
  default_timeout: 30s

model_lifecycle:
  resident_model: fast
  idle_cooldown: 1m
  max_loaded_models: 1

paths:
  state_dir: $TMP_DIR/state
  model_dir: $TMP_DIR/state/models
  runtime_dir: $TMP_DIR/run
  log_dir: $TMP_DIR/log

updates:
  check_interval: 24h
  default_policy: notify

models:
  fast:
    catalog_id: smoke_fast
    backend:
      type: process
      command: /bin/false
      transport: stdio
    runtime:
      context_tokens: 1024
      threads: 1

remote_providers:
  openai:
    enabled: false
    api_key_env: OPENAI_API_KEY

routing:
  default:
    group: llm
    profile: fast
  unavailable_profile_policy: reject
  unavailable_model_policy: use_fallback
  default_provider: local
  groups:
    llm:
      default_profile: fast
      profiles:
        fast:
          model: fast
EOF

cd "$ROOT_DIR"
go run ./cmd/yllmd -config "$CONFIG_PATH" -fake-provider >"$DAEMON_LOG" 2>&1 &
DAEMON_PID=$!

i=0
while [ "$i" -lt 400 ]; do
	if [ -S "$SOCKET_PATH" ]; then
		break
	fi
	if ! kill -0 "$DAEMON_PID" 2>/dev/null; then
		echo "yllmd exited before socket was ready" >&2
		cat "$DAEMON_LOG" >&2
		exit 1
	fi
	i=$((i + 1))
	sleep 0.05
done

if [ ! -S "$SOCKET_PATH" ]; then
	echo "timed out waiting for socket $SOCKET_PATH" >&2
	cat "$DAEMON_LOG" >&2
	exit 1
fi

go run ./cmd/yllmctl -socket "$SOCKET_PATH" health | grep '"status": "ok"' >/dev/null
go run ./cmd/yllmctl -socket "$SOCKET_PATH" status | grep '"provider": "local"' >/dev/null
go run ./cmd/yllmctl -socket "$SOCKET_PATH" providers | grep '"provider": "local"' >/dev/null
MODEL_PATH="$TMP_DIR/smoke.gguf"
printf 'smoke model\n' >"$MODEL_PATH"
MODEL_SHA=$(shasum -a 256 "$MODEL_PATH" | awk '{print $1}')
go run ./cmd/yllmctl -socket "$SOCKET_PATH" models install fast -file "$MODEL_PATH" -version smoke-v1 -sha256 "$MODEL_SHA" -activate=false | grep '"type": "installed"' >/dev/null
go run ./cmd/yllmctl -socket "$SOCKET_PATH" models activate fast -version smoke-v1 | grep '"type": "activated"' >/dev/null
go run ./cmd/yllmctl -socket "$SOCKET_PATH" models versions fast | grep '"version": "smoke-v1"' >/dev/null
go run ./cmd/yllmctl -socket "$SOCKET_PATH" models list | grep '"active_version": "smoke-v1"' >/dev/null
go run ./cmd/yllmctl -socket "$SOCKET_PATH" models routes | grep '"default_profile": "fast"' >/dev/null
go run ./cmd/yllmctl -socket "$SOCKET_PATH" models updates | grep 'No curated catalog models are installed' >/dev/null
go run ./cmd/yllmctl -socket "$SOCKET_PATH" models update -all | grep 'All installed curated models are up to date' >/dev/null
go run ./cmd/yllmctl -socket "$SOCKET_PATH" generate -stream=false -prompt "release smoke" | grep 'fake local response: release smoke' >/dev/null
go run ./cmd/yllmctl -socket "$SOCKET_PATH" generate -model fast -stream=false -prompt "exact smoke" | grep 'fake local response: exact smoke' >/dev/null

echo "smoke ok"
