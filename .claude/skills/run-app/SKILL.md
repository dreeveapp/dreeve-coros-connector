---
name: run-app
description: >-
  Builds and runs the connector against a local fake COROS server to observe a
  change working and capture terminal output as proof, without touching a real
  COROS account. Use when asked to run, try or demo the connector, check
  /healthz or /status, reproduce an error (bad credentials, wrong region, 429
  backoff, retries, corrupt ledger), or when the pr-content skill needs proof.
  Covers the binary and the container image.
allowed-tools: >-
  Read, Bash(go build:*), Bash(go run:*), Bash(docker:*), Bash(mkdir:*),
  Bash(ls:*), Bash(cat:*), Bash(kill:*)
---
# Run the connector

The connector is a CLI with no UI, so proof is terminal output plus the files it leaves in the volumes. Screenshots and videos do not apply.

Never run it against the real COROS API to get proof: every `login` signs out Training Hub. Use the fake in `tools/fakecoros`; only use the real API when the user explicitly asks and runs the command themselves.

## 1. Prepare a workspace

Use the session scratchpad, never the repository:

```bash
S=<scratchpad>/run-app
rm -rf "$S" && mkdir -p "$S/watch" "$S/state" "$S/tokens"
go build -o "$S/dreeve-coros-connector" ./cmd/dreeve-coros-connector
go build -o "$S/fakecoros" ./tools/fakecoros
```

## 2. Start the fake COROS server

Run it in the background from the repository root, because it reads `testdata/coros` relative to the working directory; from anywhere else, pass `-fixtures <repository>/testdata/coros`. It prints the environment the connector needs, then serves until killed:

```bash
"$S/fakecoros" -activities 3 -without-file 1 > "$S/fake.env" 2> "$S/fake.log" &
```

`"$S/fakecoros" -h` lists the flags. `-home-region` different from `-region` reproduces a wrong-region login, and `-fit` picks the FIT file every activity serves.

Wait until `fake.env` contains `COROS_PASSWORD` before continuing.

`fake.log` gets one line per request the fake receives, with the endpoint, region, label id and any injected fault. Quote it when the proof is about what reached COROS, such as nothing during a backoff, or one download per activity.

## 3. Drive the binary

```bash
set -a; . "$S/fake.env"; WATCH_DIR="$S/watch"; STATE_DIR="$S/state"; TOKENS_DIR="$S/tokens"; set +a
"$S/dreeve-coros-connector" login; echo "exit=$?"
"$S/dreeve-coros-connector" sync-once; echo "exit=$?"
ls -l "$S/watch" "$S/tokens"
cat "$S/state/ledger.json"
```

For the daemon, start `run` in the background with a short interval, then query its endpoints:

```bash
POLL_INTERVAL_TESTING=2s HTTP_ADDR=127.0.0.1:18081 "$S/dreeve-coros-connector" run > "$S/run.log" 2>&1 &
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:18081/healthz
curl -s http://127.0.0.1:18081/status
HTTP_ADDR=127.0.0.1:18081 "$S/dreeve-coros-connector" healthcheck; echo "exit=$?"
```

Run whatever sequence shows the change. Recipes for the usual cases:

- **Nothing delivered twice**: a second `sync-once`.
- **Bad credentials**: a wrong `COROS_PASSWORD` on `login`.
- **Wrong region**: start the fake with `-home-region 3`; `login` fails and `tokens/` stays empty.
- **Corrupt ledger**: edit `ledger.json` by hand, then `sync-once`.
- **Corrupt download**: `-fit` pointing at any file that is not FIT (an HTML page will do), with `MAX_ATTEMPTS=2`: two `sync-once` runs end the activity as `failed` and `watch/` stays empty.
- **Retries and missing files**: `-without-file 1` with a low `MAX_ATTEMPTS`.
- **429 backoff**: `-throttle 1`. The first backoff is 15 minutes, too long to watch, but `MAX_BACKOFF_SECONDS` caps every backoff, so set it to about 20 to show the whole story: the 429, `backoff` in `ledger.json`, a `sync-once` refused with "no request sent", then a delivery once it has passed.
- **Pacing and caps**: `MAX_DOWNLOADS_PER_CYCLE`, and `DOWNLOAD_DELAY_TESTING` (a Go duration) when `DOWNLOAD_DELAY_SECONDS`' minimum of 1 is too slow.

Some paths cannot be triggered from the command line, such as a failure while `login` verifies its new token. The acceptance tests drive the real binary with injected faults for those (`go test -count=1 -v -run '<TestName>' ./acceptance/`); say in the proof that this is how they were shown.

## 4. Container run

When the change touches the image, run the same flow in the container as an arbitrary non-root user with a read-only root filesystem:

```bash
"$S/fakecoros" -addr 0.0.0.0:18080 -advertise host.docker.internal > "$S/fake.env" 2> "$S/fake.log" &
chmod 777 "$S/watch" "$S/state" "$S/tokens"
docker build -t dreeve-coros-connector:dev .
docker run --rm --read-only --user 4242:4242 --add-host host.docker.internal:host-gateway \
  --env-file "$S/fake.env" -v "$S/watch:/watch" -v "$S/state:/state" -v "$S/tokens:/tokens" \
  dreeve-coros-connector:dev login
```

Then the same `docker run` with `sync-once`.

## 5. Clean up

Kill the fake server (`kill %1` or its pid) and leave the scratchpad as it is.

## Proof

Paste the commands and their output in a fenced block: exit codes, delivered file names and modes, and the relevant part of the ledger. Show each command line as it was run and its output as it came out; a reader trusts copied output, not a summary of it. Trim paths to the scratchpad prefix if they are long, and cut uninteresting lines rather than rewording them. Never paste a token from a real account.

Then say plainly what the run did not show, so nobody reads more into it than it proves.
