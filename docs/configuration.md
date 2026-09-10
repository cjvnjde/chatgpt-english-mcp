# Configuration

Configuration is read from environment variables at startup. The process exits when a required value is absent or invalid.

## Application

| Variable | Default | Description |
|---|---|---|
| `SQLITE_PATH` | `/app/data/english-mcp.sqlite` | Persistent filesystem path or local `file:` URI. Empty paths, temporary databases, and in-memory DSN variants are rejected outside tests. |
| `MCP_OWNER_KEY` | `default` | Vocabulary namespace for this deployment. It is supplied by the server, never by a tool caller. |
| `MCP_EXTERNAL_LISTEN_ADDRESS` | `0.0.0.0:8081` | The sole MCP listener; all clients require bearer authentication. |
| `MCP_BEARER_TOKEN` | none | Required secret for all MCP clients; at least 32 bytes. |
| `ADMIN_BEARER_TOKEN` | empty | Optional admin API secret; at least 32 bytes and distinct from MCP/Anki tokens. Empty disables the admin API. |
| `CAMBRIDGE_BASE_URL` | `https://dictionary.cambridge.org` | Absolute HTTP(S) provider base URL. Primarily useful for testing. |
| `CAMBRIDGE_TIMEOUT_SECONDS` | `20` | Positive upstream request timeout in seconds, within Go's representable duration range. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error`. |
| `LOG_FORMAT` | `json` | `json` or `text`. |

All enabled listen addresses must be different. The Anki export must never share an MCP listener or its bearer token.

All bearer tokens must use HTTP bearer-token characters; embedded whitespace and controls are rejected. MCP POST requests reject foreign browser origins; same-origin and originless native clients remain supported. HTTP headers have a 10-second read deadline; complete MCP request reads, including bodies, are bounded to 30 seconds.

## Container image

| Variable | Default | Description |
|---|---|---|
| `IMAGE_TAG` | `latest` | Optional tag for the locally built image. |

## Generate the MCP token

```sh
openssl rand -base64 32
```

Put the complete output in `MCP_BEARER_TOKEN`. All MCP clients use this token. Keep it distinct from the admin and Anki export tokens.

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
| `ANKI_EXPORT_LISTEN_ADDRESS` | `0.0.0.0:8082` | Go-only private listener; never publish it or route it through the public reverse proxy. |
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
