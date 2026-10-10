# azerothcore-discord

Minimal Discord status bot for [AzerothCore](https://www.azerothcore.org/) World of Warcraft servers.

For each configured realm the bot publishes a status embed field:

- **Up/down** — SOAP probe via `.server info` (worldserver remote access)
- **Uptime and queue** — from `.server info` output (omitted when the core build does not report them)
- **Online players** with level and class — from the realm's `characters` MySQL database

mod-playerbots random-bot accounts (username prefix `RNDBOT`, configurable) and their characters are
excluded from the player list; the embed shows counts as **real / total** (filtered / unfiltered).

## How it works

- One status message per Discord channel; each message has one embed field per realm
- Realms are grouped by their resolved channel: per-realm `channel_id` → top-level `channel_id` →
  `CHANNEL_ID` environment variable
- The bot edits its existing message on every refresh and recreates it if it was deleted
- Configuration is reloaded automatically when `config.json` changes on disk (no restart needed)

## Configuration

Config path defaults to `/data/config.json`; override with `-c <path>`. If the file is missing the
bot starts and waits for it to appear.

```json
{
  "channel_id": "1234567890123456789",
  "update_interval": 30,
  "blacklist_account_prefixes": ["RNDBOT"],
  "realms": [
    {
      "name": "My Realm",
      "soap": {
        "host": "127.0.0.1",
        "port": 7878,
        "user": "SOAP_USER",
        "pass": "SOAP_PASSWORD"
      },
      "characters_dsn": "user:password@tcp(127.0.0.1:3306)/acore_characters",
      "auth_dsn": "user:password@tcp(127.0.0.1:3306)/acore_auth",
      "channel_id": "1234567890123456790",
      "player_list_cap": 25
    }
  ]
}
```

| Key | Meaning |
| --- | --- |
| `channel_id` | Fallback Discord channel for realms without their own `channel_id` |
| `update_interval` | Seconds between refreshes (min 1) |
| `blacklist_account_prefixes` | Account username prefixes hidden from the player list (default `["RNDBOT"]`) |
| `realms[].soap` | worldserver SOAP (remote access) endpoint — see the [AzerothCore wiki](https://www.azerothcore.org/wiki/remote-access) |
| `realms[].characters_dsn` | MySQL DSN for the realm's characters database |
| `realms[].auth_dsn` | MySQL DSN for the auth database (account lookups for the blacklist) |
| `realms[].channel_id` | Optional per-realm Discord channel |
| `realms[].player_list_cap` | Optional max listed players, default 25, remainder shown as `+N more` |

DSNs follow the [go-sql-driver/mysql](https://github.com/go-sql-driver/mysql) DSN format:
`user:password@tcp(host:3306)/database`.

## Environment variables

| Variable | Meaning |
| --- | --- |
| `DISCORD_TOKEN` | Bot token (required) |
| `CHANNEL_ID` | Fallback channel when neither per-realm nor top-level `channel_id` is set |

`.env` in the working directory is loaded automatically if present (never commit real secrets —
see `config.json.example` and `.env.example` for placeholders).

## Build and run

```bash
go build -o bot .
export DISCORD_TOKEN="..." CHANNEL_ID="..."
./bot -c config.json
```

## Test

```bash
go test -v ./...
```

## Deployment

The repo ships a `Containerfile` (static Go binary, non-root user) published as
`ghcr.io/griznah/azerothcore-discord`. See [PODMAN.md](PODMAN.md) for local container usage.
