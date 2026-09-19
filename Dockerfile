# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Install git and ca-certificates
RUN apk add --no-cache git ca-certificates

COPY go.mod ./
# Download modules
RUN go mod download || true

COPY . .

# Build static binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o websearch cmd/server/main.go

# Production runtime stage (Distroless for minimal attack surface & fast cold-start)
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app

COPY --from=builder /app/websearch /app/websearch
COPY --from=builder /app/web/templates /app/web/templates

EXPOSE 8080

USER nonroot:nonroot

ENTRYPOINT ["/app/websearch"]
