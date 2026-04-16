# Phase 8, Task 1 — docker-compose dev loop validated

## Acceptance
- Run `make dev.start` from a clean clone; within 60s, all of the following work:
  - Postgres reachable, migrations applied via `make model.migrate.up`.
  - `curl /healthz` 200; `curl /readyz` 200.
  - Sign up via `/signup`, verify via mailhog, log in.
  - Sign in via Authelia OIDC.
  - Admin (first user) creates a second user; assigns them to a default app; revokes admin from someone else (with last-admin guard demonstrated).
  - Assume identity round-trip works; audit shows both actor and assumed.
- Add `make verify.smoke` that runs a small Go program (or shell + httpie) against the local stack performing the above and exits 0/1.

## How to verify
- A fresh clone + `make dev.start && make verify.smoke` succeeds end-to-end.

## Notes
- This is the gate for declaring the local mode "real". Document any required env in `.env.example`.
