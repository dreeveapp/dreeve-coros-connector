# Security policy

## Supported versions

Until `1.0.0`, security fixes go to the latest `0.Y` release only, published as a new patch version. Pull the `0.Y` tag, or `latest`, to receive them.

## Reporting a vulnerability

Report it privately through [GitHub's private vulnerability reporting](https://github.com/dreeveapp/dreeve-coros-connector/security/advisories/new). Do not open a public issue or pull request.

Include the image version (`docker compose run --rm coros-connector version`), what an attacker can do, and the steps to reproduce it. Never include a real COROS password or token: redact them, or reproduce with the fake COROS server described in [CONTRIBUTING.md](CONTRIBUTING.md#running-the-connector-by-hand).

The connector has a single maintainer, so reports are handled on a best-effort basis. You will be told whether the report is accepted, and credited in the advisory unless you ask not to be.

## Scope

In scope:

- The COROS password or token leaking: into logs, files, the image or `/status`
- Files the connector writes to `/watch`, `/state` or `/tokens`: their permissions, paths or contents
- A crafted COROS response or FIT file making the connector misbehave
- The published image and its build provenance

Out of scope:

- COROS's own services and Dreeve itself: report those to their maintainers
- Logging in signing out every other COROS session, which is how the COROS API works and is documented on the [COROS connector page](https://docs.dreeve.app/connectors/coros-connector/)
