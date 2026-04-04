FROM golang:1.24-alpine AS builder
RUN apk add --no-cache gcc musl-dev librdkafka-dev pkgconf
WORKDIR /build
COPY libs/common-go/ ./libs/common-go/
COPY apps/track-service/ ./apps/track-service/
WORKDIR /build/apps/track-service
RUN CGO_ENABLED=1 go build -o /server ./cmd/server

FROM alpine:3.19
RUN apk add --no-cache ca-certificates librdkafka
COPY --from=builder /server /server
EXPOSE 3002
CMD ["/server"]
