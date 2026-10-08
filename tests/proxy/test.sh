#!/usr/bin/env bash
# HTTP semantics through the gateway: methods, paths, headers, streaming,
# WebSockets, redirects and open-proxy prevention.
source "$(dirname "$0")/../lib.sh"
setup_suite proxy
COMPOSE_FILE="$ROOT/tests/proxy/compose.yml"

cat >"$ONIONFORGE_CONFIG_FILE" <<'YAML'
services:
  app:
    target: http://backend:8080
  sub:
    target: http://backend:8080/base/
  redir:
    target: http://redirector:80
    rewrite_redirects: true
  redir-raw:
    target: http://redirector:80
YAML

log "Starting gateway"
compose up -d --quiet-pull >/dev/null 2>&1
check "gateway becomes healthy" wait_healthy 90

for m in GET POST PUT PATCH DELETE OPTIONS; do
  out="$(onion_curl app /m -X "$m" -d 'x=1' 2>&1)"
  check "$m is proxied" contains "$out" "$m /m HTTP/1.1"
done
check "HEAD is proxied" contains "$(onion_curl app /h -I)" "HTTP/1.1 200"

out="$(onion_curl app '/a/b%20c/?q=1&r=two' -H 'Cookie: sid=abc')"
check "path and query preserved" contains "$out" "GET /a/b%20c/?q=1&r=two HTTP/1.1"
check "cookies forwarded" contains "$out" "Cookie: sid=abc"
check "onion Host preserved for Docker targets" contains "$out" "Host: $(onion app)"
check "X-Forwarded-Host set" contains "$out" "X-Forwarded-Host: $(onion app)"

out="$(onion_curl sub '/x/y?z=1')"
check "base path prepended once" contains "$out" "GET /base/x/y?z=1 HTTP/1.1"
check "base path root" contains "$(onion_curl sub /)" "GET /base/ HTTP/1.1"

head -c 1048576 /dev/urandom >"$WORK/upload.bin"
out="$(docker run --rm --network "container:$(gw_container)" -v "$WORK/upload.bin:/upload.bin:ro" "$CURL_IMAGE" \
  -sS --max-time 30 -H "Host: $(onion app)" --data-binary @/upload.bin -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/upload)"
check "1 MiB upload" test "$out" = 200

out="$(onion_curl app / -H 'Transfer-Encoding: chunked' --data-binary 'chunked-body' )"
check "chunked request body" contains "$out" "chunked-body"

out="$(onion_curl app /.sse -N --max-time 3 2>/dev/null || true)"
check "server-sent events stream" contains "$out" "event: server"

out="$(timeout 10 docker run --rm --network "container:$(gw_container)" "$CURL_IMAGE" -sS --max-time 3 \
  -H "Host: $(onion app)" ws://127.0.0.1:8080/ 2>/dev/null || true)"
check "WebSocket upgrade and message" contains "$out" "Request served by"

out="$(onion_curl redir /old -D - -o /dev/null)"
check "redirect Location rewritten to onion" contains "$out" "Location: http://$(onion redir)/landing?x=1"
out="$(onion_curl redir-raw /old -D - -o /dev/null)"
check "redirect untouched without rewrite_redirects" contains "$out" "Location: http://redirector/landing?x=1"

log "Open proxy prevention"
out="$(gw_curl -o /dev/null -w '%{http_code}' -H 'Host: example.com' http://127.0.0.1:8080/)"
check "unknown Host is refused (421)" test "$out" = 421
out="$(gw_curl -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/)"
check "bare IP Host is refused (421)" test "$out" = 421
out="$(gw_curl -o /dev/null -w '%{http_code}' --request-target 'http://example.com/' -H "Host: $(onion app)" http://127.0.0.1:8080/)"
check "absolute-form request to another host is refused" test "$out" = 421
out="$(onion_curl app '/proxy?url=https://example.com')"
check "URL parameters cannot select an upstream" contains "$out" "Request served by"
out="$(gw_curl -o /dev/null -w '%{http_code}' -X CONNECT --request-target 'example.com:443' -H "Host: $(onion app)" http://127.0.0.1:8080 || true)"
check "CONNECT is not tunnelled" test "$out" != 200

log "Upstream down does not make the gateway unhealthy"
compose stop backend >/dev/null 2>&1
out="$(onion_curl app / -o /dev/null -w '%{http_code}')"
check "502 while upstream is down" test "$out" = 502
check "gateway still healthy" gw_healthy
compose start backend >/dev/null 2>&1
