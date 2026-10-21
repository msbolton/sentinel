# The builder always runs on the build host's own architecture and
# cross-compiles: this service has no CGO dependency, so no emulation is needed.
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS builder
ARG TARGETOS=linux
ARG TARGETARCH
WORKDIR /build
COPY libs/common-go/ ./libs/common-go/
COPY apps/link-analysis-service/ ./apps/link-analysis-service/
WORKDIR /build/apps/link-analysis-service
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /server ./cmd/server

# Keep the runtime Alpine release aligned with the other services.
FROM alpine:3.22
RUN apk add --no-cache ca-certificates
# Not root: 10001 is also the id of the Helm chart's securityContext, and a
# numeric USER lets the kubelet check runAsNonRoot. The service writes no files
# and binds ports from 1024 up, so it needs no writable path and no capability.
# The binary below stays root-owned.
RUN addgroup -S -g 10001 sentinel && adduser -S -D -H -u 10001 -G sentinel sentinel
COPY --from=builder /server /server
USER 10001:10001
EXPOSE 3004
CMD ["/server"]
