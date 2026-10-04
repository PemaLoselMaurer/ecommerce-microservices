#!/usr/bin/env bash
# Builds every service and starts it in the background, with
# logs under example/.run/logs. Re-running the script restarts
# everything from a clean state.
#
# Environment overrides worth knowing:
#   PAYMENT_DELAY=0s      make orders succeed instead of timing out
#   INVENTORY_FLAKY=false stop ReserveStock failing 2 calls in 3
#   PRICING_URL=...       the calculate-order-price Worker
#   PRICING_API_TOKEN_FILE=...  file holding its bearer token
#   PRICING_TIMEOUT=3s    per-call budget for the Worker
#
# Usage:
#   ./scripts/start-services.sh                 # Lab 3 fault simulation on
#   PAYMENT_DELAY=0s INVENTORY_FLAKY=false ./scripts/start-services.sh
#   ./scripts/start-services.sh product customer gateway   # a subset

set -euo pipefail

cd "$(dirname "$0")/.."

# The serverless pricing function the Order Service calls. The
# URL is not a secret; the token is, so only the path to the
# file holding it is given here, and the file lives outside the
# repository (created in Lab 5, Part B).
export PRICING_URL="${PRICING_URL:-https://calculate-order-price.fritzlee-web303.workers.dev}"
export PRICING_API_TOKEN_FILE="${PRICING_API_TOKEN_FILE:-$HOME/.pricing-token}"

RUN_DIR=".run"
LOG_DIR="$RUN_DIR/logs"
BIN_DIR="$RUN_DIR/bin"
mkdir -p "$LOG_DIR" "$BIN_DIR"

ALL=(product customer inventory payment notification order gateway)
SERVICES=("${@:-}")
if [ -z "${SERVICES[*]}" ]; then
  SERVICES=("${ALL[@]}")
fi

# Gateway last: it is the only one that needs the others to
# already be listening for its health strip to look right.
for name in "${SERVICES[@]}"; do
  dir="${name}-service"
  if [ ! -d "$dir" ]; then
    echo "unknown service: $name" >&2
    exit 1
  fi

  # Stop a previous run of this service, if any.
  if [ -f "$RUN_DIR/$name.pid" ]; then
    kill "$(cat "$RUN_DIR/$name.pid")" 2>/dev/null || true
    rm -f "$RUN_DIR/$name.pid"
  fi

  go build -o "$BIN_DIR/$name" "./$dir"

  # setsid detaches the service from this script's session, so
  # it keeps running once the script (or the shell that ran it)
  # exits. The script is not a process group leader, so setsid
  # execs in place and $! is still the service's own PID.
  setsid "$BIN_DIR/$name" > "$LOG_DIR/$name.log" 2>&1 < /dev/null &
  echo $! > "$RUN_DIR/$name.pid"
  echo "started $dir (pid $!) -> $LOG_DIR/$name.log"
done

sleep 1
echo
echo "UI:   http://localhost:8081"
echo "Logs: tail -f $LOG_DIR/*.log"
echo "Stop: ./scripts/stop-services.sh"
