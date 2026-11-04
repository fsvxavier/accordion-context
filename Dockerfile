# syntax=docker/dockerfile:1

# Builds the acd-ctx proxy into a static, non-root image. Run it with
# docker-compose.yml (see docs/docker.md). Keep GO_VERSION equal to the go
# directive in go.mod; CI passes it from go.mod.
ARG GO_VERSION=1.27.1

# ---- builder -----------------------------------------------------------------
FROM golang:${GO_VERSION} AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
ARG VERSION=dev
# CGO_ENABLED=0: the SQLite driver (modernc.org/sqlite) is pure Go.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/acd-ctx ./cmd/acd-ctx \
 && install -d -o 65532 -g 65532 -m 0700 /out/data

# ---- runtime -----------------------------------------------------------------
# distroless/static has CA certificates, no shell and no package manager. The
# image runs as the nonroot user (65532). Everything the proxy writes (database,
# MITM CA, plugin token, exports) lives under /data, which is the only volume.
FROM gcr.io/distroless/static-debian12:nonroot

# ACD_CTX_DB makes every CLI command in the container use the database that
# serve runs on (otherwise the XDG path would point at an empty second database).
ENV HOME=/data \
    XDG_CONFIG_HOME=/data/config \
    ACD_CTX_DB=/data/acd-ctx.db \
    ACD_CTX_ALLOWED_HOSTS=localhost,127.0.0.1,[::1],host.docker.internal

COPY --from=builder /out/acd-ctx /acd-ctx
COPY --from=builder --chown=65532:65532 /out/data /data

WORKDIR /data
VOLUME ["/data"]
EXPOSE 8787
USER 65532:65532

# Exec form: distroless has no shell, so no curl either. The probe is the
# binary itself (internal/cli/healthcheck.go).
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["/acd-ctx", "healthcheck", "--port", "8787"]

ENTRYPOINT ["/acd-ctx"]
CMD ["serve", "--listen", "0.0.0.0:8787", "--allow-non-loopback", \
     "--db", "/data/acd-ctx.db", "--config", "/data/config.yaml"]
