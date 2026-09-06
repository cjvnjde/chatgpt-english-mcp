# English Learning MCP

A self-hosted MCP server for looking up English terms, maintaining a personal vocabulary list, and scheduling production-recall reviews with FSRS.

It provides eight tools for:

- Cambridge Dictionary lookups with permanent SQLite caching
- vocabulary metadata, notes, examples, tags, and learning states
- weighted selection mixing new words and due reviews, with recent-word cooldowns
- idempotent review recording and spaced-repetition scheduling
- timestamped presentation history retained for future learning analytics

The MCP stores and schedules learning data; the connected AI tutor decides how to explain, quiz, and respond to the learner.

The Compose stack includes [one-way AnkiWeb sync](docs/deployment.md#ankiweb-sync), publishing saved vocabulary to a dedicated managed deck. Server content overrides Anki edits; Anki scheduling remains independent.

## Documentation

- [How it works and expected workflows](docs/how-it-works.md)
- [MCP tool reference](docs/tools.md)
- [Suggested tutor and daily-review prompts](docs/prompts.md)
- [Configuration](docs/configuration.md)
- [Deployment](docs/deployment.md)
- [Development and architecture](docs/development.md)

## Quick start

```sh
cp .env.example .env
# Fill in the tunnel, MCP token, Anki export token, and AnkiWeb credentials.
docker compose up -d --build
```

The Streamable HTTP endpoint is `/mcp`. The Compose deployment exposes it to an OpenAI tunnel on internal port `8080` and to authenticated direct clients on internal port `8081`.

For Dokploy, select `docker-compose.yml`, copy the variables from `.env.example` into Environment, fill in the required values, and deploy. No additional Compose file is needed.

> This project retrieves data from Cambridge Dictionary and is not affiliated with or endorsed by Cambridge University Press & Assessment.
