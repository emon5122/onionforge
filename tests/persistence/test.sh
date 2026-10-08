#!/usr/bin/env bash
# Identity persistence across container recreation, target changes, prefix
# changes, service removal and live reload (acceptance criteria C and J).
source "$(dirname "$0")/../lib.sh"
setup_suite persistence
COMPOSE_FILE="$ROOT/tests/persistence/compose.yml"

write_config() { printf '%s\n' "$1" >"$ONIONFORGE_CONFIG_FILE"; }

write_config 'services:
  api:
    prefix: a
    target: http://backend:8080
  web:
    target: http://backend:8080'

log "First start"
compose up -d --quiet-pull >/dev/null 2>&1
check "healthy" wait_healthy 90
API1="$(onion api)"; WEB1="$(onion web)"
check "api has requested prefix" contains "${API1:0:1}" "a"

log "Destroy and recreate the container with the same volume"
compose rm -sf onionforge >/dev/null 2>&1
compose up -d onionforge >/dev/null 2>&1
check "healthy after recreate" wait_healthy 90
check "api address unchanged" test "$(onion api)" = "$API1"
check "web address unchanged" test "$(onion web)" = "$WEB1"
check "no new identity created" not_contains "$(compose logs onionforge)" "identity created: $(onion api)"

log "Graceful shutdown"
cid="$(gw_container)"
start=$(date +%s)
compose stop onionforge >/dev/null 2>&1
elapsed=$(( $(date +%s) - start ))
check "stops within Docker's timeout (${elapsed}s)" test "$elapsed" -lt 10
check "exit code 0" test "$(docker inspect -f '{{.State.ExitCode}}' "$cid")" = 0
logs="$(compose logs --no-color onionforge)"
check "Caddy stopped before Tor" bash -c '[[ "$(grep -n "Stopping Caddy" <<<"$1" | tail -1 | cut -d: -f1)" -lt "$(grep -n "Stopping Tor" <<<"$1" | tail -1 | cut -d: -f1)" ]]' _ "$logs"
compose start onionforge >/dev/null 2>&1
check "healthy after restart" wait_healthy 90
check "api address unchanged after restart" test "$(onion api)" = "$API1"

log "Change target with a live reload (SIGHUP)"
write_config 'services:
  api:
    prefix: a
    target: http://backend2:8080/v2
  web:
    target: http://backend:8080'
compose kill -s HUP onionforge >/dev/null 2>&1
check "reload completes" wait_log "Reload complete" 30
check "Tor not reloaded for a target change" wait_log "Onion services unchanged; Tor is not restarted" 5
check "api address unchanged after target change" test "$(onion api)" = "$API1"
check "new target in use" contains "$(onion_curl api /x)" "GET /v2/x HTTP/1.1"

log "Change prefix of an existing identity"
write_config 'services:
  api:
    prefix: zzz
    target: http://backend2:8080
  web:
    target: http://backend:8080'
compose restart onionforge >/dev/null 2>&1
check "healthy" wait_healthy 90
check "identity NOT replaced" test "$(onion api)" = "$API1"
check "prefix mismatch warning" wait_log "Service 'api' already has an existing Onion identity" 5
check "list shows mismatch" contains "$(docker exec "$(gw_container)" onionforge list)" "prefix mismatch"

log "Remove a service, then add it back"
write_config 'services:
  api:
    prefix: a
    target: http://backend:8080'
compose kill -s HUP onionforge >/dev/null 2>&1
check "removal logged" wait_log "Service 'web' removed from configuration; its identity is kept" 30
out="$(gw_curl -o /dev/null -w '%{http_code}' -H "Host: $WEB1" http://127.0.0.1:8080/)"
check "removed service no longer routed" test "$out" = 421
check "removed identity kept on disk" docker exec "$(gw_container)" test -s /var/lib/tor/web/hs_ed25519_secret_key
check "listed as inactive" contains "$(docker exec "$(gw_container)" onionforge list)" "inactive"
write_config 'services:
  api:
    prefix: a
    target: http://backend:8080
  web:
    target: http://backend:8080
  extra:
    target: http://backend:8080'
compose kill -s HUP onionforge >/dev/null 2>&1
check "services added via reload" wait_log "Service 'extra': identity created" 30
sleep 3
check "re-added service has its old address" test "$(onion web)" = "$WEB1"
check "re-added service routed" contains "$(onion_curl web /back)" "GET /back HTTP/1.1"
check "added service routed" contains "$(onion_curl extra /new)" "GET /new HTTP/1.1"
check "healthy after reloads" gw_healthy

log "Invalid reload keeps the running configuration"
write_config 'services:
  api:
    target: ftp://nope'
compose kill -s HUP onionforge >/dev/null 2>&1
check "reload rejected" wait_log "Reload failed, keeping previous configuration" 15
check "still serving" contains "$(onion_curl api /still)" "GET /still HTTP/1.1"

log "Secret keys never appear in logs or generated config"
secret_b64="$(docker exec "$(gw_container)" base64 -w0 /var/lib/tor/api/hs_ed25519_secret_key)"
check "secret key not in logs" not_contains "$(compose logs --no-color onionforge)" "${secret_b64:44:40}"
check "Caddyfile has no key material" not_contains "$(docker exec "$(gw_container)" cat /run/onionforge/Caddyfile)" "hs_ed25519"
check "key file mode 0600" test "$(docker exec "$(gw_container)" stat -c %a /var/lib/tor/api/hs_ed25519_secret_key)" = 600
check "identity dir mode 0700" test "$(docker exec "$(gw_container)" stat -c %a /var/lib/tor/api)" = 700
