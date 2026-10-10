# Builder stage
FROM docker.io/library/golang:1.27.2-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build
COPY . ./
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o bot .

# Final stage
FROM docker.io/library/alpine:3.24

RUN apk --no-cache add ca-certificates

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/bot .

# Create non-root user and group
RUN addgroup -g 1001 bot && \
    adduser -D -u 1001 -G bot bot && \
    chown -R bot:bot /app


# Switch to non-root user
USER 1001

CMD ["./bot"]
