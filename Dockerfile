FROM golang:alpine AS builder

# Install git for fetching dependencies
RUN apk update && apk add --no-cache git

WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download dependencies
# Note: go.mod indicates 1.25.1 which is likely a future version or typo. 
# We use the latest alpine go image which should handle it if it's compatible with current syntax.
RUN go mod download

# Copy source code
COPY . .

# Build the application
# -o main: output filename
# cmd/server/main.go: entry point
RUN CGO_ENABLED=0 GOOS=linux go build -o main cmd/server/main.go

# Final stage
FROM alpine:latest

WORKDIR /app

# Install dependencies if needed (e.g. ca-certificates for https)
RUN apk --no-cache add ca-certificates tzdata

# Set timezone
ENV TZ=Asia/Shanghai

# Copy binary from builder
COPY --from=builder /app/main .

# Copy config files
COPY --from=builder /app/config.yaml .
COPY --from=builder /app/config.prod.yaml .

# Expose port
EXPOSE 9081

# Command to run
CMD ["./main", "-config", "config.yaml"]

