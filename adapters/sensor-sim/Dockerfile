FROM golang:1.24-alpine AS builder
WORKDIR /build
COPY libs/common-go/ ./libs/common-go/
COPY libs/proto-gen/src/go/ ./libs/proto-gen/src/go/
COPY adapters/sensor-sim/ ./adapters/sensor-sim/
WORKDIR /build/adapters/sensor-sim
# No CGO: the binary is static and runs on any Linux base. -trimpath and -s -w
# keep the image small.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /sensor-sim ./cmd/sensor-sim

# Keep the runtime Alpine release aligned with the golang builder image.
FROM alpine:3.22
# CA certificates make --tls and the terrain downloads (HTTPS) work with the
# system roots.
RUN apk add --no-cache ca-certificates
# Not root: 10001 matches the other Sentinel images. The only path it writes
# is the terrain cache, created here owned by that user so that a named
# volume mounted on it starts out writable.
RUN addgroup -S -g 10001 sentinel && adduser -S -D -H -u 10001 -G sentinel sentinel \
 && mkdir -p /var/cache/sentinel/terrain && chown -R 10001:10001 /var/cache/sentinel
WORKDIR /app
COPY --from=builder /sensor-sim /app/sensor-sim
# Run from /app so `--config examples/radar.yaml` resolves.
COPY adapters/sensor-sim/examples/ /app/examples/
USER 10001:10001
ENTRYPOINT ["/app/sensor-sim"]
