#!/usr/bin/env bash
# Runs the test suite and prints a per-package summary table.
# A JUnit XML report is written alongside it.
#
# Usage:
#   ./scripts/test.sh                      # progress + summary table
#   ./scripts/test.sh -d                   # also list every test
#   ./scripts/test.sh -f testname          # a specific gotestsum format
#   ./scripts/test.sh ./order-service/...  # one package
#
# Formats worth knowing:
#   dots      one character per test (the default)
#   testdox   one readable sentence per test, grouped by package
#   testname  PASS/FAIL with the Go test name
#   standard-verbose  plain `go test -v` output
#
# The JUnit report lands in .run/test-results/unit.xml, which is
# what CI servers and IDE test viewers read.

set -uo pipefail

cd "$(dirname "$0")/.."

FORMAT="dots"
PACKAGES=()

while [ $# -gt 0 ]; do
  case "$1" in
    -d|--detail) FORMAT="testdox"; shift ;;
    -f|--format) FORMAT="$2"; shift 2 ;;
    -h|--help)   sed -n '2,19p' "$0" | cut -c3-; exit 0 ;;
    *)           PACKAGES+=("$1"); shift ;;
  esac
done

if [ ${#PACKAGES[@]} -eq 0 ]; then
  PACKAGES=("./...")
fi

RESULTS_DIR=".run/test-results"
mkdir -p "$RESULTS_DIR"

# Prefer a gotestsum on PATH, then the one `go install` puts in
# GOBIN, and fall back to plain `go test` so the suite still
# runs on a machine that has neither.
if command -v gotestsum > /dev/null 2>&1; then
  RUNNER="gotestsum"
elif [ -x "$(go env GOPATH 2>/dev/null)/bin/gotestsum" ]; then
  RUNNER="$(go env GOPATH)/bin/gotestsum"
else
  echo "gotestsum not found — falling back to plain go test."
  echo "Install it with: go install gotest.tools/gotestsum@latest"
  echo
  exec go test -count=1 "${PACKAGES[@]}"
fi

"$RUNNER" \
  --format "$FORMAT" \
  --format-hide-empty-pkg \
  --junitfile "$RESULTS_DIR/unit.xml" \
  --jsonfile "$RESULTS_DIR/unit.json" \
  -- -count=1 "${PACKAGES[@]}"

STATUS=$?

# ---------------------------------------------------------
# Summary table, read back out of the JUnit report so it
# always agrees with what was actually recorded.
# ---------------------------------------------------------
if [ ! -f "$RESULTS_DIR/unit.xml" ]; then
  exit $STATUS
fi

echo
awk '
  # Pull one attribute out of an XML tag.
  function attr(line, name,   pattern, value) {
    pattern = name "=\"[^\"]*\""
    if (match(line, pattern) == 0) return ""
    value = substr(line, RSTART, RLENGTH)
    sub(name "=\"", "", value)
    sub("\"$", "", value)
    return value
  }

  BEGIN {
    green = "\033[32m"; red = "\033[31m"; dim = "\033[2m"
    bold  = "\033[1m";  reset = "\033[0m"

    printf "%s%-26s %7s %7s %7s %9s   %s%s\n", bold,
           "PACKAGE", "TESTS", "PASSED", "FAILED", "TIME", "STATUS", reset
    printf "%s%s%s\n", dim, "──────────────────────────────────────────────────────────────────────", reset
  }

  /<testsuites / {
    totalTests    = attr($0, "tests")
    totalFailures = attr($0, "failures")
    totalTime     = attr($0, "time")
  }

  /<testsuite / {
    name  = attr($0, "name")
    tests = attr($0, "tests") + 0
    fails = attr($0, "failures") + 0
    secs  = attr($0, "time") + 0

    # Trim the module prefix; the package name is the useful part.
    sub(".*/", "", name)

    # Packages with no test files are noise in a summary.
    if (tests == 0) next

    if (fails == 0) { colour = green; label = "PASS" }
    else            { colour = red;   label = "FAIL" }

    printf "%-26s %7d %7d %s%7d%s %8.2fs   %s%s%s\n",
           name, tests, tests - fails, (fails ? red : ""), fails,
           (fails ? reset : ""), secs, colour, label, reset
  }

  END {
    printf "%s%s%s\n", dim, "──────────────────────────────────────────────────────────────────────", reset

    if (totalFailures + 0 == 0) { colour = green; label = "ALL PASSING" }
    else                        { colour = red;   label = totalFailures " FAILING" }

    printf "%s%-26s %7d %7d %7d %8.2fs   %s%s%s\n", bold,
           "TOTAL", totalTests, totalTests - totalFailures, totalFailures,
           totalTime, colour, label, reset
  }
' "$RESULTS_DIR/unit.xml"

echo
echo "JUnit report: $RESULTS_DIR/unit.xml"

exit $STATUS
