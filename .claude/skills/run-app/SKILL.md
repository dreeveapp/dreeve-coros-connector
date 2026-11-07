---
name: run-app
description: >-
  Builds and runs the connector against a local fake COROS server to observe a
  change working and capture terminal output as proof, without touching a real
  COROS account. Use when asked to run, try or demo the connector, or when the
  pr-content skill needs proof. Covers the binary and the container image.
allowed-tools: >-
  Read, Bash(go build:*), Bash(go run:*), Bash(docker:*), Bash(mkdir:*),
  Bash(ls:*), Bash(cat:*), Bash(kill:*)
---
# Run the connector

The connector is a CLI with no UI, so proof is terminal output plus the files it leaves in the volumes. Screenshots and videos do not apply.

Never run it against the real COROS API to get proof: every `login` signs out Training Hub and every other connector on the account. Use the fake COROS server in `tools/fakecoros`, which replays the recorded fixtures in `testdata/coros/`. Only use the real API when the user explicitly asks and runs the command themselves.

## 1. Prepare a workspace

Use the session scratchpad, never the repository:

```bash
S=<scratchpad>/run-app
rm -rf "$S" && mkdir -p "$S/watch" "$S/state" "$S/tokens"
go build -o "$S/dreeve-coros-connector" ./cmd/dreeve-coros-connector
go build -o "$S/fakecoros" ./tools/fakecoros
```

## 2. Start the fake COROS server

Run it in the background from the repository root. It prints the environment the connector needs, then serves until killed:

```bash
"$S/fakecoros" -activities 3 -without-file 1 > "$S/fake.env" 2> "$S/fake.log" &
```

Flags:

| Flag | Default | Effect |
| --- | --- | --- |
| `-activities` | `3` | activities served, one per day starting yesterday |
| `-without-file` | `0` | how many of the oldest activities get the synthetic "no file" response |
| `-region` | `1` | region id reported at login |
| `-home-region` | `-region` | region whose host accepts the token; set it different from `-region` to reproduce a wrong-region login |
| `-addr` | `127.0.0.1:18080` | listen address |
| `-advertise` | | host name the connector uses to reach the fake, for the container run |

Wait until `fake.env` contains `COROS_PASSWORD` before continuing.

## 3. Drive the binary

```bash
set -a; . "$S/fake.env"; WATCH_DIR="$S/watch"; STATE_DIR="$S/state"; TOKENS_DIR="$S/tokens"; set +a
"$S/dreeve-coros-connector" login; echo "exit=$?"
"$S/dreeve-coros-connector" sync-once; echo "exit=$?"
ls -l "$S/watch" "$S/tokens"
cat "$S/state/ledger.json"
```

Run whatever sequence shows the change: a second `sync-once` to show nothing is delivered twice, a wrong `COROS_PASSWORD` for the bad-credentials error, `-home-region 3` for the wrong-region error, a corrupted `ledger.json` for the corrupt-ledger error.

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

Paste the commands and their output in a fenced block: exit codes, delivered file names and modes, and the relevant part of the ledger. Trim paths to the scratchpad prefix if they are long. Never paste a token from a real account.
