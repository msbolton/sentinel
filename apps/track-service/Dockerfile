FROM golang:1.24-alpine AS builder
# librdkafka is linked dynamically against the Alpine package: the static bundle
# shipped with confluent-kafka-go has no musl build for arm64.
RUN apk add --no-cache gcc musl-dev librdkafka-dev pkgconf
WORKDIR /build
COPY libs/common-go/ ./libs/common-go/
COPY apps/track-service/ ./apps/track-service/
WORKDIR /build/apps/track-service
RUN CGO_ENABLED=1 go build -tags dynamic -o /server ./cmd/server

# Runtime librdkafka must be at least the version confluent-kafka-go was built against;
# keep the runtime Alpine release aligned with the golang builder image.
FROM alpine:3.22
RUN apk add --no-cache ca-certificates librdkafka
COPY --from=builder /server /server
EXPOSE 3002
CMD ["/server"]
