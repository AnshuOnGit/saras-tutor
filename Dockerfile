# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Install git (needed for go mod download)
RUN apk add --no-cache git ca-certificates

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build the studio binary
RUN CGO_ENABLED=0 GOOS=linux go build -o /studio cmd/studio/main.go

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