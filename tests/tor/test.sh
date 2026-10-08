#!/usr/bin/env bash
# End-to-end through the real Tor network. Needs Internet access and takes a
# few minutes, so it only runs when ONIONFORGE_TEST_TOR=1 (or run directly).
source "$(dirname "$0")/../lib.sh"
setup_suite tor
COMPOSE_FILE="$ROOT/tests/tor/compose.yml"

printf '%s\n' 'services:
  app:
    prefix: of
    target: http://backend:8080
  example:
    target: https://example.com' >"$ONIONFORGE_CONFIG_FILE"

compose up -d --quiet-pull >/dev/null 2>&1
check "healthy" wait_healthy 120

# Tor client: the same image, running tor as a SOCKS client.
tor_curl() {
  docker run --rm --network "container:$(compose ps -q torclient)" "$CURL_IMAGE" \
    -sS --max-time 60 --socks5-hostname 127.0.0.1:9050 "$@"
}
fetch_via_tor() {
  local url="$1" want="$2" out
  for _ in $(seq 18); do
    out="$(tor_curl "$url" 2>&1 || true)"
    if grep -qF -- "$want" <<<"$out"; then return 0; fi
    sleep 10
  done
  echo "$out" >&2
  return 1
}

log "Fetching through Tor (descriptor publication can take a minute or two)"
check "Docker backend reachable via its onion address" fetch_via_tor "http://$(onion app)/via-tor" "GET /via-tor HTTP/1.1"
check "external HTTPS site reachable via its onion address" fetch_via_tor "http://$(onion example)/" "Example Domain"
