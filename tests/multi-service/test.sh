#!/usr/bin/env bash
# Several onion services, several upstreams, one gateway (criteria A, B, D, E).
source "$(dirname "$0")/../lib.sh"
setup_suite multi
COMPOSE_FILE="$ROOT/tests/multi-service/compose.yml"

printf '%s\n' 'services:
  api:
    prefix: api
    target: http://backend:8080
  web:
    prefix: web
    target: http://frontend:3000' >"$ONIONFORGE_CONFIG_FILE"

compose up -d --quiet-pull >/dev/null 2>&1
check "healthy" wait_healthy 120
check "Docker health status reaches healthy" bash -c 'for i in $(seq 60); do [[ "$(docker inspect -f "{{.State.Health.Status}}" "$1")" == healthy ]] && exit 0; sleep 1; done; exit 1' _ "$(gw_container)"
check "two distinct addresses" test "$(onion api)" != "$(onion web)"
check "api routes to backend" contains "$(onion_curl api /ping)" "GET /ping HTTP/1.1"
check "web routes to frontend" test "$(onion_curl web /)" = "frontend says hi"
check "startup banner lists services" wait_log "Onion:  http://$(onion web)" 5
check "ready banner" wait_log "OnionForge is ready" 5
check "backend ports not published" test -z "$(docker port "$(compose ps -q backend)")"
check "gateway ports not published" test -z "$(docker port "$(gw_container)")"
check "Caddy listens on loopback only" bash -c 'docker exec "$1" sh -c "cat /proc/net/tcp" | awk "\$4==\"0A\"{print \$2}" | grep -qx "0100007F:1F90"' _ "$(gw_container)"
procs="$(docker exec "$(gw_container)" sh -c 'for p in /proc/[0-9]*; do c=$(cat $p/comm 2>/dev/null) || continue; u=$(awk "/^Uid/{print \$2}" $p/status); echo "$c $u"; done')"
check "tor runs unprivileged" contains "$procs" "tor 10001"
check "caddy runs unprivileged" contains "$procs" "caddy 10001"
check "no SOCKS port open" bash -c '! docker exec "$1" sh -c "cat /proc/net/tcp" | awk "\$4==\"0A\"{print \$2}" | grep -q ":235A$"' _ "$(gw_container)"
