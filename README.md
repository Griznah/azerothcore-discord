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
- Confirmed realm up/down transitions post a plain event message to Discord and (opt-in per realm)
  an ntfy push notification — see [Realm up/down notifications](#realm-updown-notifications)

## Configuration

Config path defaults to `/data/config.json`; override with `-c <path>`. If the file is missing the
bot starts and waits for it to appear.

```json
{
  "channel_id": "1234567890123456789",
  "update_interval": 30,
  "blacklist_account_prefixes": ["RNDBOT"],
  "ntfy": {
    "server": "https://ntfy.sh",
    "token": ""
  },
  "notify_channel_id": "",
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
      "player_list_cap": 25,
      "notify_channel_id": "1234567890123456791",
      "ntfy": {
        "enabled": true,
        "events": ["offline", "online"]
      }
    }
  ]
}
```

| Key | Meaning |
| --- | --- |
| `channel_id` | Fallback Discord channel for realms without their own `channel_id` |
| `update_interval` | Seconds between refreshes (min 1) |
| `blacklist_account_prefixes` | Account username prefixes hidden from the player list (default `["RNDBOT"]`) |
| `ntfy.server` | ntfy server URL for push notifications, default `https://ntfy.sh` (self-hosted or ntfy.sh) |
| `ntfy.token` | Optional ntfy access token, sent as `Authorization: Bearer <token>` |
| `notify_channel_id` | Default channel for realm up/down event messages |
| `realms[].soap` | worldserver SOAP (remote access) endpoint — see the [AzerothCore wiki](https://www.azerothcore.org/wiki/remote-access) |
| `realms[].characters_dsn` | MySQL DSN for the realm's characters database |
| `realms[].auth_dsn` | MySQL DSN for the auth database (account lookups for the blacklist) |
| `realms[].channel_id` | Optional per-realm Discord channel |
| `realms[].player_list_cap` | Optional max listed players, default 25, remainder shown as `+N more` |
| `realms[].notify_channel_id` | Optional per-realm override of the event-message channel |
| `realms[].ntfy.enabled` | Enable ntfy push notifications for this realm (default `false`) |
| `realms[].ntfy.events` | Which transitions to push, any of `"offline"`, `"online"` (default both) |

DSNs follow the [go-sql-driver/mysql](https://github.com/go-sql-driver/mysql) DSN format:
`user:password@tcp(host:3306)/database`.

## Realm up/down notifications

When a realm's SOAP probe result changes, the bot posts a plain event message (no mention, no
embed) to the resolved notify channel and — when `realms[].ntfy.enabled` is set — publishes an
ntfy notification:

- Discord offline: `🔴 **<realm>** is offline`; online:
  `🟢 **<realm>** is back online — <n> players connected`
- ntfy: title `<realm> offline` / `<realm> online`, same text as body without Markdown, priority
  `high`/`default`, tags `warning`/`tada`

Details:

- **Debounce** — the first probe after startup is a silent baseline; a transition is only
  confirmed after **two consecutive** differing probes, so a single blipped probe never notifies.
- **Event channel resolution** — realm `notify_channel_id` → top-level `notify_channel_id` → the
  status channel chain (realm `channel_id` → top-level `channel_id` → `CHANNEL_ID` env). A realm
  whose event channel resolves empty is skipped with a warning. `ntfy.events` filters only ntfy
  pushes; Discord event messages are always sent.
- **Topic derivation** — the ntfy topic is a slug of the realm name: lowercase, runes outside
  `[a-z0-9_-]` become `-`, runs collapse, truncated to 64 characters, leading/trailing `-`
  trimmed. `"My Realm"` → `my-realm`. Config validation rejects realms that derive an empty or
  colliding topic.
- **Topic security** — anyone who knows a topic name can subscribe to it on public servers
  (topic ≈ password). Prefer unguessable realm names, or run a [self-hosted ntfy
  server](https://docs.ntfy.sh/install/) with access control (`ntfy.token`). Also note ntfy.sh
  reserves short/generic topics (e.g. `up`, `down`) — derived topics like those are rejected by
  the server at publish time.
- Event messages survive restarts: startup cleanup only deletes the bot's embed messages.

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
