# Upgrade Sub2API image and restore update support

## Goal

Upgrade the deployed Sub2API service to the latest Kiro-compatible fork revision while preserving the production database, configuration, and local migration compatibility fixes. Establish image replacement, rather than upstream binary self-replacement, as the supported update path.

## Background

- The server runs `sub2api-local:d483aefe7-mig145-157fix` through `/opt/sub2api/docker-compose.yml` and exposes container port 8080 as host port 20640.
- The in-app update request fails with `mkdir /app/.sub2api-update-*: permission denied` because the process runs as UID 1000 while `/app` is owned by root.
- Local `main` is `d483aefe7`; fork `origin/main` is `6ba76ea10` and already contains official `v0.1.176` plus Kiro-specific changes.
- Pure official `v0.1.176` removes the Kiro migration and Kiro from the platform quota constraint, so replacing the binary with an official release would regress this deployment.
- Existing uncommitted migration/checksum compatibility changes belong to the user and must survive the upgrade unchanged.

## Requirements

- Fast-forward the local codebase to `origin/main@6ba76ea10` without losing or committing unrelated user changes.
- Preserve the existing migration 145/157 compatibility changes and their tests.
- Make the root Dockerfile runtime ownership consistent with the release/deploy Dockerfiles so UID 1000 can create an update staging directory under `/app`.
- Add a regression check that fails on the current root Dockerfile and passes after the ownership fix.
- Build a Linux AMD64 image locally through the available WSL Docker environment with version `0.1.176` and a unique local tag.
- Transfer the image to `69.63.212.5:19371`, load it, update only the Sub2API Compose image reference, and recreate only the `sub2api` application container.
- Preserve PostgreSQL, Redis, named volumes, environment, port 20640, and restart policy.
- Keep the previously deployed image available for immediate rollback.

## Acceptance Criteria

- [x] Local source includes `origin/main@6ba76ea10` and the pre-existing migration compatibility patch.
- [x] The Dockerfile regression check demonstrates red before the fix and green after it.
- [x] Backend migration tests and relevant Docker packaging tests pass.
- [x] The new image builds successfully for `linux/amd64` and reports version `0.1.176`.
- [x] UID 1000 can create and remove `/app/.sub2api-update-*` inside a disposable container.
- [x] The server runs the new image and reports healthy without restarting PostgreSQL or Redis.
- [x] The existing host port 20640 and persistent volume mounts remain unchanged.
- [x] Recent container logs contain no migration checksum, startup, or permission errors.
- [x] The old image tag remains available and the rollback command is recorded.

## Out Of Scope

- Switching to the pure official image or official release binary, which would remove Kiro-specific behavior.
- Modifying production migration ledger rows or application data.
- Updating PostgreSQL, Redis, OpenResty, or unrelated containers.
- Pushing commits, tags, or images to GitHub or a public registry.

## Update Policy

Future upgrades must follow the Kiro-compatible fork and replace the Docker image. The in-app updater must not be used to install a pure upstream release unless the Kiro and migration compatibility changes have first landed in that release.

## Deployment Evidence

- Deployed image: `sub2api-local:6ba76ea10-v0.1.176-mig145-157fix`.
- Deployed image ID: `sha256:7fdc1662ded5288e98b71269c496a7bef42097f87fee413f6761be1add049e1f`.
- Compose backup: `/opt/sub2api/docker-compose.yml.bak-20260814T075502Z`.
- Public health check: `GET http://69.63.212.5:20640/health` returned HTTP `200` with `{"status":"ok"}`.
- PostgreSQL container remained `4f4457ef9bc1`; Redis remained `623beb1f2a4f`.
- Rollback command:

  ```sh
  cp --preserve=all /opt/sub2api/docker-compose.yml.bak-20260814T075502Z /opt/sub2api/docker-compose.yml
  cd /opt/sub2api
  docker compose up -d --no-deps sub2api
  ```
