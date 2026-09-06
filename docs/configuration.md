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

All enabled listen addresses must be different. The Anki export must never share an MCP listener or its bearer token.

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

## Anki export and worker

The single Compose stack includes the private exporter and Anki worker. Set the variables from `.env.example` in Dokploy Environment or a local `.env`; no secret-file mounts or additional Compose file are required. See [setup and recovery](deployment.md#ankiweb-sync).

| Variable | Default | Description |
|---|---|---|
| `ANKI_SYNC_ENABLED` | `false` outside Compose | Enables the Go export listener and Python worker. Compose sets this to `true` for both services. |
| `ANKI_EXPORT_LISTEN_ADDRESS` | `0.0.0.0:8082` | Go-only private listener; never publish or tunnel it. |
| `ANKI_EXPORT_TOKEN` | none | Required by Compose. Export-only bearer secret, at least 32 bytes, different from `MCP_BEARER_TOKEN`. Shared only by the exporter and worker. |
| `ANKI_SOURCE_URL` | `http://english-learning-mcp:8082/internal/anki/snapshot` | Worker snapshot endpoint. Plain HTTP is intended only for the private Compose network; use HTTPS across hosts. |
| `ANKI_SOURCE_NAMESPACE` | `english-mcp` | Stable integration identity; must agree between server and worker and must not change on an existing worker volume. |
| `MCP_OWNER_KEY` | `default` | Must agree between server and worker; only this owner's saved items are exported. |
| `ANKIWEB_USERNAME` | none | Required by Compose. AnkiWeb account email, passed only to the worker. |
| `ANKIWEB_PASSWORD` | none | Required by Compose. AnkiWeb password used to obtain reusable sync authentication, passed only to the worker. |
| `ANKI_DECK` | `English MCP` | Dedicated, exclusively managed vocabulary deck. |
| `ANKI_COLLECTION_PATH` | `/data/collection.anki2` | Private worker collection; its directory also contains sensitive state. |
| `ANKI_POLL_SECONDS` | `60` | Positive polling interval. Every poll reconciles even if the source digest is unchanged. |

The worker cannot read the application's SQLite volume or call dictionary mutations with its export credential. Anki scheduling and the application's FSRS state remain independent.

When running the Go service or Python CLI directly, `ANKI_EXPORT_TOKEN_FILE`, `ANKIWEB_USERNAME_FILE`, and `ANKIWEB_PASSWORD_FILE` remain supported alternatives to the corresponding environment values. Do not set both forms for the same secret. The supplied Compose deployment uses environment values.

Keep credentials out of version control and restrict access to Dokploy Environment and Docker inspection. In a local `.env`, single-quote passwords containing `$` so Compose preserves them literally.
