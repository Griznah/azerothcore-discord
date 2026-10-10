# Docker / Podman

Multi-stage build: `golang:1.27.2-alpine` builder compiles a static Go binary, which runs in an `alpine:3.24` runtime image.

**Note:** We prefer `podman` for local development. All commands below use `podman`, but you can use `docker` as an alias if needed. The CI/CD pipeline uses Docker for production builds.

## Build

```bash
podman build -t azerothcore-discord .
```

## Security

This container runs as a non-root user (UID 1001) following container security best practices. At runtime, the application will immediately refuse to start if run as root (UID 0).  Make sure the bot user (UID 1001) can read config.json; see troubleshooting below if startup fails on file access.

### User Details
- **Username**: bot
- **UID/GID**: 1001/1001
- **Shell**: /sbin/nologin (no login access)
- **Permissions**: Read-only access to application code

The bot requires no file write permissions as it only reads configuration and connects to Discord, SOAP, and MySQL.

### Verify Non-Root Execution

```bash
podman exec azerothcore-discord whoami
# Should output: bot
```

## Run

### Configuration via Volume Mount

The bot loads `config.json` from the data directory (`/data/`) at startup.
Host can edit server configuration via volume mount without container rebuild.

**Option 1: Mount single config file (recommended - simpler)**
```bash
# Create config file on host
mkdir -p /path/to/config
cp config.json.example /path/to/config/config.json
nano /path/to/config/config.json

# Ensure non-root container user (UID 1001) can read
sudo chown 1001:1001 /path/to/config/config.json
sudo chmod 644 /path/to/config/config.json

# Run with file mount
podman run -d \
  --name azerothcore-discord \
  -e DISCORD_TOKEN="your_bot_token_here" \
  -e CHANNEL_ID="your_channel_id" \
  -v /path/to/config/config.json:/data/config.json:ro \
  --restart unless-stopped \
  azerothcore-discord
```

**Option 2: Mount working directory (for multiple config files)**
```bash
# Create directory with config file inside
mkdir -p /path/to/config
cp config.json.example /path/to/config/config.json
nano /path/to/config/config.json
sudo chown -R 1001:1001 /path/to/config
sudo chmod 755 /path/to/config

# Run with directory mount
podman run -d \
  --name azerothcore-discord \
  -e DISCORD_TOKEN="your_bot_token_here" \
  -e CHANNEL_ID="your_channel_id" \
  -v /path/to/config:/data:ro \
  --restart unless-stopped \
  azerothcore-discord
```

The `:ro` flag makes the mount read-only for additional security. To edit configuration:
1. Edit config file on host
2. The bot detects the change via mtime polling and reloads automatically within one update cycle (default 30s) — no restart needed
3. Restart the container only to force an immediate reload: `podman restart azerothcore-discord`

## Using Docker Compose

Create `docker-compose.yml`:

```yaml
services:
  bot:
    build: .
    container_name: azerothcore-discord
    environment:
      - DISCORD_TOKEN=${DISCORD_TOKEN}
      - CHANNEL_ID=${CHANNEL_ID}
    volumes:
      - ./config.json:/data/config.json:ro
    restart: unless-stopped
```

Then create a `.env` file with your credentials and run:

```bash
podman-compose up -d
```

## Logs

```bash
podman logs -f azerothcore-discord
```

## Stop

```bash
podman stop azerothcore-discord
podman rm azerothcore-discord
```

## Troubleshooting

### Rootless Podman: UID/GID Mapping Errors

If you see errors like:
```
cannot find UID/GID for user: no subuid ranges found for user "username" in /etc/subuid
potentially insufficient UIDs or GIDs available in user namespace
```

This means your user lacks subuid/subgid ranges required for rootless containers that create users (like Alpine images).

**Fix:**

1. **Add subuid/subgid ranges** (requires root access):
```bash
# Check current ranges (should show lines for your username)
cat /etc/subuid
cat /etc/subgid

# If empty, add ranges (replace YOUR_USERNAME with your actual username)
sudo usermod --add-subuids 100000-165535 YOUR_USERNAME
sudo usermod --add-subgids 100000-165535 YOUR_USERNAME

# Verify
cat /etc/subuid | grep YOUR_USERNAME
cat /etc/subgid | grep YOUR_USERNAME
# Should output: YOUR_USERNAME:100000:65536
```

2. **Reset Podman** to apply changes:
```bash
podman system migrate
```

3. **Rebuild**:
```bash
podman build -t azerothcore-discord .
```

**Note**: This is a one-time setup per user. Each user who runs rootless Podman needs their own subuid/subgid ranges.

### Permission Denied Errors

If the bot cannot read config.json:

```bash
# Check container user ID
podman exec azerothcore-discord id
# Output: uid=1001(bot) gid=1001(bot)

# Fix host file permissions (for single file mount)
sudo chown 1001:1001 /path/to/config/config.json
# OR for directory mount:
sudo chown -R 1001:1001 /path/to/config
```

### Config File Not Found

Check container logs for the absolute path being searched:

```bash
podman logs azerothcore-discord | grep "Loading config"
```

Common issues:
- Volume mount path must match: `/data/config.json` (file) or `/data/` (directory)
- Host path must be absolute (not relative to current directory)
- Config file name must be exactly `config.json` (case-sensitive)
