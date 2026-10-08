# OnionForge

> **Publish any HTTP service as a persistent Tor Onion Service — without changing the application.**

OnionForge is a Docker-native, multi-service Tor reverse-proxy gateway. It manages persistent Tor v3 onion identities and uses [Caddy](https://caddyserver.com) to route each onion hostname to an existing HTTP/HTTPS service on your Docker network or to an external upstream.

Your applications don't need to install Tor, change their Dockerfile or code, publish ports, or know Tor exists.

```yaml
services:
  api:
    prefix: api
    target: http://backend:8000
```

```
api3k2…xyd.onion  →  backend:8000
```

---

## Contents

1. [Quick start](#quick-start)
2. [Architecture](#architecture)
3. [Docker Compose deployment](#docker-compose-deployment)
4. [`onionforge.yml` reference](#onionforgeyml-reference)
5. [Docker networking](#docker-networking)
6. [Vanity prefixes](#vanity-prefixes)
7. [Persistence and identities](#persistence-and-identities)
8. [External upstreams](#external-upstreams)
9. [HTTPS upstreams](#https-upstreams)
10. [WebSockets, streaming and HTTP features](#websockets-streaming-and-http-features)
11. [Security](#security)
12. [Reloading and updating](#reloading-and-updating)
13. [Commands](#commands)
14. [Troubleshooting](#troubleshooting)
15. [Backup and restore](#backup-and-restore)
16. [Development and tests](#development-and-tests)

---

## Quick start

`docker-compose.yml`:

```yaml
services:
  backend:
    image: my-backend
    expose:
      - "8000"

  onionforge:
    image: emon5122/onionforge:latest
    restart: unless-stopped
    volumes:
      - onionforge-data:/var/lib/tor
      - ./onionforge.yml:/etc/onionforge/onionforge.yml:ro

volumes:
  onionforge-data:
```

`onionforge.yml`:

```yaml
services:
  backend:
    prefix: myapp
    target: http://backend:8000
```

Start it:

```bash
docker compose up -d
docker compose logs onionforge
```

The logs show your address:

```
==================================================
 OnionForge v0.1.0
==================================================
Configuration: /etc/onionforge/onionforge.yml

Services:

  backend
    Onion:  http://myappq4x…7yd.onion
    Target: http://backend:8000

==================================================
 OnionForge is ready
==================================================
```

Open the address in [Tor Browser](https://www.torproject.org/download/). The first visit can take a minute or two while Tor publishes the service descriptor. That's the whole workflow.

---

## Architecture

```text
                         TOR NETWORK
                              │
              ┌───────────────┼────────────────┐
              ▼               ▼                ▼
          api….onion      web….onion      admin….onion
              └───────────────┼────────────────┘
                              ▼
                ┌───────────────────────────┐
                │ OnionForge container      │
                │                           │
                │  Tor (one process,        │
                │  one HiddenServiceDir     │
                │  per service)             │
                │        │                  │
                │        ▼ 127.0.0.1:8080   │
                │  Caddy (routes by Host)   │
                └────────┬──────────────────┘
                         │ Docker network
          ┌──────────────┼──────────────┐
          ▼              ▼              ▼
    backend:8000   frontend:3000   admin:8080
```

Responsibilities stay separate:

| Component | Job |
|---|---|
| **Tor** | Onion identities, onion networking, publishing the services |
| **Caddy** | HTTP routing, reverse proxying, WebSockets, upstream TLS, timeouts |
| **Docker** | Service networking and DNS |
| **`onionforge`** | Small Go control binary: validates config, creates identities, generates `torrc` and `Caddyfile`, supervises Tor and Caddy, health checks, reloads, graceful shutdown |

All onion services forward to the same loopback-only Caddy listener (`127.0.0.1:8080`). Caddy picks the upstream from the `Host` header, which is the `.onion` hostname. Requests for any other host are refused with `421`.

**Startup sequence:** read and validate `onionforge.yml` → fix volume ownership and drop root → load or create identities → generate and verify `torrc` → start Tor → wait for Tor to write each `hostname` file and check it against the key → generate and validate the `Caddyfile` → start Caddy → print addresses → ready.

**State.** `onionforge.yml` is the source of truth. The generated files in `/run/onionforge` (`torrc`, `Caddyfile`, `state.json`, Caddy's admin socket) are disposable and rebuilt on every start. The only persistent state is `/var/lib/tor`.

The image contains Tor (Debian package), Caddy (the official binary), [`onion-vanity-address`](https://github.com/offset/onion-vanity-address) (compiled in at a pinned commit) and the `onionforge` binary. There is no application runtime and nothing is downloaded at startup.

---

## Docker Compose deployment

A complete deployment next to an existing application:

```yaml
services:
  backend:
    image: company/backend:latest
    expose: ["8000"]
    networks: [internal]

  frontend:
    image: company/frontend:latest
    expose: ["3000"]
    networks: [internal]

  postgres:
    image: postgres:17
    networks: [internal]

  onionforge:
    image: emon5122/onionforge:latest
    restart: unless-stopped
    volumes:
      - onionforge-data:/var/lib/tor
      - ./onionforge.yml:/etc/onionforge/onionforge.yml:ro
    networks: [internal]

networks:
  internal:

volumes:
  onionforge-data:
```

```yaml
# onionforge.yml
services:
  backend:
    prefix: api
    target: http://backend:8000
  frontend:
    prefix: app
    target: http://frontend:3000
```

You don't need `ports:` anywhere: the services are reachable only through Tor. See [`docker-compose.example.yml`](docker-compose.example.yml) for a runnable example.

One OnionForge instance can publish any number of services. Use one instance with several services rather than several instances, unless you need separate administrators, networks or volumes.

---

## `onionforge.yml` reference

```yaml
services:
  <name>:                      # required; identifies the onion identity
    target: http://host:port   # required; http:// or https://, optional base path
    prefix: abc                # optional vanity prefix (a-z, 2-7, at most 10 characters)
    host_header: preserve      # optional: preserve | upstream | <literal host>
    rewrite_redirects: false   # optional: rewrite Location headers to the onion address
    tls:                       # optional; https:// targets only
      ca_file: /etc/onionforge/ca.pem   # trust only this CA bundle for the upstream
      server_name: backend.internal     # override SNI / verification name
      insecure_skip_verify: false       # disable verification; explicit opt-in only

security:
  allow_private_targets: false     # IP literals in 10/8, 172.16/12, 192.168/16, fc00::/7, 100.64/10…
  allow_loopback_targets: false    # 127.0.0.0/8, ::1, localhost
  allow_link_local_targets: false  # 169.254.0.0/16 (cloud metadata), fe80::/10

logging:
  access_log: false                # log one line per request
```

**Service names** are lowercase letters, digits, `-` and `_` (up to 63 characters). The name is the identity: `/var/lib/tor/<name>` holds that service's keys.

**Targets**:

| Target | Meaning |
|---|---|
| `http://backend:8000` | Docker service `backend`, port 8000 |
| `http://frontend` | port 80 |
| `https://example.com` | external site over TLS, port 443 |
| `https://example.com/app` | base path: `/x` on the onion is requested as `/app/x` upstream |
| `http://192.168.1.50:8080` | LAN address; requires `allow_private_targets: true` |

Targets can't contain credentials, query strings, fragments, whitespace, quotes or braces.

**`host_header`** sets the `Host` header sent upstream:

- `preserve` sends the `.onion` hostname. This is the default for single-label Docker names (`http://backend:8000`) and IP targets, so apps that build absolute URLs from `Host` produce onion URLs.
- `upstream` sends the target's own host. This is the default for `https://` targets and FQDNs (`http://example.com`), which virtual hosting, CDNs and TLS need.
- Any other value is sent literally, for example `host_header: app.internal`.

`X-Forwarded-Host` (the onion hostname), `X-Forwarded-Proto` and `X-Forwarded-For` are always set.

**Validation** runs before anything starts and reports every problem at once:

```
Invalid configuration /etc/onionforge/onionforge.yml:

ERROR: Service 'radiolens' has invalid target:
         ftp://example.com

       unsupported scheme "ftp://"

       Supported target schemes:
         http://
         https://
```

It checks YAML syntax, unknown keys (catching typos like `tagret:`), duplicate service names, duplicate prefixes, prefix characters, target URLs and schemes, and the SSRF policy.

---

## Docker networking

OnionForge resolves targets through Docker's DNS, the same way any container would. The gateway only needs to share a network with the upstreams:

```yaml
services:
  backend:
    image: my-backend
    expose: ["8000"]      # documentation only; listening on the network is enough
    networks: [internal]
  onionforge:
    networks: [internal]
```

The backend doesn't need `ports:`, Tor, environment variables or code changes. It just serves HTTP.

To publish services from several Compose projects, attach OnionForge to each project's network (as `external: true` networks) and target the services by name.

---

## Vanity prefixes

```yaml
services:
  radiolens:
    prefix: radiolens
    target: http://radiolens:3000
```

On first start, OnionForge runs the bundled `onion-vanity-address` generator (using all CPU cores) until it finds a key whose address starts with the prefix. Without a prefix it creates a random v3 identity.

A vanity search happens **once per identity**. After that the key is stored and reused forever.

Each character multiplies the expected search time by 32:

| Prefix length | Typical time |
|---|---|
| 1–5 | seconds |
| 6 | under a minute |
| 7 | minutes |
| 8 | hours |
| 9 | days |
| 10 (maximum) | months |

Prefixes use the onion alphabet `a–z` and `2–7`, so `0`, `1`, `8`, `9` and `-` are impossible. For prefixes of 8 or more characters, generate the key on a powerful machine and [restore it](#backup-and-restore) into the volume.

The generator's output isn't trusted blindly: OnionForge re-derives the public key and address from the secret key, checks the prefix, and after Tor starts checks that Tor's `hostname` file matches. Tor's `hostname` file is authoritative.

---

## Persistence and identities

The onion address **is** the private key. OnionForge stores one directory per service:

```text
/var/lib/tor/
├── .tor/                     # Tor's own state (guards, consensus cache)
├── api/
│   ├── hostname              # written by Tor
│   ├── hs_ed25519_public_key
│   └── hs_ed25519_secret_key # the identity, mode 0600
└── web/
    └── …
```

Always mount a named volume (or a host directory) at `/var/lib/tor`. The Dockerfile declares it as a `VOLUME`, but an anonymous volume is easy to lose with `docker compose down -v` or `docker rm -v`.

**Identity lifecycle:**

```text
identity exists for this service name?
  ├─ yes → reuse it (always)
  └─ no  → prefix? ─ yes → vanity identity
                   └ no  → random identity
           → written atomically, mode 0600, dir 0700
```

Identities are **never** regenerated because the container, Docker, Caddy or the host restarted, or because the image was upgraded.

| You change… | Result |
|---|---|
| `target` | Same address, new upstream |
| `prefix` of an existing service | **Same address kept.** A warning explains the mismatch; `onionforge list` shows `prefix mismatch` |
| Remove a service | It stops being published. Its keys stay in `/var/lib/tor/<name>`; add the service back to get the same address again |
| Rename a service | The new name gets a **new** identity; the old one stays on disk (rename the directory to carry it over) |

OnionForge refuses to start if a service directory contains a `hostname` or public key but no secret key, rather than silently creating a new address over a damaged identity.

**Resetting an identity** (permanently changes the address):

```bash
docker compose exec onionforge sh -c 'mv /var/lib/tor/api /var/lib/tor/.api.retired-$(date +%s)'
docker compose restart onionforge
```

---

## External upstreams

```yaml
services:
  company:
    prefix: company
    target: https://company.com
    rewrite_redirects: true
```

```text
Tor Browser → company….onion → Tor → Caddy → HTTPS → company.com
```

Caddy connects with the correct SNI and verifies the certificate against the system CA store. The `Host` header defaults to `company.com`.

**Redirects.** Sites often redirect to their own absolute URL (`Location: https://company.com/login`), which would send onion visitors to the clearnet. `rewrite_redirects: true` rewrites `Location` headers that point at the target's own host (any scheme or port, and the base path if one is configured) to `http://<onion>/…`. Response bodies are never rewritten, so absolute links inside HTML still point to the original site.

---

## HTTPS upstreams

`https://` targets are fully supported, and certificate verification is **on** by default.

```yaml
services:
  # Private CA (e.g. an internal PKI)
  internal:
    target: https://backend:8443
    tls:
      ca_file: /etc/onionforge/ca.pem       # mount it into the container
      server_name: backend.internal         # if the cert isn't for "backend"

  # Self-signed development backend: explicit, per service
  legacy:
    target: https://legacy:8443
    tls:
      insecure_skip_verify: true
```

`insecure_skip_verify` only applies to the service where you set it. It is never enabled globally or by default. Onion services themselves are published as `http://…onion`: Tor already authenticates and encrypts the connection end to end, so no certificates or ACME are involved.

---

## WebSockets, streaming and HTTP features

Caddy's reverse proxy passes traffic through unchanged:

- all methods (`GET`, `POST`, `PUT`, `PATCH`, `DELETE`, `OPTIONS`, `HEAD`)
- query strings, cookies and headers
- uploads and downloads, chunked request and response bodies
- streaming responses and Server-Sent Events (flushed immediately)
- WebSockets (`Upgrade` handled natively; no configuration needed)
- redirects (passed through, or rewritten with `rewrite_redirects`)

Paths are forwarded as-is. The only change is prepending a target's base path.

---

## Security

**No open proxy.** Every upstream address comes from `onionforge.yml`; the generated Caddyfile contains no request-dependent upstream. A visitor can't pick a destination: unknown `Host` headers, absolute-form requests to other hosts and `CONNECT` are all refused, and query parameters like `?url=` are just passed to your app. Tor runs with `SocksPort 0` and `ClientOnly 1`: no SOCKS proxy, relay, bridge or exit.

**SSRF policy.** Targets are static, but a mistaken or malicious config could point at internal infrastructure, so these are rejected by default:

| Target | Default |
|---|---|
| Docker service names (`backend`) and public DNS names | allowed |
| Private IP literals (10/8, 172.16/12, 192.168/16, 100.64/10, fc00::/7…) | rejected (`allow_private_targets`) |
| Loopback (`127.0.0.1`, `::1`, `localhost`) | rejected (`allow_loopback_targets`) |
| Link-local, including cloud metadata `169.254.169.254` | rejected (`allow_link_local_targets`) |
| `0.0.0.0`, multicast, `.onion` | always rejected |

Rejecting loopback by default also stops a config from pointing at OnionForge's own listener.

**No public ports.** Caddy binds `127.0.0.1:8080` inside the container. You never need `ports:`, and the only way in is through Tor.

**Least privilege.** The container starts as root only to fix ownership of the volume, then the `onionforge` binary drops to the unprivileged `onionforge` user (uid 10001) before starting Tor and Caddy. Caddy's admin API listens on a unix socket in `/run/onionforge`, not on TCP. To run fully unprivileged, set `user: "10001:10001"` and make sure the volume is owned by that uid.

**Secret keys** stay in `/var/lib/tor` with mode `0600` (directories `0700`). They are never logged, never written to generated configuration and never served over HTTP. Only `.onion` hostnames are printed.

**No authentication.** OnionForge is transport and routing infrastructure. Authentication belongs in your application.

---

## Reloading and updating

**Live reload.** Edit `onionforge.yml`, then:

```bash
docker compose kill -s HUP onionforge
```

- If only targets or options changed, Caddy reloads gracefully and Tor isn't touched.
- If services were added or removed, Tor reloads its configuration too (no restart). New identities are created first.
- If the new file is invalid, the reload is rejected and the running configuration stays in place.

> **Note on single-file bind mounts:** many editors save by writing a new file and renaming it. The container then keeps seeing the old file. Either mount the containing directory (`./config:/etc/onionforge:ro`) or run `docker compose restart onionforge` after editing.

**Upgrading the image** never touches `/var/lib/tor`:

```bash
docker compose pull onionforge
docker compose up -d onionforge
```

All addresses stay the same across versions. The on-disk identity format is Tor's own, so it's compatible with any Tor installation. Releases follow semantic versioning, and configuration semantics won't change incompatibly within a major version.

**Shutdown.** On `SIGTERM`/`SIGINT` (`docker stop`), OnionForge stops Caddy first (with a 5-second grace period for in-flight requests), then Tor, then exits with status 0. If Tor or Caddy dies unexpectedly, OnionForge stops the other and exits with status 1 so that `restart: unless-stopped` brings it back.

---

## Commands

The image's entrypoint is the `onionforge` binary:

| Command | Purpose |
|---|---|
| `onionforge run` | Start the gateway (default) |
| `onionforge validate` | Validate the configuration and print the resolved services; exit code 2 if invalid |
| `onionforge list` | Show all identities (active, inactive, prefix mismatch) with their addresses |
| `onionforge healthcheck` | Used by the Docker `HEALTHCHECK` |
| `onionforge version` | Print the version |

```bash
# Validate a config without starting anything (e.g. in CI)
docker run --rm -v ./onionforge.yml:/etc/onionforge/onionforge.yml:ro emon5122/onionforge validate

# Show addresses
docker compose exec onionforge onionforge list
```

```
SERVICE  STATUS                     ONION                                                           TARGET
api      active                     apiq2…7yd.onion                                                 http://backend:8000
old      inactive (not configured)  oldk3…qad.onion                                                 -
```

The **health check** passes when Tor and Caddy are running, every configured service's `hostname` file exists and matches its key, Caddy has a configuration loaded, and the local listener accepts connections. It doesn't contact upstreams, so a temporarily unavailable backend (which returns `502` to visitors) doesn't make the gateway unhealthy.

**Logs** are tagged by source:

```
[onionforge] Service 'api': identity created: api…yd.onion
[tor] Oct 08 07:12:32.000 [notice] Bootstrapped 100% (done): Done
[onionforge] Tor is connected to the Tor network; onion services are being published
[caddy] 2026/10/08 07:13:42.155 INFO http.log server running
```

Per-request logging is off by default; enable it with `logging.access_log: true`.

---

## Troubleshooting

| Symptom | Fix |
|---|---|
| Tor Browser: "Onion site not found" right after start | Wait 1–3 minutes after `Tor is connected to the Tor network` for the descriptor to propagate. |
| `502 Bad Gateway` | Caddy can't reach the target. Check that the upstream is on the same Docker network, the name and port are right, and it listens on `0.0.0.0` rather than `127.0.0.1`. |
| `421 Unknown onion service` | The request's `Host` isn't one of the configured onion hostnames, for example a stale address. Check `onionforge list`. |
| App redirects to its clearnet domain | Set `rewrite_redirects: true`, or configure the app's public URL to the onion address. |
| App generates links with the wrong host | Try `host_header: preserve` (the onion host) or set a literal `host_header`. |
| `private IP targets are not allowed` | Use the Docker service name, or opt in with `security.allow_private_targets: true`. |
| `x509: certificate signed by unknown authority` | Set `tls.ca_file` for a private CA (or `tls.insecure_skip_verify` as a last resort). |
| `… is owned by uid N but OnionForge runs as uid M` | You're running with `user:`. Chown the volume to that uid, or remove `user:` and let OnionForge fix ownership. |
| `refusing to create a new identity over it` | A service directory lost its `hs_ed25519_secret_key`. Restore it from backup, or move the directory away to accept a new address. |
| Prefix warning after editing `prefix:` | Expected: the existing address is kept. [Reset the identity](#persistence-and-identities) if you really want a new one. |
| Config edits aren't picked up on `SIGHUP` | See the single-file bind mount note under [Reloading](#reloading-and-updating). |

Generated files are at `/run/onionforge/torrc` and `/run/onionforge/Caddyfile` inside the container if you want to look at what OnionForge produced.

---

## Backup and restore

**Backing up `/var/lib/tor` backs up your onion addresses.** A lost secret key means a lost address, and anyone holding a copy of the key can impersonate your service. Protect backups like credentials: encrypt them and restrict access.

```bash
# Backup (from the host)
docker run --rm -v onionforge-data:/data:ro -v "$PWD":/backup debian:stable-slim \
  tar czf /backup/onionforge-identities.tgz -C /data --exclude=./.tor .

# Restore into a fresh volume
docker run --rm -v onionforge-data:/data -v "$PWD":/backup debian:stable-slim \
  tar xzf /backup/onionforge-identities.tgz -C /data
```

Ownership and permissions are fixed automatically on the next start.

**Importing an existing key** (from another Tor host, or a vanity key generated elsewhere): copy its `hs_ed25519_secret_key` into `/var/lib/tor/<service-name>/` in the volume. The public key and `hostname` are derived from it.

---

## Development and tests

Repository layout:

```text
cmd/onionforge/        entrypoint / CLI
internal/config/       onionforge.yml parsing and validation
internal/validation/   target, prefix, name and SSRF checks
internal/identity/     key generation, vanity generator wrapper, persistence
internal/tor/          torrc generation
internal/caddy/        Caddyfile generation, admin API client
internal/gateway/      supervisor: startup, reload, health, shutdown
tests/                 Docker integration suites
```

```bash
go test ./...                       # unit tests
tests/run.sh                        # unit + Docker integration suites
tests/run.sh proxy persistence      # selected suites
ONIONFORGE_TEST_TOR=1 tests/run.sh  # also round-trip through the real Tor network
```

The integration suites build the image and cover the acceptance criteria. They test:

- **config:** validation
- **multi-service:** several services and upstreams, Docker DNS, no published ports, unprivileged processes
- **proxy:** methods, paths, uploads, SSE, WebSockets, redirects, open-proxy prevention
- **persistence:** recreate, target and prefix changes, removal, live reload, key hygiene
- **vanity:** prefixes and their persistence
- **tor:** a real onion round-trip, including an external HTTPS site

Multi-architecture build:

```bash
docker buildx build --platform linux/amd64,linux/arm64 -t emon5122/onionforge:latest --push .
```

### CI and releases

- **`ci.yml`** runs vet, unit tests and the Docker integration suites on every push to `main` and every pull request. The real-Tor suite also runs, but it doesn't block.
- **`release-please.yml`** keeps a release PR open based on [Conventional Commits](https://www.conventionalcommits.org) (`feat:`, `fix:`, `feat!:`). Merging that PR updates `CHANGELOG.md` and creates the `vX.Y.Z` tag and GitHub release.
- **`release.yml`** runs on every `v*.*.*` tag. It builds the `linux/amd64` + `linux/arm64` image and pushes `emon5122/onionforge:X.Y.Z`, `:X.Y` and `:latest` (plus `:X` from 1.0 on). To rebuild an existing tag, run it manually from the Actions tab; that doesn't move `latest`.

Repository secrets:

| Secret | Purpose |
|---|---|
| `RELEASE_PLEASE_TOKEN` | Fine-grained PAT with *Contents* and *Pull requests* read/write on this repo. Tags pushed with the default `GITHUB_TOKEN` don't trigger other workflows, so without this token `release.yml` would never run. |
| `DOCKERHUB_USERNAME` | `emon5122` |
| `DOCKERHUB_TOKEN` | Docker Hub access token with push permission |

Also enable *Settings → Actions → General → Allow GitHub Actions to create and approve pull requests*.

### Out of scope (for now)

Forward/SOCKS proxying, relays and bridges, authentication, web UI, metrics, HTML rewriting, onion TLS certificates, Kubernetes, and service discovery beyond the config file. Ideas for later include `onionforge identity reset` with confirmation, Prometheus metrics, client authorization, and per-service isolation.

## License

MIT. Bundles [onion-vanity-address](https://github.com/offset/onion-vanity-address) (BSD-3-Clause), [Tor](https://www.torproject.org) (BSD-3-Clause) and [Caddy](https://caddyserver.com) (Apache-2.0).
