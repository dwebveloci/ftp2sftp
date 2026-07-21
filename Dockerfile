# syntax=docker/dockerfile:1

# --- build stage -------------------------------------------------------
FROM golang:1.25-bookworm AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/ftp2sftp ./cmd/ftp2sftp

# --- runtime stage -------------------------------------------------------
# distroless/static: no shell, no package manager, no libc beyond what the
# static binary already contains. Runs as the built-in "nonroot" user
# (uid 65532) by default, so no root execution is possible even if the
# image is misconfigured downstream (RNF-001 / section 14.1).
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

WORKDIR /app

COPY --from=build /out/ftp2sftp /app/ftp2sftp

USER nonroot:nonroot

# 2121: FTP control port (unprivileged; see docs/deployment/deployment-model.md
#   for why 21 is not bound directly inside the container).
# 8080: internal health/readiness/metrics HTTP server.
# The passive port range is configuration-defined and has no single fixed
# value to EXPOSE; publish whatever range server.passivePortStart/End uses.
EXPOSE 2121 8080

# The runtime image has no shell or curl/wget, so the binary checks itself
# via `--healthcheck` (see cmd/ftp2sftp/main.go).
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/app/ftp2sftp", "--healthcheck"]

ENTRYPOINT ["/app/ftp2sftp"]
