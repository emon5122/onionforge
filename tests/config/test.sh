#!/usr/bin/env bash
# Configuration validation through the real image: invalid files are rejected
# with exit code 2 before anything is started.
source "$(dirname "$0")/../lib.sh"
setup_suite config
DIR="$ROOT/tests/config"

validate() { docker run --rm -v "$1:/etc/onionforge/onionforge.yml:ro" "$ONIONFORGE_IMAGE" validate 2>&1; }

for f in "$DIR"/valid/*.yml; do
  check "valid: $(basename "$f")" validate "$f" >/dev/null
done

for f in "$DIR"/invalid/*.yml; do
  set +e; out="$(validate "$f")"; rc=$?; set -e
  if [[ $rc -eq 2 && "$out" == *ERROR:* ]]; then pass "invalid: $(basename "$f")"; else fail "invalid: $(basename "$f") (rc=$rc) $out"; fi
done

out="$(validate "$DIR/invalid/ftp-target.yml" || true)"
check "error message names service and target" contains "$out" "Service 'radiolens' has invalid target:"
check "error message lists supported schemes" contains "$out" "https://"

log "run refuses an invalid configuration before starting Tor"
set +e
out="$(docker run --rm -v "$DIR/invalid/ftp-target.yml:/etc/onionforge/onionforge.yml:ro" "$ONIONFORGE_IMAGE" 2>&1)"; rc=$?
set -e
check "exit code 2" test "$rc" = 2
check "Tor never started" not_contains "$out" "[tor]"

set +e
out="$(docker run --rm "$ONIONFORGE_IMAGE" 2>&1)"; rc=$?
set -e
check "missing config is a clear error" contains "$out" "not found: mount your onionforge.yml"
check "missing config exits non-zero" test "$rc" != 0

log "Corrupt identity is never replaced"
vol="oftest-config-$$"
docker volume create "$vol" >/dev/null
docker run --rm -v "$vol:/var/lib/tor" --entrypoint sh "$ONIONFORGE_IMAGE" -c 'mkdir -p /var/lib/tor/backend && echo old.onion > /var/lib/tor/backend/hostname'
set +e
out="$(docker run --rm -v "$vol:/var/lib/tor" -v "$DIR/valid/single.yml:/etc/onionforge/onionforge.yml:ro" "$ONIONFORGE_IMAGE" 2>&1)"; rc=$?
set -e
check "partial identity is refused" contains "$out" "refusing to create a new identity"
check "and the gateway does not start" test "$rc" != 0
docker volume rm -f "$vol" >/dev/null
