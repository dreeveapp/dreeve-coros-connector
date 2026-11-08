# Dreeve COROS connector

Downloads your completed COROS activities as FIT files into [Dreeve](https://github.com/dreeveapp)'s watch folder, so they get imported without manual exports. One container serves one COROS account.

## Install

Images are published to `ghcr.io/xballoy/dreeve-coros-connector` for `linux/amd64` and `linux/arm64`, tagged `X.Y.Z`, `X.Y` (latest patch) and `latest` (highest stable version). Each [release](https://github.com/xballoy/dreeve-coros-connector/releases) shows how to verify its signed build provenance.

## Docker Compose

Add the connector next to the `app` and `daemon` services in [Dreeve's `docker-compose.yml`](https://docs.dreeve.app/getting-started/installation/#docker-composeyml), mounting the same `./watch` folder:

```yaml
  coros-connector:
    image: ghcr.io/xballoy/dreeve-coros-connector:latest
    container_name: dreeve-coros-connector
    user: "1000:1000"
    read_only: true
    restart: unless-stopped
    volumes:
      - ./watch:/watch
      - ./coros/state:/state
      - ./coros/tokens:/tokens
```

`user:` must be able to write to the three folders. Use your own uid and gid (`id -u`, `id -g`), or Dreeve's `PUID` and `PGID` if you set them. Create the folders and give them to that user before the first start, since Docker would otherwise create them owned by root:

```sh
mkdir -p watch coros/state coros/tokens
sudo chown 1000:1000 watch coros/state coros/tokens
```

Log in once, then start the connector:

```sh
docker compose run --rm -it coros-connector login
docker compose up -d coros-connector
```

Activities appear in `./watch` and Dreeve's daemon imports them on its next run.

## Logging in

> [!WARNING]
> Logging in signs out every other COROS session on the account, including COROS Training Hub and any other connector. The mobile app is not affected.

`login` reads `COROS_EMAIL` and `COROS_PASSWORD`, or their `_FILE` variants, and prompts in a terminal for whatever is missing. The password is never logged or saved. If COROS reports a region the connector does not know, set `COROS_REGION`. With Docker secrets:

```yaml
services:
  coros-connector:
    environment:
      COROS_EMAIL_FILE: /run/secrets/coros_email
      COROS_PASSWORD_FILE: /run/secrets/coros_password
    secrets: [coros_email, coros_password]
secrets:
  coros_email:
    file: ./coros/email.txt
  coros_password:
    file: ./coros/password.txt
```

## Upgrading

```sh
docker compose pull coros-connector
docker compose up -d coros-connector
```

No new `login` is needed and no activity is delivered twice. Read the [release notes](https://github.com/xballoy/dreeve-coros-connector/releases) first: until `1.0.0`, a minor version may change the configuration or the volumes, so pin the `0.Y` tag to receive only fixes. To roll back, set the previous exact version as the `image:` tag.

## Commands

| Command | What it does |
| --- | --- |
| `run` | Default. Syncs at startup, then every `POLL_INTERVAL`, and serves `/healthz` and `/status` |
| `sync-once` | Syncs once, then exits |
| `login` | Logs in to COROS and saves the token |
| `healthcheck` | Exits 0 when `/healthz` is healthy; the image's `HEALTHCHECK` runs it |
| `version` | Prints the image's version |

## Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `COROS_EMAIL`, `COROS_PASSWORD` | | Account credentials, read only by `login` |
| `COROS_EMAIL_FILE`, `COROS_PASSWORD_FILE` | | Files holding them, such as Docker secrets |
| `COROS_REGION` | | `us`, `eu` or `cn`, when COROS reports a region the connector does not know |
| `POLL_INTERVAL` | `3600` | Seconds between syncs, `900` to `604800`, randomised by ±10% |
| `SINCE` | `-30d` | How far back the first sync goes: `-<days>d` or `YYYY-MM-DD` |
| `LOOKBACK_DAYS` | `7` | Days each sync lists again, for activities that reach COROS late |
| `MAX_DOWNLOADS_PER_CYCLE` | `25` | Downloads per sync; the rest wait for the next one |
| `DOWNLOAD_DELAY_SECONDS` | `2` | Pause between downloads |
| `MAX_BACKOFF_SECONDS` | `21600` | Longest wait after repeated HTTP 429 responses |
| `MAX_ATTEMPTS` | `5` | Failed downloads before an activity is given up |
| `WRITE_TITLE` | `true` | Write the COROS activity name into each FIT file so Dreeve shows it |
| `DRY_RUN` | `false` | Log what would be delivered and write nothing |
| `TZ` | `UTC` | Time zone that decides what "today" is |
| `UMASK` | `022` | Permission bits removed from every file written |
| `HTTP_ADDR` | `:8080` | Health server address |
| `LOG_LEVEL`, `LOG_FORMAT` | `info`, `text` | `debug`/`info`/`warn`/`error`, and `text` or `json` |

## How it works

- The ledger in `/state` records every activity, so nothing is delivered twice, even across restarts and upgrades.
- Files appear in `/watch` atomically, as `coros_<labelId>.fit`, only once fully downloaded and checked to be an intact FIT file.
- A failed download is retried on later syncs and recorded as `failed` after `MAX_ATTEMPTS`. Activities without a FIT file are recorded as `no-file`.
- When COROS answers HTTP 429, the connector backs off from 15 minutes, doubling up to `MAX_BACKOFF_SECONDS`, and keeps that backoff across restarts.
- With `WRITE_TITLE=true`, the COROS activity name is appended to the file as a `workout` message, unless the file already has one. Nothing COROS wrote is changed.
- Passwords and tokens are redacted from every log line.

## Health and troubleshooting

| Endpoint | Response |
| --- | --- |
| `/healthz` | `200` when healthy, `503` while the token is rejected, missing or unreadable |
| `/status` | JSON with `healthy`, `token`, `lastCycle` and the ledger's `counts` |

- **Unhealthy**: the token was revoked, usually by a login elsewhere. Run `login` again; the running container picks it up on its next sync.
- **Fails at startup with a uid and gid**: the volumes are not writable by `user:`. Fix their owner.
- **Retry a `failed` or `no-file` activity**: stop the connector, delete its entry from `ledger.json`, start it again.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

[AGPL-3.0](LICENSE)
