# Build Stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Set proxy to avoid timeouts
ENV GOPROXY=https://proxy.golang.org,direct
ENV GO111MODULE=on
ENV GOTOOLCHAIN=auto

# Install minimal dependencies for build
RUN apk add --no-cache git

# Copy all files first to ensure local replacements are found
COPY . .

# Install Swag CLI and generate docs
RUN go install github.com/swaggo/swag/cmd/swag@v1.16.6 && \
    swag init -g cmd/server/main.go --output docs --parseDependency=false

ARG VERSION=0.0.8
# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -mod=mod -trimpath \
    -ldflags "-s -w -X github.com/user/whatsmeow-basileia/internal/api.Version=${VERSION}" \
    -o main cmd/server/main.go

# Final Stage
FROM alpine:3.20

WORKDIR /app

# Install minimal dependencies (wget is used by the healthcheck)
RUN apk add --no-cache ca-certificates tzdata wget && \
    addgroup -S app && adduser -S -G app -h /app app && \
    mkdir -p /app/data && chown -R app:app /app

COPY --from=builder --chown=app:app /app/main .
COPY --from=builder --chown=app:app /app/cmd/server/static ./cmd/server/static

ENV DATA_DIR=/app/data
VOLUME ["/app/data"]

USER app

# Expose port
EXPOSE 8080

HEALTHCHECK --interval=15s --timeout=5s --start-period=30s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/health >/dev/null || exit 1

# Command to run
CMD ["./main"]
