FROM --platform=$BUILDPLATFORM golang:1.25-bookworm AS builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOTOOLCHAIN=local go build -trimpath -ldflags="-s -w" -o /out/autodrive-server ./cmd/server

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --uid 10001 --create-home autodrive \
    && mkdir -p /var/lib/autodrive \
    && chown autodrive:autodrive /var/lib/autodrive
WORKDIR /app
COPY --from=builder /out/autodrive-server /usr/local/bin/autodrive-server
USER autodrive
ENV AUTODRIVE_ADDR=:8080 \
    AUTODRIVE_DB_PATH=/var/lib/autodrive/autodrive.db
EXPOSE 8080
VOLUME ["/var/lib/autodrive"]
HEALTHCHECK --interval=5s --timeout=2s --start-period=5s --retries=12 CMD curl -fsS http://127.0.0.1:8080/readyz || exit 1
ENTRYPOINT ["/usr/local/bin/autodrive-server"]
