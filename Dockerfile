# Build stage — Go 1.24 with auto-toolchain for 1.25 deps
FROM golang:1.24-alpine AS builder

WORKDIR /app

ENV GOTOOLCHAIN=auto

# Install git (needed for go mod download)
RUN apk add --no-cache git ca-certificates

# Copy go mod files
COPY go.mod go.sum ./
RUN GOTOOLCHAIN=auto go mod download

# Copy source
COPY . .

# Build the studio binary
RUN GOTOOLCHAIN=auto CGO_ENABLED=0 GOOS=linux go build -o /studio cmd/studio/main.go

# Runtime stage
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

# Copy binary from builder
COPY --from=builder /studio /app/studio

# Expose port
EXPOSE 8090

# Run
CMD ["/app/studio"]