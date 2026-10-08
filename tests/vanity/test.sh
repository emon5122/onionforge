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
