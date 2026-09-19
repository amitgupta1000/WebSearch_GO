# Build stage
FROM golang:1.23-alpine AS builder

WORKDIR /app

# Install git and ca-certificates
RUN apk add --no-cache git ca-certificates

# go.sum is required for reproducible module downloads.
# Run `go mod tidy` locally and commit go.sum before building this image.
COPY go.mod go.sum ./

# Download modules
RUN go mod download

COPY . .

# Build static binary (HTML templates are embedded via internal/web)
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-w -s" -o websearch ./cmd/server

# Production runtime stage (Distroless for minimal attack surface & fast cold-start)
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app

COPY --from=builder /app/websearch /app/websearch

EXPOSE 8080

USER nonroot:nonroot

ENTRYPOINT ["/app/websearch"]
