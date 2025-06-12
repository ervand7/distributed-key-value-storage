# Build stage
FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY . .
RUN go mod download
RUN CGO_ENABLED=0 go build -o /dkv ./cmd/node

# Runtime stage
FROM alpine:3.20
WORKDIR /app
COPY --from=builder /dkv /dkv
ENTRYPOINT ["/dkv"]
