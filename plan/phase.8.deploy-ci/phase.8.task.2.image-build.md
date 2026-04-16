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
- **[local]** `make build.images` produces images runnable via `docker run` against the local docker daemon.
- **[local, optional]** Push to `ttl.sh` (no auth, ephemeral) and `cosign verify` round-trip.
- **[draft-only]** Verification against a real registry (GCR/ECR) with keyless cosign deferred until cloud access is available.

## Notes
- For local execution, ko's `--local` flag loads the image directly into the docker daemon — no registry needed for development.
- The publish path (`make build.images.publish REGISTRY=…`) MUST be implemented and dry-runnable, but live publish to a private registry is out of scope for this delivery.
