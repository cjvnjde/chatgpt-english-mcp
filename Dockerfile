# syntax=docker/dockerfile:1.27.0@sha256:bde3983e9c939224420ddaf6b784cc30e09b035a4dea01f581230c50809f372e

FROM golang:1.27.1-trixie@sha256:9baa6b4187bbb98d240372a8a235ac0bb6b5ddd52bba1431dc2f7c0705862728 AS tunnel-builder
WORKDIR /src/tunnel-client
# tunnel-client v0.0.14, pinned to its peeled release commit.
RUN git init . \
    && git remote add origin https://github.com/openai/tunnel-client.git \
    && git fetch --depth 1 origin 0f870e50a973fa820d4c409000059e181e8d242b \
    && git checkout --detach FETCH_HEAD
RUN mkdir -p /out \
    && CGO_ENABLED=0 go build -trimpath -o /out/tunnel-client ./cmd/client

FROM golang:1.27.1-trixie@sha256:9baa6b4187bbb98d240372a8a235ac0bb6b5ddd52bba1431dc2f7c0705862728 AS mcp-builder
WORKDIR /src/english-learning-mcp
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN mkdir -p /out \
    && CGO_ENABLED=0 go build -trimpath -o /out/english-learning-mcp \
       ./cmd/english-learning-mcp

FROM debian:stable-slim@sha256:04634311a8d5fc442b6eb06d792293c4f3e2268652ca7634e00ce8ef5cc0a28a
RUN mkdir -p /app/data /home/app \
    && chown -R 10001:10001 /app /home/app

COPY --from=mcp-builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=tunnel-builder /out/tunnel-client /usr/local/bin/tunnel-client
COPY --from=mcp-builder /out/english-learning-mcp /usr/local/bin/english-learning-mcp
COPY --from=mcp-builder /src/english-learning-mcp/internal/usefulness/licenses /usr/share/licenses/english-learning-mcp/frequency

ENV HOME=/home/app
USER 10001:10001
EXPOSE 8080 8081
ENTRYPOINT ["/usr/local/bin/english-learning-mcp"]
