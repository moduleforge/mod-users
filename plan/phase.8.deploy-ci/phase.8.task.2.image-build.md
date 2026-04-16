# Phase 8, Task 2 — ko image build + cosign signing + SBOM

## Acceptance
- `api/.ko.yaml` configured: distroless base, multi-arch (amd64, arm64), reproducible builds.
- `gui/Dockerfile` (Next.js standalone output → distroless-node) since ko is Go-only. Multi-arch via buildx.
- `make build.images` builds both images locally.
- `make build.images.publish REGISTRY=…` publishes signed images:
  - cosign keyless (`COSIGN_EXPERIMENTAL=1`) sign each image after push.
  - syft SBOM attached as a cosign attestation.
- README in `deploy/` documents how to verify a published image (`cosign verify ... --certificate-identity-regexp …`).

## How to verify
- `make build.images` produces images runnable via `docker run`.
- After a publish, `cosign verify <ref>` succeeds.

## Notes
- For local registry testing, point at `ttl.sh` — no auth, ephemeral.
