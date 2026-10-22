FROM golang:1.24-alpine AS builder
WORKDIR /build
COPY libs/common-go/ ./libs/common-go/
COPY libs/proto-gen/src/go/ ./libs/proto-gen/src/go/
COPY adapters/ew-sensor/ ./adapters/ew-sensor/
WORKDIR /build/adapters/ew-sensor
# No CGO: the binary is static and runs on any Linux base. -trimpath and -s -w
# keep the image small.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /ew-sensor ./cmd/ew-sensor

# Keep the runtime Alpine release aligned with the golang builder image.
FROM alpine:3.22
# CA certificates make --tls work with the system roots.
RUN apk add --no-cache ca-certificates
# Not root: 10001 matches the other Sentinel images. The CLI writes no files
# and binds no port, so it needs no writable path and no capability.
RUN addgroup -S -g 10001 sentinel && adduser -S -D -H -u 10001 -G sentinel sentinel
WORKDIR /app
COPY --from=builder /ew-sensor /app/ew-sensor
# Run from /app so `--config examples/coastal-esm.yaml` resolves.
COPY adapters/ew-sensor/examples/ /app/examples/
USER 10001:10001
ENTRYPOINT ["/app/ew-sensor"]
