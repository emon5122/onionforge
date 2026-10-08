#!/usr/bin/env bash
# Runs the OnionForge test suites.
#
#   tests/run.sh                 unit tests + all Docker suites (except tor)
#   tests/run.sh proxy vanity    selected suites
#   ONIONFORGE_TEST_TOR=1 tests/run.sh   also run the real-Tor end-to-end suite
set -euo pipefail
cd "$(dirname "$0")/.."
source tests/lib.sh

suites=("$@")
if [[ ${#suites[@]} -eq 0 ]]; then
  suites=(unit config multi-service proxy persistence vanity)
  [[ "${ONIONFORGE_TEST_TOR:-}" == "1" ]] && suites+=(tor)
fi

needs_image=0
for s in "${suites[@]}"; do [[ "$s" != unit ]] && needs_image=1; done
[[ $needs_image -eq 1 ]] && build_image
export ONIONFORGE_SKIP_BUILD=1

failed=()
for s in "${suites[@]}"; do
  log "Suite: $s"
  if [[ "$s" == unit ]]; then
    go test ./... || failed+=("$s")
  else
    "tests/$s/test.sh" || failed+=("$s")
  fi
  echo
done

if [[ ${#failed[@]} -gt 0 ]]; then
  echo "FAILED suites: ${failed[*]}"
  exit 1
fi
echo "All suites passed."
