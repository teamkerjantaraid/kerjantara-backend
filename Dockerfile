# Stage 1: Build the Go binary
FROM golang:1.25-alpine AS builder

# Install git and certificates
RUN apk update && apk add --no-cache git ca-certificates

WORKDIR /app

# Copy dependency files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build production binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o kerjantara-backend cmd/main.go

# Stage 2: Run the binary in a clean environment
FROM alpine:3.18

RUN apk update && apk add --no-cache ca-certificates tzdata

WORKDIR /app

# Copy the binary from builder
COPY --from=builder /app/kerjantara-backend .

# Expose port
EXPOSE 8080

# Command to run
CMD ["./kerjantara-backend"]
