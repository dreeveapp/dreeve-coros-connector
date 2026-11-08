---
root: false
targets: ["*"]
description: "How to record and redact COROS API fixtures and golden FIT files"
globs: ["testdata/coros/**", "testdata/fit/**", "testdata/dreeve/**", "scripts/capture-coros-fixtures.sh", "internal/fitscrub/**", "tools/fitscrub/**"]
---

# COROS fixtures

`testdata/coros/` holds redacted responses recorded from the real COROS API. `http.json` records the HTTP status and content type of every recorded call. COROS returns HTTP 200 for every result code, including `1019` and `1030`.

## Recording

Only a human with a COROS account can record fixtures. Run:

```
./scripts/capture-coros-fixtures.sh
```

It prompts for the email and password (or reads `COROS_EMAIL` and `COROS_PASSWORD`), logs in three times, and writes redacted fixtures. The logins sign out Training Hub and every running connector on that account. Set `MANUAL_LABEL_ID` and `MANUAL_SPORT_TYPE` to also record the download response for an activity without a file.

## Redaction

A fixture must not let anyone identify the account. The script enforces this and fails if an identifier it knows about is still present:

- token, email, user id, nickname, country, device id: fixed placeholder values
- login data: only the fields the connector needs are kept
- `labelId`: remapped to `9000000000000000NN`, consistently across files
- activity names: `Activity N`
- activity metrics (heart rate, distance, pace, calories, power, ...): `0`
- image URLs (route thumbnails): empty
- timestamps: the latest activity starts on 2025-03-10, each earlier one exactly a day before, each lasting one hour
- `count` and `totalPage`: describe only the captured activities
- `fileUrl`: `http://coros-files.test/<labelId>.fit`

Review every file by hand before committing, especially fields the script does not know about.

## Synthetic fixtures

A response that could not be recorded is named `*.synthetic.json` and its `message` says it is synthetic. Replace it with a recording as soon as one is available.

## Golden FIT files

`testdata/fit/` holds real activity files exported from COROS, named `coros-<activity>.fit`. Only a human with a COROS account can export them, from Training Hub or with `sync-once` into a folder outside the repository.

Never commit a raw export. Scrub it first:

```
go run ./tools/fitscrub -origin 48.8584,2.2945 raw.fit testdata/fit/coros-<activity>.fit
```

`fitscrub` patches values in place and recomputes the CRCs, so every other byte stays as COROS served it. It never re-encodes the file:

- positions (every `semicircles` field): translated so the first position lands on the origin, keeping the track's shape; missing positions stay missing
- `serial_number` fields: a fixed placeholder
- `user_profile`: every field cleared

It refuses a file that is truncated, fails its CRC, or does not decode. It does not touch developer fields: check their names in the `field_description` messages and that none holds a position before committing. Timestamps, heart rate and the device model stay as recorded.

### Adding a golden file

1. Scrub the export as above and save it as `testdata/fit/coros-<activity>.fit`.
2. Run `go test ./internal/fit ./internal/fittitle ./acceptance` and `DREEVE_ORACLE=1 go test -count=1 -run TestDreeveOracle ./acceptance`. Every test that globs `testdata/fit/*.fit` picks the file up; no test needs editing.
3. Record on issue #1 anything the file reveals about COROS exports, such as a `workout` message or chained FIT files.

Dreeve's own `fit-tool`, built by the oracle test, is the source of truth for what Dreeve reads from a file. When `muktihari/fit` in this module and the oracle disagree, the oracle wins: fix the connector, not the oracle test.
