# syntax=docker/dockerfile:1@sha256:ecfaec9ed6d810b56388c508f4121597bfbba70d41a6dfeee4d8cad5f295fc32

FROM golang:1.27.1-bookworm@sha256:648f440f42a0958804efb24df176f806f9d353b41f1c0627f666428e40310f6b AS tunnel-builder
WORKDIR /src/tunnel-client
# tunnel-client v0.0.13, pinned to its peeled release commit.
RUN git init . \
    && git remote add origin https://github.com/openai/tunnel-client.git \
    && git fetch --depth 1 origin 4b5267f823be0b046bb883aacb51603cfde3a0ea \
    && git checkout --detach FETCH_HEAD
RUN mkdir -p /out \
    && CGO_ENABLED=0 go build -trimpath -o /out/tunnel-client ./cmd/client

FROM golang:1.27.1-bookworm@sha256:648f440f42a0958804efb24df176f806f9d353b41f1c0627f666428e40310f6b AS mcp-builder
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
