# --- Stage 1: Builder ---
# Use the Go version matching your go.mod (1.24) on Alpine
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Copy dependency files first to leverage Docker cache
COPY go.mod go.sum ./
RUN go mod download

# Copy the source code
COPY main.go .

# Build the binary. 
# CGO_ENABLED=0 ensures a static binary that works perfectly on Alpine.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o manager main.go

# --- Stage 2: Runtime ---
# We use Alpine Linux so you have a shell and 'apk'
FROM alpine:latest

WORKDIR /

# Install curl (and ca-certificates for HTTPS calls)
# 'bash' is added just in case you prefer it over standard 'sh'
RUN apk add --no-cache curl bash ca-certificates

# Copy the binary from the builder stage
COPY --from=builder /app/manager .

# NOTE: We run as root by default so you can use 'apk add' 
# inside the running container if you need more tools later.
# For production, you would typically switch to a non-root user.
# USER 65532:65532

ENTRYPOINT ["/manager"]