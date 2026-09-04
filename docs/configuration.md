# Configuration

Configuration is read from environment variables at startup. The process exits when a required value is absent or invalid.

## Application

| Variable | Default | Description |
|---|---|---|
| `SQLITE_PATH` | `/app/data/english-mcp.sqlite` | SQLite database path. `:memory:` is rejected outside tests. |
| `MCP_OWNER_KEY` | `default` | Vocabulary namespace for this deployment. It is supplied by the server, never by a tool caller. |
| `MCP_TUNNEL_LISTEN_ADDRESS` | `0.0.0.0:8080` | Listener intended only for the private OpenAI tunnel network. |
| `MCP_EXTERNAL_LISTEN_ADDRESS` | `0.0.0.0:8081` | Bearer-authenticated listener for direct clients. |
| `MCP_BEARER_TOKEN` | none | Required direct-client secret; at least 32 bytes. |
| `CAMBRIDGE_BASE_URL` | `https://dictionary.cambridge.org` | Absolute HTTP(S) provider base URL. Primarily useful for testing. |
| `CAMBRIDGE_TIMEOUT_SECONDS` | `20` | Positive upstream request timeout in seconds. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error`. |
| `LOG_FORMAT` | `json` | `json` or `text`. |

The two listen addresses must be different.

## OpenAI tunnel container

| Variable | Default | Description |
|---|---|---|
| `CONTROL_PLANE_API_KEY` | none | Required credential used by the tunnel client. It is not sent to the MCP server. |
| `CONTROL_PLANE_TUNNEL_ID` | none | Required tunnel identifier. |
| `TUNNEL_CLIENT_VERSION` | `v0.0.13` | Tunnel-client Git tag built into the image. |
| `IMAGE_TAG` | `latest` | Optional tag for the locally built image. |

## Generate the direct-client token

```sh
openssl rand -base64 32
```

Put the complete output in `MCP_BEARER_TOKEN`. Do not reuse the OpenAI control-plane key for direct MCP authentication.

## Direct MCP client

After routing HTTPS to container port `8081`, configure a Streamable HTTP client:

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

The exact client configuration shape may vary by host. The endpoint path is always `/mcp`.

## Namespace model

`MCP_OWNER_KEY` provides one vocabulary namespace per running deployment. It is not user authentication and callers cannot select or switch it through MCP tools. Use separate deployments or carefully isolated configuration and databases when independent users must not share learning data.
