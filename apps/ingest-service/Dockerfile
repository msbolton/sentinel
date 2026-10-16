FROM golang:1.24-alpine AS builder
# librdkafka is linked dynamically against the Alpine package: the static bundle
# shipped with confluent-kafka-go has no musl build for arm64.
RUN apk add --no-cache gcc musl-dev librdkafka-dev
WORKDIR /build
COPY libs/common-go/ ./libs/common-go/
COPY apps/ingest-service/ ./apps/ingest-service/
WORKDIR /build/apps/ingest-service
RUN CGO_ENABLED=1 GOOS=linux go build -tags dynamic -o /ingest-service ./cmd/server

# Runtime librdkafka must be at least the version confluent-kafka-go was built against;
# keep the runtime Alpine release aligned with the golang builder image.
FROM alpine:3.22
RUN apk add --no-cache ca-certificates librdkafka
COPY --from=builder /ingest-service /ingest-service
EXPOSE 4000
CMD ["/ingest-service"]
