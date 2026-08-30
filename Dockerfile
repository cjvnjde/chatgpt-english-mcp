# syntax=docker/dockerfile:1

ARG TUNNEL_CLIENT_VERSION=v0.0.13

FROM golang:latest AS tunnel-builder
ARG TUNNEL_CLIENT_VERSION
WORKDIR /src/tunnel-client
RUN test -n "${TUNNEL_CLIENT_VERSION}" \
    && git clone --depth 1 --branch "${TUNNEL_CLIENT_VERSION}" \
       https://github.com/openai/tunnel-client.git .
RUN mkdir -p /out \
    && CGO_ENABLED=0 go build -trimpath -o /out/tunnel-client ./cmd/client

FROM golang:latest AS mcp-builder
WORKDIR /src/english-learning-mcp
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN mkdir -p /out \
    && CGO_ENABLED=0 go build -trimpath -o /out/english-learning-mcp \
       ./cmd/english-learning-mcp

FROM debian:stable-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --create-home --uid 10001 app \
    && mkdir -p /app/data \
    && chown -R app:app /app

COPY --from=tunnel-builder /out/tunnel-client /usr/local/bin/tunnel-client
COPY --from=mcp-builder /out/english-learning-mcp /usr/local/bin/english-learning-mcp

USER app
ENTRYPOINT ["/usr/local/bin/tunnel-client", "run"]
