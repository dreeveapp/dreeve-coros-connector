---
root: false
targets: ["*"]
description: "How to record and redact COROS API fixtures"
globs: ["testdata/coros/**", "scripts/capture-coros-fixtures.sh"]
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
- timestamps: shifted by a constant so the latest activity starts on 2025-03-10
- `fileUrl`: `http://coros-files.test/<labelId>.fit`

Review every file by hand before committing, especially fields the script does not know about.

## Synthetic fixtures

A response that could not be recorded is named `*.synthetic.json` and its `message` says it is synthetic. Replace it with a recording as soon as one is available.
