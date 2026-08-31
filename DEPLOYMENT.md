# English MCP deployment

The deployment has two containers with distinct responsibilities:

```text
ChatGPT -> OpenAI tunnel -> openai-tunnel -> http://english-learning-mcp:8080/mcp
                                              |
Direct client -> HTTPS reverse proxy ---------+
                                              |
                                              v
                                  /app/data/english-mcp.sqlite
```

Only `english-learning-mcp` opens SQLite. The tunnel is an HTTP client of that same MCP process,
so tunnel and direct requests always use the same tools, owner namespace, and database.

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

Route the public hostname to `english-learning-mcp:8080` on the Docker network. Terminate TLS at
the proxy and match only the exact `/mcp` path. The application also returns 404 for every other
path and validates the bearer token itself.

In Dokploy, create an HTTPS domain for the `english-learning-mcp` service, select container port
`8080`, and use `/mcp` as the path. Do not publish port 8080 directly to the host.

Equivalent Traefik routing and rate-limit settings are:

```yaml
labels:
  - traefik.enable=true
  - traefik.http.routers.english-mcp.rule=Host(`english-mcp.example.com`) && Path(`/mcp`)
  - traefik.http.routers.english-mcp.entrypoints=websecure
  - traefik.http.routers.english-mcp.tls=true
  - traefik.http.routers.english-mcp.middlewares=english-mcp-rate-limit
  - traefik.http.services.english-mcp.loadbalancer.server.port=8080
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

The `openai-tunnel` container does not use the public hostname. It connects over the internal
Docker network and injects the same bearer token on that final local request hop.
