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
RUN mkdir -p /app/data /home/app \
    && chown -R 10001:10001 /app /home/app

COPY --from=mcp-builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=tunnel-builder /out/tunnel-client /usr/local/bin/tunnel-client
COPY --from=mcp-builder /out/english-learning-mcp /usr/local/bin/english-learning-mcp

ENV HOME=/home/app
USER 10001:10001
EXPOSE 8080 8081
ENTRYPOINT ["/usr/local/bin/english-learning-mcp"]
