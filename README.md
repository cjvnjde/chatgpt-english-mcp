# English Learning MCP

A self-hosted MCP server for looking up English terms, maintaining a personal vocabulary list, and scheduling production-recall reviews with FSRS.

It provides ten tools for:

- Cambridge Dictionary lookups with permanent SQLite caching
- vocabulary metadata, offline word/expression usefulness with optional AI hints, notes, examples, tags, and learning states
- learning-step priority, a fixed new/mature-review mix, new-word usefulness weights, and recent-card cooldowns
- idempotent review recording and spaced-repetition scheduling
- timestamped presentation history retained for future learning analytics
- learned-word reinforcement weighted by usefulness, comments, difficulty, and recency, with a strict 25% per-word probability cap
- personal-interest priorities that favor interesting words without excluding less-important ones

The MCP stores and schedules learning data; the connected AI tutor decides how to explain, quiz, and respond to the learner.

The Compose stack includes [one-way AnkiWeb sync](docs/deployment.md#ankiweb-sync), publishing saved vocabulary to a dedicated managed deck. Server content overrides Anki edits; Anki scheduling remains independent.

## Marketplace listing

- **Name:** English Vocabulary
- **Description:** Look up English words and expressions, save the meanings you want to learn, and build lasting vocabulary with personalized spaced-repetition reviews and contextual practice.
- **MCP server ID:** `english-learning-mcp`

The server advertises this display name and description in its MCP initialization response. Use the name and description above when creating your OpenAI marketplace listing; repository metadata does not publish a listing automatically.

Connect using your public HTTPS `/mcp` URL and bearer-token authentication (`Authorization: Bearer <MCP_BEARER_TOKEN>`). Each deployment shares one vocabulary namespace among clients using its token; it does not provide separate accounts for marketplace users.

## Documentation

- [How it works: algorithms, formulas, diagrams, and workflows](docs/how-it-works.md)
- [MCP tool reference](docs/tools.md)
- [Suggested tutor and daily-review prompts](docs/prompts.md)
- [Configuration](docs/configuration.md)
- [Deployment](docs/deployment.md)
- [Development and architecture](docs/development.md)
- [Static admin UI, analytics, and SQLite export](admin/README.md)

## Quick start

```sh
cp .env.example .env
# Fill in the MCP token, Anki export token, and AnkiWeb credentials.
docker compose up -d --build
```

The Streamable HTTP endpoint is `/mcp` on internal port `8081`. Every MCP client must supply `MCP_BEARER_TOKEN`; there is no unauthenticated MCP listener. Route public access through an HTTPS reverse proxy.

For Dokploy, select `docker-compose.yml`, copy the variables from `.env.example` into Environment, fill in the required values, and deploy. No additional Compose file is needed.

The same Compose file includes the static admin UI. Set `ADMIN_BEARER_TOKEN` and route `/admin` on your MCP domain to `english-admin:80`, preserving the path. Keep `/mcp` routed to `english-learning-mcp:8081`. See the [admin setup guide](admin/README.md) for the exact Dokploy domain settings.

> This project retrieves data from Cambridge Dictionary and is not affiliated with or endorsed by Cambridge University Press & Assessment.
