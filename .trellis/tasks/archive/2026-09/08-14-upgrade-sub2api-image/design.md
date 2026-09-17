# Design

## Upgrade Baseline

Use `origin/main@6ba76ea10`. It contains official `v0.1.176` and subsequent upstream commits while retaining the fork's Kiro implementation. Reapply the current working-tree migration compatibility patch after the fast-forward.

## Docker Packaging Fix

The application updater creates its staging directory beside `/app/sub2api`. The root Dockerfile currently owns only `/app/data` and the binary as `sub2api`; the directory remains root-owned. Match `deploy/Dockerfile` and `Dockerfile.goreleaser` by recursively assigning `/app` to `sub2api` before copying the root-owned entrypoint.

The entrypoint remains root-owned and executable, starts as root to repair mounted data ownership, and then uses `su-exec` to run the application as UID 1000.

## Source Preservation

Before updating the branch, save a binary Git patch of tracked changes and inventory untracked paths. Fast-forward with Git autostash or an equivalent reversible procedure, then verify the exact migration diffs remain. Do not stage or commit unrelated `.agents`, `.omx`, `.pi`, or `.trellis` files.

## Image And Rollout

Build a uniquely tagged Linux AMD64 image in WSL Docker. Export it as a compressed image archive, transfer it over SSH, and load it on the server. Back up `/opt/sub2api/docker-compose.yml`, change only the application image tag, then run Compose for the `sub2api` service without dependencies.

## Verification

Verify source tests, image permissions, embedded version, server health, container identity, mounts, port mapping, and startup logs. Retain the old image and Compose backup until verification is complete.

## Rollback

Restore the prior Compose file or image tag `sub2api-local:d483aefe7-mig145-157fix`, recreate only `sub2api`, and recheck health. Named volumes are never deleted or recreated.
