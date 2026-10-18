#!/bin/bash
# reup QA orchestrator.
# Runs go vet + the full Go test suite (unit + PTY-driven TUI integration).
#
# Usage:
#   ./tests/run-qa.sh
set -u

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# --- preflight ---
if ! command -v go >/dev/null; then
  echo "error: missing dependency: go (https://go.dev/dl/ or brew install go)"
  exit 2
fi
if [[ ! -x "$REPO_ROOT/tests/mock-bin/codebuddy" ]]; then
  echo "error: missing executable: $REPO_ROOT/tests/mock-bin/codebuddy"
  exit 2
fi
fail=0

echo "════════════════════════════════════════════════════════"
echo " reup QA — go vet + go test (unit + TUI integration)"
echo "════════════════════════════════════════════════════════"

cd "$REPO_ROOT"

echo ""
echo "[1/2] go vet ./..."
echo "────────────────────────────────────────────────────────"
if go vet ./...; then
  echo "  ✓ vet clean"
else
  fail=1
fi

echo ""
echo "[2/2] go test ./..."
echo "────────────────────────────────────────────────────────"
if go test ./...; then
  echo "  ✓ all tests pass"
else
  fail=1
fi

echo ""
echo "════════════════════════════════════════════════════════"
if (( fail == 0 )); then
  echo " QA PASS"
else
  echo " QA FAIL"
fi
echo "════════════════════════════════════════════════════════"
exit $fail
