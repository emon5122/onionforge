#!/usr/bin/env bash
# Shared helpers for OnionForge integration tests.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export ONIONFORGE_IMAGE="${ONIONFORGE_IMAGE:-emon5122/onionforge:test}"
CURL_IMAGE="${CURL_IMAGE:-curlimages/curl:8.16.0}"

PASSED=0
FAILED=0

log()  { printf '\033[1m==> %s\033[0m\n' "$*"; }
pass() { PASSED=$((PASSED + 1)); printf '  \033[32mPASS\033[0m %s\n' "$*"; }
fail() { FAILED=$((FAILED + 1)); printf '  \033[31mFAIL\033[0m %s\n' "$*"; }

# check DESCRIPTION COMMAND... — records pass/fail without aborting.
check() {
  local desc="$1"; shift
  if "$@"; then pass "$desc"; else fail "$desc"; fi
}

contains() { grep -qF -- "$2" <<<"$1"; }
not_contains() { ! grep -qF -- "$2" <<<"$1"; }

build_image() {
  if [[ "${ONIONFORGE_SKIP_BUILD:-}" == "1" ]]; then return; fi
  log "Building $ONIONFORGE_IMAGE"
  docker build -q -t "$ONIONFORGE_IMAGE" "$ROOT" >/dev/null
}

# setup_suite NAME — creates a work dir and compose project for a suite.
setup_suite() {
  SUITE="$1"
  WORK="$(mktemp -d)"
  export COMPOSE_PROJECT_NAME="oftest-${SUITE}-$$"
  export ONIONFORGE_CONFIG_FILE="$WORK/onionforge.yml"
  trap teardown_suite EXIT
}

teardown_suite() {
  if [[ "${FAILED}" -gt 0 && -n "${COMPOSE_FILE:-}" ]]; then
    echo "---- onionforge logs (last 60 lines) ----"
    compose logs --no-color --tail 60 onionforge 2>/dev/null || true
  fi
  [[ -n "${COMPOSE_FILE:-}" ]] && compose down -v --remove-orphans >/dev/null 2>&1 || true
  rm -rf "$WORK"
  echo
  echo "$SUITE: $PASSED passed, $FAILED failed"
  [[ "$FAILED" -eq 0 ]]
}

compose() { docker compose -f "$COMPOSE_FILE" "$@"; }

gw_container() { compose ps -q onionforge; }

# wait_healthy [TIMEOUT] — waits for the Docker health check to pass.
wait_healthy() {
  local timeout="${1:-90}" cid status
  cid="$(gw_container)"
  for _ in $(seq "$timeout"); do
    status="$(docker inspect -f '{{.State.Health.Status}}' "$cid" 2>/dev/null || echo missing)"
    [[ "$status" == healthy ]] && return 0
    # Faster than the 30s Docker interval: ask directly.
    docker exec "$cid" onionforge healthcheck >/dev/null 2>&1 && return 0
    sleep 1
  done
  echo "gateway did not become healthy (status: $status)" >&2
  return 1
}

# wait_log PATTERN [TIMEOUT]
wait_log() {
  local pattern="$1" timeout="${2:-60}"
  for _ in $(seq "$timeout"); do
    # Capture first: "producer | grep -q" fails under pipefail when grep
    # exits early and the producer gets SIGPIPE.
    contains "$(compose logs --no-color onionforge 2>/dev/null)" "$pattern" && return 0
    sleep 1
  done
  return 1
}

onion() { docker exec "$(gw_container)" cat "/var/lib/tor/$1/hostname"; }

# gw_curl ARGS... — curl from inside the gateway's network namespace, hitting
# the local Caddy listener exactly as Tor does.
gw_curl() {
  docker run --rm --network "container:$(gw_container)" "$CURL_IMAGE" -sS --max-time 15 "$@"
}

# onion_curl SERVICE PATH [ARGS...]
onion_curl() {
  local svc="$1" path="$2"; shift 2
  gw_curl -H "Host: $(onion "$svc")" "$@" "http://127.0.0.1:8080$path"
}

gw_healthy() { docker exec "$(gw_container)" onionforge healthcheck >/dev/null 2>&1; }
