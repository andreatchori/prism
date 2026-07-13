# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /prism ./cmd/prism

# Runtime stage
FROM alpine:3.20

RUN apk add --no-cache ca-certificates
WORKDIR /app

COPY --from=builder /prism /app/prism
COPY config/examples/rules.toml /app/config/examples/rules.toml

ENV PRISM_CONFIG=/app/config/examples/rules.toml
ENV PRISM_PORT=8080

EXPOSE 8080
ENTRYPOINT ["/app/prism"]
