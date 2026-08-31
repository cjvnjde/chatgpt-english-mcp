# English MCP deployment

The deployment has two containers with distinct responsibilities:

```text
ChatGPT -> OpenAI tunnel -> openai-tunnel -> :8080/mcp (internal, no MCP auth)
                                                |
Direct client -> HTTPS reverse proxy -> :8081/mcp (bearer auth)
                                                |
                                                v
                                    /app/data/english-mcp.sqlite
```

Only `english-learning-mcp` opens SQLite. Its two listeners use the same tools, owner namespace,
and database, but have different access policies:

- port `8080` is for the private Docker-network tunnel connection and requires no MCP bearer token;
- port `8081` is for direct external access and requires the configured MCP bearer token.

The OpenAI control-plane key remains necessary for the tunnel client to establish its tunnel. It is
not forwarded to the MCP server and is separate from direct-client authentication.

## Configure and start

Copy `.env.example` to `.env`, retain the existing OpenAI tunnel values, and generate a bearer
token containing at least 32 bytes of randomness:

```sh
openssl rand -base64 32
```

Set the output as `MCP_BEARER_TOKEN`, then deploy:

```sh
docker compose up -d --build
```

The existing `english-learning-data` named volume and `/app/data/english-mcp.sqlite` path are
unchanged. Do not run `docker compose down -v`, because `-v` deletes the database volume.

## HTTPS reverse proxy

Route the public hostname to `english-learning-mcp:8081` on the Docker network. Terminate TLS at
the proxy and match only the exact `/mcp` path. The application also returns 404 for every other
path and validates the bearer token itself.

In Dokploy, create an HTTPS domain for the `english-learning-mcp` service, select container port
`8081`, and use `/mcp` as the path. Do not publish either application port directly to the host.

Equivalent Traefik routing and rate-limit settings are:

```yaml
labels:
  - traefik.enable=true
  - traefik.http.routers.english-mcp.rule=Host(`english-mcp.example.com`) && Path(`/mcp`)
  - traefik.http.routers.english-mcp.entrypoints=websecure
  - traefik.http.routers.english-mcp.tls=true
  - traefik.http.routers.english-mcp.middlewares=english-mcp-rate-limit
  - traefik.http.services.english-mcp.loadbalancer.server.port=8081
  - traefik.http.middlewares.english-mcp-rate-limit.ratelimit.average=60
  - traefik.http.middlewares.english-mcp-rate-limit.ratelimit.period=1m
  - traefik.http.middlewares.english-mcp-rate-limit.ratelimit.burst=20
```

Use the certificate resolver and Docker network names from the existing Traefik/Dokploy setup.
TLS must be enabled before connecting a client; the container intentionally serves plain HTTP only
inside the private Docker network.

## Direct client

```json
{
  "mcpServers": {
    "english": {
      "type": "http",
      "url": "https://english-mcp.example.com/mcp",
      "headers": {
        "Authorization": "Bearer YOUR_SECRET_TOKEN"
      }
    }
  }
}
```

The `openai-tunnel` container does not use the public hostname. It connects to the unauthenticated
port `8080` over the private Docker network and sends no MCP authorization header. Never route a
public hostname or publish a host port to `8080`.
