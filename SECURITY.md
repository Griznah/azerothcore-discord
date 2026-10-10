# Security Guide & Credential Rotation

## Security Contact

- Primary: [maintainer@example.com](mailto:maintainer@example.com)
- For urgent security notifications, add an ADMIN or published email here

## Credential Rotation & Incident Runbook

### When to Rotate Secrets
- On suspected or confirmed compromise/leak (token or config committed or observed in logs)
- When an operator leaves or changes role
- At each scheduled quarterly review

### How to Rotate DISCORD_TOKEN
1. Generate a new token in the Discord Developer Portal (**Bot** → **Reset Token**)
2. Set `DISCORD_TOKEN` in your `.env` file, environment, or secret store
3. Restart the bot (or roll the deployment)
4. Confirm the old token is no longer valid

### How to Rotate Database/SOAP Credentials
1. Rotate the password on the MySQL accounts and the worldserver SOAP user
2. Update `characters_dsn` / `auth_dsn` / `soap` in `config.json` — the bot reloads it without a
   restart, or roll the deployment secret
3. Confirm the next status update succeeds (watch the logs)

## Security Checklist

- [ ] `DISCORD_TOKEN`, DSNs, and SOAP credentials are never committed (`.env`, `config.json` real
      values stay out of git; examples use placeholders only)
- [ ] Log output is checked for secrets before being shared (the bot redacts common patterns, but
      verify)
- [ ] Container runs as non-root (enforced at startup)

## Reporting a Vulnerability

Please report privately to the security contact above. Do not open public issues for
vulnerabilities.
