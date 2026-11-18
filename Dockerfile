# syntax=docker/dockerfile:1

FROM golang:bookworm AS app
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o /out/lnd4su ./cmd/server

FROM debian:bookworm AS iso
RUN apt-get update && apt-get install -y --no-install-recommends \
        git gcc make pkg-config libcap-dev libseccomp-dev libc6-dev ca-certificates \
    && rm -rf /var/lib/apt/lists/*
# Only `make isolate`: isolate-cg-keeper is a systemd helper (needs
# libsystemd via pkg-config) and is useless inside a container, where the
# app itself keeps the cgroup alive.
RUN git clone --depth 1 https://github.com/ioi/isolate /tmp/isolate \
    && cd /tmp/isolate \
    && make isolate \
    && mkdir -p /out \
    && install -m 0755 isolate /out/isolate

FROM debian:bookworm-slim AS runtime
RUN apt-get update && apt-get install -y --no-install-recommends \
        gcc g++ libc6-dev libcap2-bin libseccomp2 curl ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=app /out/lnd4su /usr/local/bin/lnd4su
COPY --from=iso /out/isolate /usr/local/bin/isolate

RUN chown root:root /usr/local/bin/isolate \
    && chmod 4755 /usr/local/bin/isolate \
    && chmod 0755 /usr/local/bin/lnd4su

RUN mkdir -p /data /var/local/isolate /run/isolate

USER root

ENV LND4SU_PORT=8080 \
    LND4SU_DATA=/data \
    LND4SU_WORKERS=1 \
    LND4SU_ISOLATE=/usr/local/bin/isolate \
    LND4SU_BOXROOT=/var/local/isolate \
    LND4SU_USERS=/wiki-conf/users.auth.php

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s \
    CMD curl -fsS http://127.0.0.1:8080/healthz || exit 1

CMD ["/usr/local/bin/lnd4su"]
