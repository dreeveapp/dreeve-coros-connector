# Dreeve COROS connector

Downloads your completed COROS activities as FIT files into [Dreeve](https://github.com/dreeveapp)'s watch folder, so they get imported without manual exports.

One container serves one COROS account. Only completed activities with a FIT file are delivered; activities without a file are recorded and skipped.

## Commands

| Command | What it does |
| --- | --- |
| `login` | Logs in with `COROS_EMAIL` and `COROS_PASSWORD`, finds the account's home region, verifies the token, and saves it to `/tokens/token.json` (mode `0600`). Nothing is saved if any step fails. |
| `sync-once` | Lists your activities, downloads each new one as `coros_<labelId>.fit` into `/watch`, records it in `/state/ledger.json`, then exits. It never reads your password. |

**Logging in signs out every other COROS session on the account**, including COROS Training Hub and any other connector using that account. The mobile app is not affected. When the saved token is revoked, `sync-once` fails with an explicit message: run `login` again.

## Volumes

| Path | Contents |
| --- | --- |
| `/watch` | Dreeve's watch folder. Files appear atomically: written as `.coros_<labelId>.fit.part`, synced, then renamed. |
| `/state` | `ledger.json`: which activities were delivered. If it is corrupt, `sync-once` stops without touching it. |
| `/tokens` | `token.json`: the COROS access token, its regional host, region id and issue time. |

The image runs as a non-root user and works with a read-only root filesystem. When you set your own `user:`, make sure that user can write to the three volumes.

## Configuration

| Variable | Default | Used by | Meaning |
| --- | --- | --- | --- |
| `COROS_EMAIL` | | `login` | COROS account email |
| `COROS_PASSWORD` | | `login` | COROS account password |
| `SINCE` | `-30d` | `sync-once` | How far back the first sync goes: `-<days>d` or `YYYY-MM-DD`. Resolved once on the first run and stored in the ledger; later changes are ignored. |
| `LOOKBACK_DAYS` | `7` | `sync-once` | How many days each run lists again, to catch activities that sync to COROS late. A run also reaches back to the last completed sync if that is older. |
| `TZ` | `UTC` | `sync-once` | Optional IANA time zone, e.g. `Europe/Paris`, used to decide what "today" is when listing activities. Unset means UTC; an unknown zone is an error. |
| `WATCH_DIR`, `STATE_DIR`, `TOKENS_DIR` | `/watch`, `/state`, `/tokens` | both | Override the volume paths, mainly for running outside a container |
| `COROS_API_HOSTS` | COROS production hosts | both | Testing only: `1=<url>,2=<url>,3=<url>` region-to-host mapping |

## Docker Compose

```yaml
services:
  coros-connector:
    image: ghcr.io/xballoy/dreeve-coros-connector:latest
    user: "1000:1000"
    read_only: true
    volumes:
      - ./dreeve/watch:/watch
      - ./coros/state:/state
      - ./coros/tokens:/tokens
```

Log in once, then sync:

```sh
docker compose run --rm -e COROS_EMAIL=you@example.com -e COROS_PASSWORD='...' coros-connector login
docker compose run --rm coros-connector sync-once
```

Until the daemon mode lands, run `sync-once` from cron or a systemd timer.

## Development

See [`CLAUDE.md`](CLAUDE.md) for how to run each test level. Agent guidance is generated from `.rulesync/`; edit it there and run `npm ci --ignore-scripts && npx rulesync generate`.

## License

[AGPL-3.0](LICENSE)
