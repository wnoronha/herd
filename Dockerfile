# Build stage
FROM golang:1.27-alpine AS builder

WORKDIR /src

# Copy dependency manifests
COPY go.mod go.sum ./
RUN go mod download

# Copy source tree
COPY . .

# Build statically linked binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/herd ./cmd/herd

# Runtime stage - pure minimal Alpine without Node.js
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /bin/herd /usr/local/bin/herd

# Create non-root herd user and group
RUN addgroup -S herd && adduser -S herd -G herd -h /home/herd

USER herd
WORKDIR /home/herd

ENTRYPOINT ["/usr/local/bin/herd"]
CMD ["daemon", "run"]
