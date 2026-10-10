# azerothcore-discord

Minimal Go Discord status bot for AzerothCore realms: SOAP up/down + `.server info` metrics,
online player list from the characters MySQL database, mod-playerbots accounts filtered out.

## Files

| File | What |
| ---- | ---- |
| `README.md` | Full docs: config schema, env vars, build/run |
| `main.go` | Bot core: config load/reload (default `/data/config.json`, `-c` override, no-config startup), realm aggregation, Discord embeds, update loop |
| `main_test.go` | Config validation, ConfigManager reload, channel resolution tests |
| `internal/mysql/` | Characters DB queries + blacklist filtering (player list only) |
| `internal/soap/` | AzerothCore SOAP client (stdlib) + `.server info` parser |
| `config.json.example` | Config schema |
| `Containerfile` | Container image, static Go binary |
| `.env.example` | Env vars: `DISCORD_TOKEN`, `CHANNEL_ID` |

## Subdirectories

| Directory | What |
| --------- | ---- |
| `plans/` | Decision records per feature — see `plans/AGENTS.md` |

## Process
- All work on separate branches
- Before building anything non-trivial, use `pi-plans` to make a plan and review the plan before presenting it to the user
- Use relevant skills to review code before creating PRs
- After opening a PR on GitHub wait for external AI reviewers

## Build

```bash
go build -o bot .
```

## Test

```bash
go test -v ./...
```

## Run

```bash
export DISCORD_TOKEN="..." CHANNEL_ID="..."
go run . -c config.json          # default path: /data/config.json
```

## Format

```bash
gofmt -l .   # check
gofmt -w .   # fix
```
