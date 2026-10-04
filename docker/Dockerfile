FROM golang:1.25-alpine AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w -extldflags=-static" \
    -o s3-orphan-cleaner ./cmd/cleaner

FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 1000 cleaner

WORKDIR /app
COPY --from=builder /build/s3-orphan-cleaner /app/s3-orphan-cleaner

USER cleaner

ENTRYPOINT ["/app/s3-orphan-cleaner"]
