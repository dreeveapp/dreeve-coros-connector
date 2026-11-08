# Releasing

Pushing a `vX.Y.Z` tag (or `vX.Y.Z-suffix`) runs `.github/workflows/release.yml`:

1. **build** pushes a `linux/amd64` + `linux/arm64` image to `ghcr.io/xballoy/dreeve-coros-connector` by digest only, without a tag.
2. **smoke** runs `TestContainerSmoke` against that digest on a native amd64 runner and a native arm64 runner (`CONTAINER_SMOKE_IMAGE`).
3. **publish** tags the same digest `X.Y.Z` and `X.Y`, plus `latest` when the tag is the highest stable version in the repository, checks that every tag points at it, and attests its build provenance. A backport such as `v1.2.4` after `v1.3.0` therefore leaves `latest` on `1.3.0`, on GHCR and on GitHub alike.
4. **release** creates the GitHub release with generated notes, the image reference and the `gh attestation verify` command.

A tag with a pre-release suffix (`v1.3.0-rc.1`) gets only its exact image tag and a pre-release on GitHub. If smoke fails, nothing is tagged: fix, then push a new version.

## Checklist

1. `main` is green on CI, and the commit to release is on `main`.
2. Release notes: note any change to the configuration, the volumes or the ledger, and anything users must do when they upgrade.
3. End-to-end with Dreeve, below.
4. Tag and push: `git tag vX.Y.Z && git push origin vX.Y.Z`.
5. First release only, as soon as `build` has pushed: the package is created private. In its settings on GitHub, make it public and check that it is linked to this repository, or `smoke` is the only job that can pull it.
6. When the workflow is green:
   - `docker buildx imagetools inspect ghcr.io/xballoy/dreeve-coros-connector:X.Y.Z` lists `linux/amd64` and `linux/arm64`, and `X.Y` shows the same digest, as does `latest` when this is the highest stable version. The two `unknown/unknown` entries are the SBOM attestations.
   - `docker buildx imagetools inspect ghcr.io/xballoy/dreeve-coros-connector:X.Y.Z --format '{{json .SBOM}}'` shows an SPDX document for each platform that lists `github.com/muktihari/fit`.
   - `gh attestation verify oci://ghcr.io/xballoy/dreeve-coros-connector:X.Y.Z --repo xballoy/dreeve-coros-connector` succeeds.

## End-to-end with Dreeve

Run Dreeve and the connector together from the README's compose example, against the fake COROS server serving a golden FIT file, in an empty directory outside the repository:

1. Copy the `docker-compose.yml` and the minimal `.env` from [Dreeve's installation guide](https://docs.dreeve.app/getting-started/installation/). Set `APP_SECRET` to any long string.
2. Paste the `coros-connector` service from the README's [Docker Compose](README.md#docker-compose) section under `services:`, unchanged. To test an unreleased commit, build it as that image: `docker build -t ghcr.io/xballoy/dreeve-coros-connector:latest <repository>`.
3. Start the fake COROS server, which prints the variables the connector needs. Pick a port no other process listens on, including on `127.0.0.1`: Docker Desktop routes `host.docker.internal` to the host's loopback.

   ```sh
   go build -o fakecoros <repository>/tools/fakecoros
   ./fakecoros -fixtures <repository>/testdata/coros -fit <repository>/testdata/fit/coros-outdoor-run.fit \
     -activities 1 -addr 0.0.0.0:18480 -advertise host.docker.internal > fake.env &
   ```

4. Point the connector at it with a `compose.override.yml`, holding the three variables from `fake.env`:

   ```yaml
   services:
     coros-connector:
       extra_hosts: ["host.docker.internal:host-gateway"]
       environment:
         COROS_API_HOSTS: "<from fake.env>"
         COROS_EMAIL: "<from fake.env>"
         COROS_PASSWORD: "<from fake.env>"
   ```

5. Follow the README from `mkdir` to `docker compose up -d coros-connector`, plus `docker compose up -d app daemon`.
6. `coros_900000000000000002.fit` appears in `./watch`, and `docker compose ps` shows the connector healthy.
7. `docker compose exec app bin/console app:import:files`, then open `/admin/file-imports`: the file is `success`, and the activity's title is the COROS title, `Demo run 1`.
8. `docker compose down`, and stop the fake.
