# ntfy realm up/down notifications

Realm offline/online transitions are announced both in Discord (plain text, no mentions) and via
[ntfy](https://docs.ntfy.sh) push notifications.

## Decisions

- **ntfy toggled in config, not Discord commands** — per-realm `"ntfy": {"enabled": ..., "events": [...]}`
  with a top-level server/token block (`"server"` defaults to `https://ntfy.sh`). Keep the surface
  minimal at this stage; Discord-managed toggles were rejected as extra state and command plumbing
  nobody asked for yet.
- **Topics auto-derived from realm names** — slug of the name (lowercase, `[a-z0-9_-]`, collapsed
  `-`, max 64, trimmed). Explicit per-realm topic config was rejected as one more field to get
  wrong; validation rejects empty and colliding slugs. README documents that topics are guessable
  and that ntfy.sh reserves short/generic topics — unguessable realm names or a self-hosted server
  are the mitigation.
- **2-consecutive-probe debounce** — per-realm state on the bot (settled state + diff streak,
  keyed by realm name, survives config reloads). First probe is a silent baseline; a transition
  needs two consecutive differing probes, so one blipped probe never notifies. No-debounce was
  rejected as too noisy against flaky SOAP links.
- **Event choice gates ntfy only** — Discord event messages are always sent for every realm;
  `events` filters only ntfy publishes.
- **Event-message channel resolution** — realm `notify_channel_id` → top-level `notify_channel_id`
  → existing status-channel chain (realm `channel_id` → top-level `channel_id` → `CHANNEL_ID` env);
  empty resolution skips the message with one warning.
- **Failure handling** — ntfy publish and Discord send errors are logged and skipped, never fatal;
  ntfy uses its own 10s-timeout HTTP client (worst case N×10s per cycle accepted for small realm
  counts).
- **Startup cleanup keeps event messages** — `cleanupOldMessages` deletes only messages with
  embeds, so plain-text event messages survive restarts.
- **No new dependencies** — `net/http` only, mirroring the stdlib SOAP client.
