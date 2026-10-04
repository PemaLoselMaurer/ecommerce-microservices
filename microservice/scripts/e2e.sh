#!/usr/bin/env bash
# Runs the browser end-to-end suite.
#
# Node, Playwright and Chromium are installed on the Windows
# side; the services run here in WSL. This script bridges the
# two so the whole suite can be started from the same terminal
# as everything else.
#
# Usage:
#   ./scripts/e2e.sh                  # all 31 tests, headless
#   ./scripts/e2e.sh --headed         # watch it in a real browser
#   ./scripts/e2e.sh --ui             # step through interactively
#   ./scripts/e2e.sh tests/03-failures.spec.js
#   ./scripts/e2e.sh --report         # open the last HTML report
#
# The services must already be running:
#   PAYMENT_DELAY=0s INVENTORY_FLAKY=false ./scripts/start-services.sh

set -uo pipefail

cd "$(dirname "$0")/.."

PROJECT_LINUX="$(pwd)/e2e"

# Z: is a drive letter mapped to this WSL filesystem. npm
# refuses a UNC path as its working directory, so the Windows
# side needs a lettered path to run in.
DRIVE="${E2E_DRIVE:-Z:}"
PROJECT_WINDOWS="${DRIVE}$(echo "$PROJECT_LINUX" | tr '/' '\\')"

CMD="/mnt/c/Windows/System32/cmd.exe"

if [ ! -x "$CMD" ]; then
  echo "cmd.exe not found — this script needs WSL's Windows interop." >&2
  exit 1
fi

# Check the drive is mapped before trying to use it, so a
# missing mapping gives a useful message rather than a cryptic
# one from npm.
if ! "$CMD" /c "if exist $PROJECT_WINDOWS\\package.json (exit 0) else (exit 1)" > /dev/null 2>&1; then
  echo "Cannot reach the tests at $PROJECT_WINDOWS" >&2
  echo >&2
  echo "The $DRIVE drive is probably not mapped. In a Windows terminal, run:" >&2
  echo "    net use $DRIVE \\\\wsl.localhost\\Ubuntu /persistent:yes" >&2
  exit 1
fi

# --report just opens the last run rather than starting a new one.
if [ "${1:-}" = "--report" ]; then
  exec "$CMD" /c "cd /d $PROJECT_WINDOWS && npx playwright show-report report"
fi

echo "Running the end-to-end suite against http://localhost:8081"
echo

"$CMD" /c "cd /d $PROJECT_WINDOWS && npx playwright test $*"
STATUS=$?

echo
if [ $STATUS -eq 0 ]; then
  echo "All end-to-end tests passed."
else
  echo "Some end-to-end tests failed."
fi
echo "HTML report: ./scripts/e2e.sh --report"

exit $STATUS
