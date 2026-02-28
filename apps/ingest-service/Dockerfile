FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -o /ingest-service ./cmd/server

FROM alpine:3.19
RUN apk add --no-cache ca-certificates librdkafka
COPY --from=builder /ingest-service /ingest-service
EXPOSE 4000
CMD ["/ingest-service"]
