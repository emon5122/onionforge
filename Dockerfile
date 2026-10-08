# syntax=docker/dockerfile:1

# OnionForge: Tor + Caddy + vanity generator + control binary.
# Multi-arch: build with `docker buildx build --platform linux/amd64,linux/arm64 .`

ARG GO_VERSION=1.25
ARG CADDY_VERSION=2.10
ARG DEBIAN_VERSION=trixie

# ---------------------------------------------------------------------------
# Build stage: cross-compiles Go binaries for the target platform.
# ---------------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-${DEBIAN_VERSION} AS build
ARG TARGETOS
ARG TARGETARCH
# Pinned commit of github.com/offset/onion-vanity-address (BSD-3-Clause).
ARG VANITY_REPO=https://github.com/offset/onion-vanity-address
ARG VANITY_REF=37b0dc0955436c1762f8972266419230ff6960cb
ARG VERSION=dev
ENV CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH}

WORKDIR /src/vanity
RUN git init -q . \
 && git fetch -q --depth 1 "${VANITY_REPO}" "${VANITY_REF}" \
 && git checkout -q FETCH_HEAD \
 && go build -trimpath -ldflags '-s -w' -o /out/onion-vanity-address .

WORKDIR /src/onionforge
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/onionforge ./cmd/onionforge

# ---------------------------------------------------------------------------
# Official Caddy binary for the target platform.
# ---------------------------------------------------------------------------
FROM caddy:${CADDY_VERSION} AS caddy

# ---------------------------------------------------------------------------
# Runtime
# ---------------------------------------------------------------------------
FROM debian:${DEBIAN_VERSION}-slim
ARG VERSION=dev

LABEL org.opencontainers.image.title="OnionForge" \
      org.opencontainers.image.description="Publish any HTTP service as a persistent Tor Onion Service" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.licenses="MIT"

RUN apt-get update \
 && apt-get install -y --no-install-recommends tor ca-certificates \
 && rm -rf /var/lib/apt/lists/* /etc/tor/torrc \
 && groupadd --system --gid 10001 onionforge \
 && useradd --system --uid 10001 --gid onionforge --home-dir /var/lib/tor \
            --no-create-home --shell /usr/sbin/nologin onionforge \
 && rm -rf /var/lib/tor \
 && mkdir -p /var/lib/tor /run/onionforge /etc/onionforge \
 && chown onionforge:onionforge /var/lib/tor /run/onionforge \
 && chmod 0700 /var/lib/tor

COPY --from=caddy /usr/bin/caddy /usr/bin/caddy
COPY --from=build /out/onion-vanity-address /usr/local/bin/onion-vanity-address
COPY --from=build /out/onionforge /usr/local/bin/onionforge
COPY onionforge.example.yml /etc/onionforge/onionforge.example.yml

# Onion identities (private keys) and Tor state. Always mount a volume here.
VOLUME ["/var/lib/tor"]

# The container starts as root only to fix volume ownership; OnionForge then
# drops to the unprivileged 'onionforge' user before starting Tor and Caddy.
HEALTHCHECK --interval=30s --timeout=10s --start-period=120s --start-interval=2s --retries=3 \
  CMD ["onionforge", "healthcheck"]
STOPSIGNAL SIGTERM
ENTRYPOINT ["onionforge"]
CMD ["run"]
