FROM golang:1.24-alpine AS builder
RUN apk add --no-cache gcc musl-dev
WORKDIR /build
COPY libs/common-go/ ./libs/common-go/
COPY apps/link-analysis-service/ ./apps/link-analysis-service/
WORKDIR /build/apps/link-analysis-service
RUN CGO_ENABLED=0 go build -o /server ./cmd/server

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=builder /server /server
EXPOSE 3004
CMD ["/server"]
