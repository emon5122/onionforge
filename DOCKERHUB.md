# OnionForge

**Publish any HTTP service as a persistent Tor Onion Service — without changing the application.**

OnionForge is a Docker-native gateway that gives your existing containers persistent **Tor v3 `.onion` addresses**. One container runs Tor and [Caddy](https://caddyserver.com) and routes each onion hostname to an HTTP/HTTPS upstream on your Docker network, or to an external site.

Your applications don't install Tor, change their Dockerfile or code, publish ports, or know Tor exists.

- **Source & full documentation:** https://github.com/emon5122/onionforge
- **Issues:** https://github.com/emon5122/onionforge/issues
- **License:** MIT

---

## Quick start

`docker-compose.yml`:

```yaml
services:
  backend:
    image: my-backend          # your existing app, unchanged
    expose:
      - "8000"

  onionforge:
    image: emon5122/onionforge:latest
    restart: unless-stopped
    volumes:
      - onionforge-data:/var/lib/tor   # onion identities: keep this volume!
      - ./onionforge.yml:/etc/onionforge/onionforge.yml:ro

volumes:
  onionforge-data:
```

`onionforge.yml`:

```yaml
services:
  backend:
    prefix: myapp                 # optional vanity prefix
    target: http://backend:8000
```

```bash
docker compose up -d
docker compose logs onionforge
```

```
Services:

  backend
    Onion:  http://myappq4x…7yd.onion
    Target: http://backend:8000

==================================================
 OnionForge is ready
==================================================
```

Open the address in [Tor Browser](https://www.torproject.org/download/). No `ports:` are needed anywhere.

---

## Features

- **Many services, one container:** one Tor process and one Caddy route any number of onion addresses to any number of upstreams.
- **Persistent identities:** each address is tied to the service name and survives restarts, recreation and image upgrades. Changing a target keeps the address.
- **Vanity prefixes:** generated once with the bundled [`onion-vanity-address`](https://github.com/offset/onion-vanity-address). You can give several alternative prefixes. Long searches (hours to days) run as one combined background search for all waiting services, at low priority and with a live time estimate. Each service is published automatically when its key is found.
- **Docker-native:** targets like `http://backend:8000` resolve through Docker DNS, and backends need no published ports.
- **HTTPS and external upstreams:** correct SNI, certificate verification on by default, and per-service private CAs.
- **Full HTTP:** WebSockets, Server-Sent Events, streaming, uploads, cookies, and optional `Location` redirect rewriting.
- **Safe by default:** no open proxy, an SSRF policy for private, loopback and link-local targets, a loopback-only listener, and Tor and Caddy running unprivileged.
- **Operations:** config validation, Docker health check, graceful shutdown, and live reload on `SIGHUP` without restarting Tor.

## Architecture

```
              ┌───────────── OnionForge container ─────────────┐
 api….onion ──┤                                                ├──▶ backend:8000
 web….onion ──┤   Tor ──▶ Caddy  (127.0.0.1:8080, by Host)     ├──▶ frontend:3000
 ext….onion ──┤                                                ├──▶ https://example.com
              └────────────────────────────────────────────────┘
```

## Configuration

```yaml
services:
  api:
    prefix: api                      # optional, a-z and 2-7, max 10 chars; or a list [api, web]
    target: http://backend:8000      # http:// or https://, optional base path
  company:
    target: https://company.example
    rewrite_redirects: true          # rewrite Location headers to the onion
  internal:
    target: https://backend:8443
    tls:
      ca_file: /etc/onionforge/ca.pem

vanity:
  background: auto                   # long prefixes don't block startup
  threads: 0                         # CPU threads for the search (0 = all)

security:                            # all false by default
  allow_private_targets: false
  allow_loopback_targets: false
  allow_link_local_targets: false
```

See the full [configuration reference](https://github.com/emon5122/onionforge#onionforgeyml-reference).

## Image details

| | |
|---|---|
| Base | `debian:trixie-slim` |
| Contents | Tor (Debian package), Caddy 2 (official binary), `onion-vanity-address`, `onionforge` |
| Entrypoint | `onionforge run` |
| Volume | `/var/lib/tor`: onion identities and Tor state (**back it up like credentials**) |
| Config | `/etc/onionforge/onionforge.yml` |
| Exposed ports | none: the services are reachable only through Tor |
| Runs as | starts as root to fix volume ownership, then drops to `onionforge` (uid 10001) |
| Health check | built in (`onionforge healthcheck`) |

## Useful commands

```bash
docker compose exec onionforge onionforge list        # addresses and status
docker compose kill -s HUP onionforge                 # reload onionforge.yml
docker run --rm -v ./onionforge.yml:/etc/onionforge/onionforge.yml:ro \
  emon5122/onionforge validate                        # validate a config
```

## Tags

- `latest`: the most recent release
- `X.Y.Z`, `X.Y`: specific releases ([changelog](https://github.com/emon5122/onionforge/releases))

---

Backups, troubleshooting, security model and more: **https://github.com/emon5122/onionforge**
