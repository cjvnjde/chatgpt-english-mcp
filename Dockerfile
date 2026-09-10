# syntax=docker/dockerfile:1

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
COPY --from=mcp-builder /out/english-learning-mcp /usr/local/bin/english-learning-mcp
COPY --from=mcp-builder /src/english-learning-mcp/internal/usefulness/licenses /usr/share/licenses/english-learning-mcp/frequency

ENV HOME=/home/app
USER 10001:10001
EXPOSE 8081
ENTRYPOINT ["/usr/local/bin/english-learning-mcp"]
