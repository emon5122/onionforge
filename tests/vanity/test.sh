#!/usr/bin/env bash
# Vanity prefixes (acceptance criterion H).
source "$(dirname "$0")/../lib.sh"
setup_suite vanity
COMPOSE_FILE="$ROOT/tests/persistence/compose.yml"

printf '%s\n' 'services:
  radio:
    prefix: radio
    target: http://backend:8080
  ab:
    prefix: ab
    target: http://backend:8080' >"$ONIONFORGE_CONFIG_FILE"

compose up -d --quiet-pull >/dev/null 2>&1
check "healthy" wait_healthy 180
check "5-char prefix honoured" contains "$(onion radio)" "radio"
check "address starts with prefix" test "$(onion radio | cut -c1-5)" = radio
check "2-char prefix honoured" test "$(onion ab | cut -c1-2)" = ab
check "v3 address format" bash -c '[[ "$1" =~ ^[a-z2-7]{55}d\.onion$ ]]' _ "$(onion radio)"
R="$(onion radio)"
compose rm -sf onionforge >/dev/null 2>&1
compose up -d onionforge >/dev/null 2>&1
check "healthy after recreate" wait_healthy 60
check "vanity identity persists" test "$(onion radio)" = "$R"
check "no regeneration" not_contains "$(compose logs --no-color onionforge)" "generating vanity onion identity with prefix 'radio'"

log "Background vanity searches"
compose down -v >/dev/null 2>&1
printf '%s\n' 'vanity:
  background: always
  threads: 2
services:
  web:
    target: http://backend:8080
  bg:
    prefix: [xyz, qq]
    target: http://backend:8080
  slow:
    prefix: zzzzzzzzzz
    target: http://backend:8080' >"$ONIONFORGE_CONFIG_FILE"
compose up -d >/dev/null 2>&1
check "healthy while searches run" wait_healthy 60
check "other services published immediately" docker exec "$(gw_container)" test -s /var/lib/tor/web/hostname
check "background search finished and published" wait_log "Service 'bg': identity created" 120
check "published without restarting Tor" wait_log "Onion services changed; reloading Tor" 30
sleep 3
B="$(onion bg)"
check "background address matches one of the prefixes" bash -c '[[ "$1" == xyz* || "$1" == qq* ]]' _ "$B"
check "background service routed" contains "$(onion_curl bg /bg)" "GET /bg HTTP/1.1"
check "speed estimate logged" wait_log "expected time" 30
out="$(docker exec "$(gw_container)" onionforge list)"
check "list shows running search with estimate" contains "$out" "searching for 'zzzzzzzzzz', running"
check "still healthy" gw_healthy

log "Reload cancels a search that is no longer wanted"
printf '%s\n' 'vanity:
  background: always
services:
  web:
    target: http://backend:8080
  bg:
    prefix: [xyz, qq]
    target: http://backend:8080
  slow:
    target: http://backend:8080' >"$ONIONFORGE_CONFIG_FILE"
compose kill -s HUP onionforge >/dev/null 2>&1
check "search cancelled" wait_log "Service 'slow': background vanity search cancelled" 30
check "service published with a random address instead" wait_log "Service 'slow': identity created" 30
check "background address unchanged" test "$(onion bg)" = "$B"
check "no generator left running" bash -c '! docker exec "$1" sh -c "cat /proc/[0-9]*/comm" | grep -q onion-vanity' _ "$(gw_container)"
