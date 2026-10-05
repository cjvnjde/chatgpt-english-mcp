# Configuration

Deployment configuration is read from environment variables at startup. The process exits when a required value is absent or invalid. Learning algorithm settings are stored separately in SQLite and can be changed live in Admin → **Settings**.

## Application

| Variable | Default | Description |
|---|---|---|
| `SQLITE_PATH` | `/app/data/english-mcp.sqlite` | Persistent filesystem path or local `file:` URI. Empty paths, temporary databases, and in-memory DSN variants are rejected outside tests. |
| `MCP_OWNER_KEY` | `default` | Vocabulary namespace for this deployment. It is supplied by the server, never by a tool caller. |
| `MCP_TUNNEL_LISTEN_ADDRESS` | `0.0.0.0:8080` | Listener intended only for the private OpenAI tunnel network. |
| `MCP_EXTERNAL_LISTEN_ADDRESS` | `0.0.0.0:8081` | Bearer-authenticated listener for direct clients. |
| `MCP_BEARER_TOKEN` | none | Required direct-client secret; at least 32 bytes. |
| `ADMIN_BEARER_TOKEN` | empty | Optional admin API secret; at least 32 bytes and distinct from MCP/Anki tokens. Empty disables the admin API. |
| `CAMBRIDGE_BASE_URL` | `https://dictionary.cambridge.org` | Absolute HTTP(S) provider base URL. Primarily useful for testing. |
| `CAMBRIDGE_TIMEOUT_SECONDS` | `20` | Positive upstream request timeout in seconds, within Go's representable duration range. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error`. |
| `LOG_FORMAT` | `json` | `json` or `text`. |

All enabled listen addresses must be different. The Anki export must never share an MCP listener or its bearer token.

All bearer tokens must use HTTP bearer-token characters; embedded whitespace and controls are rejected. MCP POST requests reject foreign browser origins; same-origin and originless native clients remain supported. HTTP headers have a 10-second read deadline; complete MCP request reads, including bodies, are bounded to 30 seconds.

## Runtime algorithm settings

Open `/admin/#view=settings` with the admin token. Settings belong to `MCP_OWNER_KEY`, persist in the database (including backups), and apply without a restart. Saving does **not** rewrite past grades, recalculate existing due dates, or relabel vocabulary. Each next selection/review reads a consistent settings snapshot. A changed review correction uses current settings with the original review state/time; identical retries keep their saved schedule.

The page includes units, explanations, bounds, validation, **Discard changes**, and **Restore defaults**. Restoring defaults changes only the draft until **Save changes** is pressed. Unsaved navigation is guarded; concurrent edits return a conflict and preserve the draft rather than overwriting newer values.

| Setting / API key | Default | Allowed values and effect |
|---|---|---|
| `learningMode` | `"mixed"` | `mixed` uses the full vocabulary pool; `focused` restricts learning to unfinished members of a persistent batch and waits when none is due. Switching to mixed retains the batch for resumption. |
| `focusBatchSize` | `10` | Integer 1–100 saved meanings. Used for the next automatic batch and as the maximum size of an explicit `learning_focus` selection. An unfinished batch is never resized by a settings change. |
| `fastAnswerSeconds` | `30` seconds | Integer 0–300; `0` disables. Only `good` is promoted to `easy` when elapsed time from the token's first presentation is nonnegative and strictly below the threshold. Exactly at the threshold and slower answers keep their grade. Explicit corrections bypass promotion. |
| `requestedRetention` | `0.9` (90%) | 0.7–0.99; FSRS target recall probability. Higher values generally increase review frequency. |
| `maximumIntervalDays` | `36500` | Integer 1–36500; cap on newly scheduled long-term intervals. |
| `learningStepsMinutes` | `[1, 10]` | Up to 10 strictly increasing positive minute values below 1440; `[]` skips learning steps. |
| `relearningStepsMinutes` | `[10]` | Same bounds; steps after forgetting a review card. |
| `masteryDays` | `5` | Integer 1–365; successful UTC review days without a failure/Hard reset, at most one credit per day. |
| `masteryIntervalDays` | `21` | Integer 1–maximum interval; promotion also requires FSRS Review state. |
| `presentationCooldownMinutes` | `30` | Integer 0–1440; time window for recent-card exclusion. Zero disables cooldown. Small pools can relax exclusions. |
| `recentPresentationCount` | `3` | Integer 0–100; recent event window, combined with the cooldown duration. Zero disables cooldown. |
| `newShareMin`, `newShareMax` | `0.2`, `0.8` | Each 0.01–0.99, min ≤ max. Bound the new share **within active work** when new and due-review pools both exist; due learning steps take priority. |
| `learnedShareMin`, `learnedShareMax` | `0.1`, `0.4` | Each 0–0.99, min ≤ max. Bound learned maintenance when competing with active work; not a quota when only learned cards are available. |
| `learnedWeightDivisor` | `9` | 1–100; learned share starts at `learnedWeight / (divisor × activeWeight + learnedWeight)`, then is bounded. |
| `lowUsefulnessWeight`, `highUsefulnessWeight` | `0.5`, `2` | Each 0.05–20; new-card and reinforcement multipliers. Normal remains 1. |
| `lowInterestWeight`, `highInterestWeight` | `0.5`, `2` | Each 0.05–20; selectable-card and reinforcement multipliers. Normal remains 1. |
| `unseenExposureWeight` | `4` | 0.05–20; unseen-card exposure priority. |
| `exposureRecoveryHours` | `24` | 1–168; exposure recovery from 0.25 to 1. Subsequent scheduled-selection rise from 1 to 2 still takes 29 days. Also controls reinforcement soft recency. |
| `reinforcementCooldownHours` | `6` | 0–720; hard word-level issuance cooldown; zero disables. |
| `reinforcementMaxWordShare` | `0.25` (25%) | 0.05–1; capped lottery requires at least `max(4, ceil(1 / cap))` distinct eligible learned words. |
| `troublesomeConsecutiveFailures` | `2` | Integer 1–100; threshold for the troublesome flag/reason. |
| `troublesomeLapses` | `3` | Integer 1–100; alternate threshold for the troublesome flag/reason. |

`learning_focus` start/stop changes the same persisted learning mode and advances the settings revision, so a stale Admin draft receives the normal conflict response. Migration adds mixed mode and batch size 10 to existing settings while preserving other values and revisions. Batch membership is included in SQLite backups.

Percentages are displayed as percentages in the UI and stored as fractions in the API. The admin API provides:

- `GET /admin/api/settings` → `{ values, revision, defaults }`.
- `PUT /admin/api/settings` with `{ values, expectedRevision }` → the saved snapshot and defaults.
- An owner without saved overrides starts at revision `0`. PUT requires **all** values, rejects unknown/missing/null fields, and validates cross-field bounds atomically. A stale revision returns HTTP `409`; a missing revision returns `428`.

FSRS fitted model coefficients are not manual controls. Model weights remain based on the dependency defaults, short-term scheduling stays enabled, and interval fuzz stays disabled. Network settings and credentials remain environment-only.


## OpenAI tunnel container

| Variable | Default | Description |
|---|---|---|
| `CONTROL_PLANE_API_KEY` | none | Required credential used by the tunnel client. It is not sent to the MCP server. |
| `CONTROL_PLANE_TUNNEL_ID` | none | Required tunnel identifier. |
| `IMAGE_TAG` | `latest` | Optional tag for the locally built image. |

The image builds tunnel client `v0.0.14` from immutable commit `0f870e50a973fa820d4c409000059e181e8d242b`. Update its pin in `Dockerfile` when upgrading; there is no runtime version override.

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
