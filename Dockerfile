# Build stage
FROM golang:1.21-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build binary
RUN CGO_ENABLED=0 GOOS=linux go build -o enocean-mqtt .

# Runtime stage
FROM alpine:3.19

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/enocean-mqtt .

# Default config location
VOLUME /app/config

ENTRYPOINT ["./enocean-mqtt"]
CMD ["-config", "/app/config/config.yaml"]
