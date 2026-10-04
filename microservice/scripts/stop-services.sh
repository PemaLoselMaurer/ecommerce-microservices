#!/usr/bin/env bash
# Stops services started by start-services.sh. With no
# arguments it stops all of them; with names, only those — which
# is how Part D scenario 5 takes a single dependency down:
#
#   ./scripts/stop-services.sh product

set -uo pipefail

cd "$(dirname "$0")/.."

RUN_DIR=".run"
ALL=(product customer inventory payment notification order gateway)

SERVICES=("${@:-}")
if [ -z "${SERVICES[*]}" ]; then
  SERVICES=("${ALL[@]}")
fi

for name in "${SERVICES[@]}"; do
  pidfile="$RUN_DIR/$name.pid"
  if [ ! -f "$pidfile" ]; then
    echo "$name-service is not running"
    continue
  fi

  pid="$(cat "$pidfile")"
  if kill "$pid" 2>/dev/null; then
    echo "stopped $name-service (pid $pid)"
  else
    echo "$name-service (pid $pid) was already gone"
  fi
  rm -f "$pidfile"
done
