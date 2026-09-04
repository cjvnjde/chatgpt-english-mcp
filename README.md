# English Learning MCP

A self-hosted MCP server for looking up English terms, maintaining a personal vocabulary list, and scheduling production-recall reviews with FSRS.

It provides eight tools for:

- Cambridge Dictionary lookups with permanent SQLite caching
- vocabulary metadata, notes, examples, tags, and learning states
- automatic selection of the next word to practise
- idempotent review recording and spaced-repetition scheduling

The MCP stores and schedules learning data; the connected AI tutor decides how to explain, quiz, and respond to the learner.

## Documentation

- [How it works and expected workflows](docs/how-it-works.md)
- [MCP tool reference](docs/tools.md)
- [Configuration](docs/configuration.md)
- [Deployment](docs/deployment.md)
- [Development and architecture](docs/development.md)

## Quick start

```sh
cp .env.example .env
# Fill in the required values in .env
docker compose up -d --build
```

The Streamable HTTP endpoint is `/mcp`. The Compose deployment exposes it to an OpenAI tunnel on internal port `8080` and to authenticated direct clients on internal port `8081`.

> This project retrieves data from Cambridge Dictionary and is not affiliated with or endorsed by Cambridge University Press & Assessment.
