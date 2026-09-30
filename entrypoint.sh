#!/bin/sh
set -eu

: "${MODEL_PATH:?MODEL_PATH is required}"
: "${MMPROJ_PATH:?MMPROJ_PATH is required}"
: "${STARTUP_TIMEOUT:=900}"
: "${LLAMA_THREADS:=0}"

cd /models
sha256sum --check --strict manifest.sha256

set -- --model "$MODEL_PATH" --mmproj "$MMPROJ_PATH" --host 127.0.0.1 --port 8000 --no-mmproj-offload
if [ "$LLAMA_THREADS" -gt 0 ]; then
    set -- "$@" --threads "$LLAMA_THREADS"
fi

llama-server "$@" &
llama_pid=$!
go_pid=""

stop() {
    if [ -n "$go_pid" ]; then kill -TERM "$go_pid" 2>/dev/null || true; fi
    kill -TERM "$llama_pid" 2>/dev/null || true
    wait 2>/dev/null || true
}
trap stop INT TERM EXIT

started_at=$(date +%s)
until curl --fail --silent http://127.0.0.1:8000/health >/dev/null; do
    if ! kill -0 "$llama_pid" 2>/dev/null; then
        echo "llama-server exited before becoming ready" >&2
        exit 1
    fi
    if [ "$(($(date +%s) - started_at))" -ge "$STARTUP_TIMEOUT" ]; then
        echo "llama-server did not become ready within ${STARTUP_TIMEOUT}s" >&2
        exit 1
    fi
    sleep 2
done

lightonocr-mcp &
go_pid=$!
while kill -0 "$go_pid" 2>/dev/null && kill -0 "$llama_pid" 2>/dev/null; do sleep 1; done

if ! kill -0 "$llama_pid" 2>/dev/null; then
    echo "llama-server exited unexpectedly" >&2
    exit 1
fi
wait "$go_pid"
