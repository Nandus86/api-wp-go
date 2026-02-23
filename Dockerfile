# Build Stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Set proxy to avoid timeouts
ENV GOPROXY=https://proxy.golang.org,direct
ENV GO111MODULE=on
ENV GOTOOLCHAIN=auto
ENV GOSUMDB=off

# Copy all files first to ensure local replacements are found
COPY . .

# Install minimal dependencies for build
RUN apk add --no-cache git

# Install Swag CLI and generate docs
RUN go install github.com/swaggo/swag/cmd/swag@latest && \
    swag init -g cmd/server/main.go --output docs

# Build the application skipping tidy
RUN CGO_ENABLED=0 GOOS=linux go build -mod=mod -v -o main cmd/server/main.go

# Final Stage
FROM alpine:latest

WORKDIR /app

# Install minimal dependencies
RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /app/main .
COPY --from=builder /app/cmd/server/static ./cmd/server/static

# Expose port
EXPOSE 8080

# Command to run
CMD ["./main"]
