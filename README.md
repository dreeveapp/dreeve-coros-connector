# Dreeve COROS connector

Downloads your completed COROS activities as FIT files into [Dreeve](https://github.com/dreeveapp)'s watch folder, so they get imported without manual exports.

One container serves one COROS account. Only completed activities with a FIT file are delivered; activities without a file are retried, then recorded and skipped.

## Commands

| Command | What it does |
| --- | --- |
| `login` | Logs in with `COROS_EMAIL` and `COROS_PASSWORD`, finds the account's home region, verifies the token, and saves it to `/tokens/token.json` (mode `0600`). Nothing is saved if any step fails. |
| `sync-once` | Checks the volumes (see [Startup checks](#startup-checks)), lists your activities, downloads each new one as `coros_<labelId>.fit` into `/watch`, records it in `/state/ledger.json`, then exits. It never reads your password. It exits non-zero when COROS throttles it or while it is still backing off. |
| `run` | The image's default. Checks the volumes, runs a sync cycle at startup, then every `POLL_INTERVAL` seconds with a random ±10% jitter, and serves `/healthz` and `/status` on `HTTP_ADDR`. It reads `/tokens/token.json` again on every cycle and never logs in by itself. |
| `healthcheck` | Exits 0 when `/healthz` on `HTTP_ADDR` answers 200, non-zero otherwise. The image's `HEALTHCHECK` runs it. |

## Rate limits and retries

A sync never hammers COROS, and a failed download is never lost:

- Each cycle downloads at most `MAX_DOWNLOADS_PER_CYCLE` activities, oldest first, and waits at least `DOWNLOAD_DELAY_SECONDS` after one download finishes before starting the next. A large backfill is delivered over several cycles.
- When COROS answers HTTP 429, the cycle stops at once and the connector backs off: 15 minutes after the first 429, doubling after each consecutive one, never more than `MAX_BACKOFF_SECONDS`. No request reaches COROS until the backoff elapses, including after a restart, because the backoff is stored in the ledger. The first cycle that completes clears it.
- A download that fails with a network error, a timeout, an HTTP error or a truncated file is recorded as `pending` and retried on the next cycle. After `MAX_ATTEMPTS` failures it is recorded as `failed` and no longer retried.
- A download that COROS answers with a result code other than `0000` is logged with that code and retried the same way. After `MAX_ATTEMPTS` it is recorded as `no-file` with the code.
- A file is written to `/watch` only once it has been downloaded completely. If the process is killed while writing, at most a hidden `.coros_<labelId>.fit.part` file is left behind; the next start removes it and delivers the activity.

To retry a `failed` or `no-file` activity, delete its entry from `ledger.json` while the connector is stopped.

**Logging in signs out every other COROS session on the account**, including COROS Training Hub and any other connector using that account. The mobile app is not affected. When the saved token is revoked, `sync-once` fails with an explicit message: run `login` again.

## Startup checks

Before contacting COROS, `sync-once` and `run`:

- check that `/watch` and `/state` are writable by creating and removing a hidden `.write-probe-*` file, and otherwise exit non-zero with the path, the process's uid and gid, and the error;
- remove every `.coros_*.fit.part` file left in `/watch` by an interrupted delivery, logging each one. Other files are never touched.

## Dry run

With `DRY_RUN=true`, `sync-once` and `run` list your activities and log each one that would be delivered (`dry run: would deliver`, with its file name), then a summary. They skip the startup checks and write nothing: no file in `/watch`, no `.part` cleanup, no ledger change (not even a 429 backoff), so a dry run also works on read-only volumes. Download links are not requested. The token must still be valid, and an existing backoff is still respected. Every activity that is not yet settled is listed, including those beyond `MAX_DOWNLOADS_PER_CYCLE` (`thisCycle=false`). Under `run`, each cycle lists the same activities again, since nothing is recorded, and `/status` keeps reporting the ledger's counts as they were.

## Logs

Every log line goes to stderr; stdout stays empty except for `help`. `LOG_FORMAT=json` writes one JSON object per line with `time`, `level` and `msg`, plus fields such as `labelId`, `file` or `error`; the default `text` writes `key=value` lines. `LOG_LEVEL` keeps records at that level and above: `debug` adds the token's host, region and age, the listed date range and the activities already settled.

The COROS password and access token are replaced with `[REDACTED]` wherever they would appear in a log line, at every level, as is the MD5 hash of the password sent to COROS and the value of any field named `token`, `accessToken`, `password` or `pwd`.

## Health

When COROS rejects the saved token, or `token.json` is absent or not valid JSON, `run` keeps running, logs the error with the token's age, and reports unhealthy. Run `login` again: the next cycle picks up the new token and the container turns healthy, without a restart. Network and COROS errors, and HTTP 429 backoffs, are logged, reported as the last cycle's `error`, and retried on a later cycle without changing health. A corrupt ledger, a volume that cannot be read or written, or an invalid `SINCE` on the first run stops `run` with a non-zero exit. A non-writable `/watch` or `/state` stops it at startup, before the health server starts.

| Endpoint | Response |
| --- | --- |
| `/healthz` | `200` when healthy, `503` while the token is rejected, missing or unreadable |
| `/status` | JSON: `healthy`, `token` (`unknown`, `valid`, `rejected`, `missing` or `unreadable`), `lastCycle` (`startedAt`, `finishedAt`, `error`) and `counts` of ledger entries for every status, including zeros |

## Volumes

| Path | Contents |
| --- | --- |
| `/watch` | Dreeve's watch folder. Files appear atomically: written as `.coros_<labelId>.fit.part`, synced, then renamed. Their mode is `0666` minus `UMASK` (`0644` by default). |
| `/state` | `ledger.json`: each activity's status (`delivered`, `pending`, `failed` or `no-file`, with the attempt count and last error or COROS result code) and any current backoff. If it is corrupt, `sync-once` stops without touching it. Its mode is `0666` minus `UMASK`. |
| `/tokens` | `token.json`: the COROS access token, its regional host, region id and issue time. Its mode is `0600` minus `UMASK`, so it is never readable by others. |

The image runs as a non-root user and works with a read-only root filesystem. When you set your own `user:`, make sure that user can write to the three volumes.

## Configuration

| Variable | Default | Used by | Meaning |
| --- | --- | --- | --- |
| `COROS_EMAIL` | | `login` | COROS account email |
| `COROS_PASSWORD` | | `login` | COROS account password |
| `POLL_INTERVAL` | `3600` | `run` | Seconds between sync cycles, from `900` to `604800` (one week). Each wait is randomised by ±10%. A value outside that range stops `run` at startup. |
| `HTTP_ADDR` | `:8080` | `run`, `healthcheck` | Address the health server listens on, and that `healthcheck` queries (an empty or wildcard host means `127.0.0.1`) |
| `SINCE` | `-30d` | `sync-once`, `run` | How far back the first sync goes: `-<days>d` or `YYYY-MM-DD`. Resolved once on the first run and stored in the ledger; later changes are ignored. |
| `LOOKBACK_DAYS` | `7` | `sync-once`, `run` | How many days each run lists again, to catch activities that sync to COROS late. A run also reaches back to the last completed sync, or to the oldest activity still waiting for a retry, if that is older. |
| `MAX_DOWNLOADS_PER_CYCLE` | `25` | `sync-once`, `run` | Most activities downloaded in one cycle; the rest wait for the next cycle |
| `DOWNLOAD_DELAY_SECONDS` | `2` | `sync-once`, `run` | Least number of seconds between the end of one download and the start of the next, at least `1` |
| `MAX_BACKOFF_SECONDS` | `21600` | `sync-once`, `run` | Longest wait after consecutive HTTP 429 responses, from `1` to `604800` (one week) |
| `MAX_ATTEMPTS` | `5` | `sync-once`, `run` | Failed downloads of one activity before it is recorded as `failed` or `no-file` |
| `TZ` | `UTC` | `sync-once`, `run` | Optional IANA time zone, e.g. `Europe/Paris`, used to decide what "today" is: it sets the day `SINCE=-<days>d` counts back from, how a `YYYY-MM-DD` `SINCE` is read, and the days listed from COROS. Unset means UTC; an unknown zone is an error. |
| `UMASK` | `022` | all | Octal permission bits removed from every file written: FIT files and the ledger start from `0666`, the token from `0600`. For example `002` makes FIT files `0664` and `027` makes them `0640`. A value that is not octal or above `777` is an error. |
| `DRY_RUN` | `false` | `sync-once`, `run` | `true` lists what would be delivered and writes nothing (see [Dry run](#dry-run)) |
| `LOG_LEVEL` | `info` | all | `debug`, `info`, `warn` or `error` |
| `LOG_FORMAT` | `text` | all | `text` or `json` (one object per line) |
| `WATCH_DIR`, `STATE_DIR`, `TOKENS_DIR` | `/watch`, `/state`, `/tokens` | all | Override the volume paths, mainly for running outside a container |
| `COROS_API_HOSTS` | COROS production hosts | all | Testing only: `1=<url>,2=<url>,3=<url>` region-to-host mapping |
| `POLL_INTERVAL_TESTING` | | `run` | Testing only: a Go duration such as `200ms` that replaces `POLL_INTERVAL` and bypasses its minimum |
| `DOWNLOAD_DELAY_TESTING` | | `sync-once`, `run` | Testing only: a Go duration that replaces `DOWNLOAD_DELAY_SECONDS` and bypasses its minimum |
| `HTTP_TIMEOUT_TESTING` | `60s` | `sync-once`, `run` | Testing only: a Go duration for the COROS request timeout |

## Docker Compose

```yaml
services:
  coros-connector:
    image: ghcr.io/xballoy/dreeve-coros-connector:latest
    user: "1000:1000"
    read_only: true
    restart: unless-stopped
    volumes:
      - ./dreeve/watch:/watch
      - ./coros/state:/state
      - ./coros/tokens:/tokens
```

Log in once, then start the daemon:

```sh
docker compose run --rm -e COROS_EMAIL=you@example.com -e COROS_PASSWORD='...' coros-connector login
docker compose up -d
```

`docker compose ps` shows the container's health. If it turns unhealthy because the token was revoked, run the `login` command again; the running container resumes on its next cycle.

## Development

See [`CLAUDE.md`](CLAUDE.md) for how to run each test level. Agent guidance is generated from `.rulesync/`; edit it there and run `npm ci --ignore-scripts && npx rulesync generate`.

## License

[AGPL-3.0](LICENSE)
