# Implementation Plan

1. Snapshot Git status, tracked diff, untracked paths, current image tag, and server Compose application definition.
2. Add a Docker packaging regression test asserting the runtime application directory is writable by UID 1000; run it and confirm the expected failure.
3. Fast-forward local `main` to `origin/main@6ba76ea10` with a recoverable backup of user changes, resolve conflicts, and verify migration 145/157 fixes are unchanged.
4. Update the root Dockerfile ownership to match the release/deploy image behavior; rerun the regression test and packaging test suite.
5. Run focused backend migration tests, then broader relevant backend checks.
6. Build a uniquely tagged `linux/amd64` image in WSL Docker with version `0.1.176` and commit metadata `6ba76ea10`.
7. Verify the image starts, reports the expected version, and UID 1000 can create/remove an updater staging directory under `/app`.
8. Re-copy the encrypted SSH key, inspect and back up the current Compose file, export/upload/load the image, and update only the Sub2API image tag.
9. Recreate only the `sub2api` service with `--no-deps`; wait for healthy status and validate port 20640, mounts, process UID, version, and logs.
10. Preserve the old image and backup for rollback; remove local/server temporary archives and temporary credentials.

## Completion Evidence

- Docker ownership regression: passed.
- Docker runtime resources regression: passed.
- `go test ./internal/repository ./migrations -count=1`: passed.
- `pnpm run lint:check`: passed.
- `pnpm run build`: passed (`vue-tsc -b` and Vite production build).
- Production container: healthy on image `sub2api-local:6ba76ea10-v0.1.176-mig145-157fix`.
- Application update staging directory: writable as UID/GID `1000:1000`.
- Public health endpoint: HTTP `200`.
- Production logs: zero fatal, migration, checksum, or permission indicators after rollout.

## Validation Commands

- Docker packaging regression script under `deploy/tests/`
- Existing `deploy/tests/docker-runtime-resources-test.sh`
- Focused Go migration repository tests
- `docker buildx build --platform linux/amd64 --load ...`
- Disposable image permission test using `su-exec sub2api mktemp -d /app/.sub2api-update-*`
- Server-side `docker compose up -d --no-deps sub2api`, health inspection, and filtered startup/update logs

## Rollback Points

- Git patch and autostash before source synchronization.
- Existing server image `sub2api-local:d483aefe7-mig145-157fix`.
- Timestamped backup of `/opt/sub2api/docker-compose.yml` before changing the image tag.
