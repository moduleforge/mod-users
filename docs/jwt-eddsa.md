Purpose: Operator and integrator guide to the EdDSA (Ed25519) access-token carrier, the signing key and its persisted bootstrap, public-key-only verification, key rotation, and adoption by verify-only services.
---
# EdDSA access tokens and verify-only deployment

mod-users signs every locally-issued access token (sign-in, refresh, OIDC handoff, step-up, client credentials, impersonation) with an Ed25519 private key. EdDSA is the only algorithm: Ed25519 keys are small, signing and verification are fast and deterministic, the standard library supports it directly, and there are no parameter choices (curve, hash, padding) to get wrong. HS256 and the `JWT_ALG` switch of the earlier model are removed, so no shared secret can sign a session token and a service that only needs to *verify* tokens is given just the public key, so a compromise of that service cannot mint tokens.

A token is a **carrier**, not an authority: it names a server-side session (`sid`) or a non-interactive credential (`cid`) and the acting profile (`prof`), and mod-users re-checks the session or credential and the profile in the database on every request, taking the authoritative `acr` and `auth_time` from the session row. The claim set, the audience rules and the liveness check are described in [Sessions and tokens](./architecture/sessions-and-tokens.md#the-access-token); this document covers the key material.

## Claims

| Claim | Meaning |
|---|---|
| `iss` | `LOCAL_ISSUER` |
| `aud` | The session's app slug, else `AUTH_DEFAULT_AUDIENCE` (client credentials: `AUTH_DEFAULT_AUDIENCE` or the requested app slug). A token is accepted only if `aud` is in `AUTH_ACCEPTED_AUDIENCES` or, when that is empty, is the default audience or a registered, non-archived app's slug |
| `sub` | The principal uuid (under impersonation, the impersonated holder's) |
| `sid` or `cid` | The session uuid, or the credential uuid for a client-credentials token. Exactly one |
| `prof` | The acting profile uuid |
| `acr`, `amr`, `auth_time` | The session's assurance level (`"1"` to `"3"`), the factors' authentication methods, and when a factor was last proved. For a client-credentials token, the credential kind's base level, `["swk"]` and the issue time |
| `act` | `{"sub": "<administrator principal uuid>"}`, present only while impersonating |
| `iat`, `exp` | `exp` is the earlier of `iat` plus `AUTH_ACCESS_TOKEN_TTL` (default one hour, at most one hour) and the session's own expiry; a client-credentials token lives `AUTH_CLIENT_CREDENTIALS_TTL` (default 15 minutes) |

There are no `email`, `roles`, impersonation-subject or anonymity claims.

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `JWT_SIGNING_KEY` | unset | Ed25519 private key, PKCS#8 PEM. When unset, a key is generated on first boot and persisted in `mod_users.auth_signing_keys` (see "Persisted signing key") |
| `JWT_SIGNING_KID` | unset | Optional key id placed in the JWT `kid` header. Max 128 bytes of printable ASCII, no whitespace. Recommended so rotation works |
| `JWT_VERIFY_PUBLIC_KEYS` | unset | Additional public keys accepted for verification: either one PKIX PEM public key (matches tokens with no `kid` header) or a JSON object mapping `kid` to PKIX PEM (at most 32 keys). Used for rotation and for verify-only services |
| `JWT_SECRET` | unset | Optional explicit HMAC key for the OAuth `state` parameter of the OIDC flow only; when unset, that key is derived from the signing key (see "Derived OAuth state key"). It signs nothing else and is never accepted as an access-token verification key |
| `LOCAL_ISSUER` | `users-module-local` | The `iss` claim local tokens must carry |
| `AUTH_DEFAULT_AUDIENCE`, `AUTH_ACCEPTED_AUDIENCES` | `mod-users`, unset | The audience of a token whose session has no app, and the audiences bearer tokens may carry |
| `AUTH_ACCESS_TOKEN_TTL` | `1h` | Access-token lifetime; may not exceed one hour. The other `AUTH_*` lifetimes are listed in the [README](../README.md#configuration) |

All config problems are reported together at startup. Error messages never
include key material or the raw variable value.

### PEM format and the `\n` escape

- `JWT_SIGNING_KEY` must be a PKCS#8 block: `-----BEGIN PRIVATE KEY-----`.
- `JWT_VERIFY_PUBLIC_KEYS` entries must be PKIX blocks: `-----BEGIN PUBLIC KEY-----`.
- Multi-line values are awkward in `.env` files and many secret stores, so the
  two-character sequence `\n` (backslash, `n`) is accepted in place of each
  newline. Both forms are equivalent:

  ```
  JWT_SIGNING_KEY="-----BEGIN PRIVATE KEY-----\nMC4CAQAw...\n-----END PRIVATE KEY-----"
  ```

- In the JSON form, the PEM is a JSON string, so write `\n` inside the string
  (it is then decoded by JSON into a real newline), and quote the whole value
  for your shell or env file:

  ```
  JWT_VERIFY_PUBLIC_KEYS='{"2026-10":"-----BEGIN PUBLIC KEY-----\nMCow...\n-----END PUBLIC KEY-----"}'
  ```

## Generating a key pair

```sh
openssl genpkey -algorithm ed25519 -out signing.pem            # private, PKCS#8
openssl pkey -in signing.pem -pubout -out verify.pem           # public, PKIX
# one-line form for an env file:
awk 'NF {printf "%s\\n", $0}' signing.pem
```

Keep `signing.pem` in your secret store; never commit it. `verify.pem` is not
secret.

## Persisted signing key

When `JWT_SIGNING_KEY` is not supplied via the environment and a database URL
is resolved, `config.Load()` fetches the signing key from the single-row
`mod_users.auth_signing_keys` table, or generates an Ed25519 key and persists it
on first boot. This keeps zero-configuration local development working; a
production deployment can supply `JWT_SIGNING_KEY` from its secret store
instead, and an explicit key always wins and skips the database.

The bootstrap runs before the application's connection pool and this module's
migrations, so it opens its own short-lived connection (10 second timeout),
takes a Postgres advisory lock, creates the schema and table idempotently, and
reads back the winning row when concurrent first boots race. A persisted key
that does not parse fails startup loudly rather than being regenerated,
because regenerating would invalidate every outstanding access token; a
`JWT_SIGNING_KEY` that is set but invalid is reported and never silently
replaced by the persisted key; an unreachable database fails `config.Load()`.
Deployments that run several instances share the one persisted key through the
database. Replacing the persisted key means deleting the row, which invalidates
every outstanding access token and every in-flight OAuth `state`.

## Verification behavior

The accepted algorithm is pinned, never taken from the token header:

- A verifier accepts only `EdDSA` tokens, checked against the configured public
  keys. It rejects HS256 (including an HS256 token keyed with the public key
  bytes or PEM, the classic algorithm-confusion attack), `alg: none`, and every
  other algorithm.
- A token's `kid` must match a configured key exactly; an unknown or non-string
  `kid` is rejected. A token with no `kid` matches only a key configured without
  one (a single-PEM `JWT_VERIFY_PUBLIC_KEYS`).
- Issuer (`iss`) and expiry (`exp`, required) are checked on every path, and the
  token must have the carrier shape: a uuid `sub` and `prof`, exactly one of
  `sid` and `cid`, and a non-empty `aud`.

Audience tokens (`auth.IssueAudienceToken`) are the artefact for offline
verifiers such as a managed app acting as a relying party or a relay. They carry
the JWS header `typ: aud+jwt`, `iss`, `sub` (the principal uuid), `aud`, `iat`,
`exp` and caller-supplied extra claims, share the access-token signing key and
issuer, and carry no `sid`, `cid`, `prof`, `acr`, `amr` or `auth_time`. Offline
verifiers should check `typ` to tell them apart: the access-token
`Authenticator` rejects any token with `typ: aud+jwt`, so an audience token can
never be replayed as a bearer token against mod-users.

Go API (package `github.com/moduleforge/mod-users/api/auth`):
`NewSigner`, `NewLocalVerifier`, `NewVerifierWithOptions` with `WithOIDC`,
`WithInsecureSkipTLSVerify`, `WithLocalIssuer`, `WithEd25519PublicKey`,
`WithEd25519PublicKeys`, and `IssueAudienceToken`. The 5-argument `NewVerifier`
and `WithHS256Secret` are removed.

## Signer wiring for composing apps

An app that composes mod-users through the generated composition root gets the
wiring from `moduleforge.module.yaml`: the `signer` service (`auth.NewSigner`
over `cfg.LocalAuth`) is handed to `sessionService`, which mints every access
token, and the `verifier` service (`auth.NewLocalVerifier`) is handed to the
`authenticator`. An app that composes the services itself must hand the session
service the signer selected by its config and build the verifier from the same
config:

```go
signer, err := auth.NewSigner(cfg.LocalAuth) // the Ed25519 signing key
if err != nil { /* fail startup */ }
verifier, err := auth.NewLocalVerifier(ctx, cfg.LocalAuth) // the key's public half + JWT_VERIFY_PUBLIC_KEYS
if err != nil { /* fail startup */ }
authenticator := auth.NewAuthenticatorWithAppGate(pool, verifier, cfg.Auth, authorizer)
```

`NewSigner` rejects a nil or malformed key and an invalid `kid`. The OIDC
orchestrator (`auth.NewOAuth`) resolves its OAuth state key once from `cfg`
(`auth.OAuthStateKey`) at construction and fails startup when none can be
resolved.

## Derived OAuth state key

The OAuth `state` parameter of the OIDC flow is HMAC-signed, not asymmetric, and
needs no separate secret:

- **Precedence.** An explicit `JWT_SECRET` wins and is used as-is (no length
  policy change). A blank or whitespace-only `JWT_SECRET` is treated as unset.
  When it is unset, the key is derived from the signing key.
- **Derivation.** HKDF-SHA256 (RFC 5869): input keying material is the Ed25519
  key's 32-byte seed, no salt, 32-byte output, and the purpose label is the HKDF
  info input. The label is `mod-users/v1/oauth-state`. A key derived under any
  other label is independent. `v1` versions the scheme; the JWT `kid` is
  deliberately not an input.
- **Rotation behavior.** The derived key follows the signing key. When
  `JWT_SIGNING_KEY` is swapped, in-flight OAuth `state` fails verification and
  the user must restart the OIDC login. The window equals the state lifetime.
  Session tokens are unaffected (see "Rotation procedure"). Set an explicit
  `JWT_SECRET` for continuity across signing-key swaps.
- **Composing apps** that build the state signer themselves resolve the key with
  `auth.OAuthStateKey(cfg.LocalAuth)`, which fails with an error that never
  carries key material when no key can be resolved.
- **No other HMAC key exists.** The step-up token of the earlier model and its
  HMAC key are gone: step-up is a factor in the session, not a token (see
  [Step-up](./architecture/sessions-and-tokens.md#step-up)).

## Rotation procedure

1. Generate a new key pair and choose a new `kid`.
2. Add the **new public key** to `JWT_VERIFY_PUBLIC_KEYS` (JSON form, keyed by
   its `kid`) on every verifier (this service and any verify-only services).
   Deploy. Nothing signs with it yet.
3. Switch the signer: set `JWT_SIGNING_KEY` to the new private key and
   `JWT_SIGNING_KID` to the new `kid`. Move the *old* public key into
   `JWT_VERIFY_PUBLIC_KEYS` under its old `kid` so in-flight tokens stay valid
   (the signing key's own public half is added automatically).
4. After the longest token lifetime has elapsed (an access token lives at most
   one hour), remove the old public key from `JWT_VERIFY_PUBLIC_KEYS`. Its
   tokens are then rejected. (Removing it earlier also works, but immediately
   invalidates every still-unexpired token it signed; see "Revocation" below.)

Startup fails if a `JWT_VERIFY_PUBLIC_KEYS` entry reuses `JWT_SIGNING_KID` with
a different key.

Derived key: unless `JWT_SECRET` is set explicitly, step 3 also rotates the
derived OAuth state key, so OIDC logins in flight at the swap fail and must be
restarted (see "Derived OAuth state key"). Old public keys in
`JWT_VERIFY_PUBLIC_KEYS` do not help here: the state parameter is HMAC, not a
signature. Moving from a persisted key to an explicit `JWT_SIGNING_KEY` is the
same swap: put the persisted key's public half in `JWT_VERIFY_PUBLIC_KEYS`
(under the `kid` it signed with, or none) until its tokens have expired.

## Verify-only deployment and flow-accounts adoption

A service that trusts mod-users-issued tokens but never issues them (for
example flow-accounts) should hold only public keys:

```go
verifier, err := auth.NewVerifierWithOptions(ctx,
    auth.WithLocalIssuer("users-module-local"),          // must equal LOCAL_ISSUER of the issuer
    auth.WithEd25519PublicKeys(map[string]ed25519.PublicKey{"2026-10": pub}),
)
// verify the signature, issuer, expiry and audience offline
```

Notes for adopters:

- No HMAC secret exists, so the service cannot verify (or be tricked into
  verifying) HS256 tokens, and holds nothing that can mint tokens.
- A verify-only service checks only the token: signature, issuer, expiry and
  audience. It has no access to the sessions table, so it cannot see that a
  session was revoked, a principal disabled or a profile locked; it accepts an
  access token until `exp`. mod-users itself and any service that runs the
  module's `Authenticator` against the same database re-check the session on
  every request. A relying party that needs an offline-verifiable, audience-bound
  token should be issued an audience token (`auth.IssueAudienceToken`) with a
  short lifetime rather than be handed a user's access token.
- Distribute public keys by the same `kid` the issuer stamps in `JWT_SIGNING_KID`;
  follow the rotation procedure above, adding keys to verifiers *before* the
  issuer switches.
- The issuing mod-users instance sets `JWT_SIGNING_KEY` (or relies on the
  persisted key) and `JWT_SIGNING_KID`. A verify-only service holds no signing
  key, so it should build its verifier directly with the library options above
  rather than run the full mod-users config.
- Keep token lifetimes short; see "Revocation" below.

## Revocation

Two mechanisms revoke access, at different grain.

**Per session or credential.** Revoking a session (logout, an administrator or
user revoking it, a disabled principal, a revoked factor credential, refresh
reuse) or a client credential stops every access token of it on the next
request to any service that runs the module's per-request liveness check. This
is the normal way to end access, and it is immediate there; it is invisible to a
verify-only service, which sees only the token.

**Per key.** Removing a key from `JWT_VERIFY_PUBLIC_KEYS` is what revokes it
everywhere: once removed from every verifier, every token signed by that key is
rejected immediately, even if its `exp` has not passed. Under `EdDSA` the
signing key's own public half is accepted automatically for as long as it is
the configured (or persisted) signing key, so retiring the active key means
rotating first. Rotation step 4 waits for the longest token lifetime only to
avoid invalidating live sessions, not because removal would fail to take effect
earlier.

See also: [architecture](./architecture.md#configuration-and-secrets-bootstrap),
[spec security requirements](./mod-users-spec.md#security-requirements).
