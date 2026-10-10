# Contributing to azerothcore-discord

Thank you for your interest in contributing! This guide will help you set up local development.

## Prerequisites

- **Go 1.27+** - [Install Go](https://golang.org/doc/install)
- **Git** - [Install Git](https://git-scm.com/)
- **Discord Bot Token** - See [Discord Bot Setup](#discord-bot-setup) below
- **Optional**: Docker/Podman for containerized testing

## Discord Bot Setup

1. Go to [Discord Developer Portal](https://discord.com/developers/applications)
2. Click "New Application" and give it a name
3. Navigate to **Bot** → **Add Bot**
4. Under **Privileged Gateway Intents**, enable:
   - Message Content Intent
5. Click "Reset Token" to generate a token → save this as `DISCORD_TOKEN`
6. Go to **OAuth2** → **URL Generator**
   - Scopes: `bot`
   - Bot Permissions: `Send Messages`, `Manage Messages`, `Embed Links`
7. Use the generated URL to invite the bot to your server

## Local Development

```bash
cp .env.example .env    # edit: add DISCORD_TOKEN and CHANNEL_ID
go run . -c config.json
```

## Testing

```bash
go test -v ./...
```

## Commit Message Format

We use [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <subject>

<body>
<footer>
```

Examples:
- `feat(bot): add per-realm channel routing`
- `fix(soap): tolerate missing queue line in .server info`
