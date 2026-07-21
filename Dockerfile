# --- Rust engine (deterministic rules CLI) ---
FROM rust:1.85-alpine AS rust-builder

RUN apk add --no-cache musl-dev

WORKDIR /src
COPY rust/ ./
ENV CARGO_TARGET_DIR=/src/target
RUN cargo build --release -p prism-cli \
	&& cp /src/target/release/prism /prism-engine

# --- Go server ---
FROM golang:1.24-alpine AS go-builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /prism ./cmd/prism

# --- Runtime ---
FROM alpine:3.20

RUN apk add --no-cache ca-certificates
WORKDIR /app

COPY --from=go-builder /prism /app/prism
COPY --from=rust-builder /prism-engine /app/bin/prism-engine
COPY config/examples/rules.toml /app/config/examples/rules.toml
COPY config/repos/ /app/config/repos/

ENV PRISM_CONFIG=/app/config/examples/rules.toml
ENV PRISM_REPOS_DIR=/app/config/repos
ENV PRISM_ENGINE=/app/bin/prism-engine
ENV PRISM_PORT=8080

EXPOSE 8080
ENTRYPOINT ["/app/prism"]
