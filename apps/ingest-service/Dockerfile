FROM golang:1.24-alpine AS builder
# librdkafka is linked dynamically against the Alpine package: the static bundle
# shipped with confluent-kafka-go has no musl build for arm64.
RUN apk add --no-cache gcc musl-dev librdkafka-dev
WORKDIR /build
COPY libs/common-go/ ./libs/common-go/
COPY libs/proto-gen/src/go/ ./libs/proto-gen/src/go/
COPY apps/ingest-service/ ./apps/ingest-service/
WORKDIR /build/apps/ingest-service
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 GOOS=linux go build -tags dynamic -o /ingest-service ./cmd/server

# Runtime librdkafka must be at least the version confluent-kafka-go was built against;
# keep the runtime Alpine release aligned with the golang builder image.
FROM alpine:3.22
RUN apk add --no-cache ca-certificates librdkafka
# Not root: 10001 is also the id of the Helm chart's securityContext, and a
# numeric USER lets the kubelet check runAsNonRoot. The service writes no files
# (it only reads the files connections name, in secretRefs and stream TLS file
# paths) and binds ports from 1024 up, so it needs no writable path and no
# capability. The binary below stays root-owned.
RUN addgroup -S -g 10001 sentinel && adduser -S -D -H -u 10001 -G sentinel sentinel
COPY --from=builder /ingest-service /ingest-service
USER 10001:10001
EXPOSE 4000
CMD ["/ingest-service"]
