Purpose: Operator and integrator guide to EdDSA (Ed25519) JWT signing, public-key-only verification, key rotation, and adoption by verify-only services.
---
# EdDSA JWT signing and verify-only deployment

mod-users signs locally-issued JWTs (login, register, email-code, assume-user)
with either a shared HMAC secret (HS256, the default) or an Ed25519 private key
(EdDSA). EdDSA is the asymmetric algorithm chosen for this module: Ed25519 keys
are small, signing and verification are fast and deterministic, the standard
library supports it directly, and there are no parameter choices (curve, hash,
padding) to get wrong. A service that only needs to *verify* tokens can then be
given just the public key, so a compromise of that service cannot mint tokens.

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `JWT_ALG` | `HS256` | Signing algorithm for locally-issued tokens: `HS256` or `EdDSA`. Any other value fails startup. |
| `JWT_SIGNING_KEY` | unset | Ed25519 private key, PKCS#8 PEM. Required when `JWT_ALG=EdDSA`. |
| `JWT_SIGNING_KID` | unset | Optional key id placed in the JWT `kid` header. Max 128 bytes of printable ASCII, no whitespace. Recommended with EdDSA so rotation works. |
| `JWT_VERIFY_PUBLIC_KEYS` | unset | Additional public keys accepted for verification: either one PKIX PEM public key (matches tokens with no `kid` header) or a JSON object mapping `kid` to PKIX PEM (at most 32 keys). Used for rotation and for verify-only services. |
| `JWT_SECRET` | required | Still required in every mode: it signs OAuth state and step-up tokens, and it is the HS256 secret under `JWT_ALG=HS256`. Under `EdDSA` it is never accepted as a session-token verification key. |
| `LOCAL_ISSUER` | `users-module-local` | The `iss` claim local tokens must carry. |

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

## Verification behavior

The accepted algorithm is pinned by the configured key material, never taken
from the token header:

- A verifier holding only public keys accepts only `EdDSA` tokens. It rejects
  HS256 (including an HS256 token keyed with the public key bytes or PEM, the
  classic algorithm-confusion attack), `alg: none`, and every other algorithm.
- A verifier holding only an HMAC secret accepts only `HS256` and rejects EdDSA.
- A token's `kid` must match a configured key exactly; an unknown or non-string
  `kid` is rejected. A token with no `kid` matches only a key configured without
  one (a single-PEM `JWT_VERIFY_PUBLIC_KEYS`).
- Issuer (`iss`) and expiry (`exp`, required) are checked on every path.

**Behavior change:** the legacy `auth.NewVerifier(ctx, issuerURL, clientID,
jwtSecret, localIssuer)` called with an empty `jwtSecret` now accepts *no* HS256
tokens (previously an empty HMAC key was usable). Callers relying on that were
verifying with a publicly-guessable key and must supply a real secret.

Go API (package `github.com/moduleforge/mod-users/api/auth`):
`NewSigner`, `NewLocalVerifier`, and `NewVerifierWithOptions` with `WithOIDC`,
`WithHS256Secret`, `WithLocalIssuer`, `WithEd25519PublicKey`,
`WithEd25519PublicKeys`.

## Signer wiring for composing apps

An app that composes mod-users handlers itself (rather than running
`cmd/server`) must hand every token-minting handler the signer selected by its
config. A handler built without a signer falls back to HS256 keyed by the
`jwtSecret` it was constructed with, which is wrong under `JWT_ALG=EdDSA`:

```go
signer, err := auth.NewSigner(cfg.LocalAuth) // HS256 or EdDSA per JWT_ALG
if err != nil { /* fail startup */ }

// Preferred: constructor variants that require a signer (nil is an error).
authHandler, err := authhandlers.NewWithSigner(pool, queries, coreQ, signer, cfg.LocalAuth.LocalIssuer, sender, guiBase)
assumeHandler, err := handlers.NewAssumeHandlerWithSigner(uaSvc, signer, cfg.LocalAuth.LocalIssuer)

// Equivalent for the existing constructors:
//   authHandler.SetSigner(signer); assumeHandler.SetSigner(signer)
```

`NewHS256Signer` (and so the no-signer fallback) rejects an empty secret. The
OIDC handler builds its signer once from `cfg` at construction; call
`oidcHandler.SignerErr()` after construction to fail startup on a bad config.

## Rotation procedure

1. Generate a new key pair and choose a new `kid`.
2. Add the **new public key** to `JWT_VERIFY_PUBLIC_KEYS` (JSON form, keyed by
   its `kid`) on every verifier (this service and any verify-only services).
   Deploy. Nothing signs with it yet.
3. Switch the signer: set `JWT_SIGNING_KEY` to the new private key and
   `JWT_SIGNING_KID` to the new `kid`. Move the *old* public key into
   `JWT_VERIFY_PUBLIC_KEYS` under its old `kid` so in-flight tokens stay valid
   (the signing key's own public half is added automatically).
4. After the longest token lifetime has elapsed, remove the old public key
   from `JWT_VERIFY_PUBLIC_KEYS`. Its tokens are then rejected. (Removing it
   earlier also works, but immediately invalidates every still-unexpired token
   it signed; see "Revocation" below.)

Startup fails if a `JWT_VERIFY_PUBLIC_KEYS` entry reuses `JWT_SIGNING_KID` with
a different key.

## Verify-only deployment and flow-accounts adoption

A service that trusts mod-users-issued tokens but never issues them (for
example flow-accounts) should hold only public keys:

```go
verifier, err := auth.NewVerifierWithOptions(ctx,
    auth.WithLocalIssuer("users-module-local"),          // must equal LOCAL_ISSUER of the issuer
    auth.WithEd25519PublicKeys(map[string]ed25519.PublicKey{"2026-10": pub}),
)
// pass verifier to auth.RequireAuth(verifier, mapper, resolver)
```

Notes for adopters:

- No HMAC secret is configured, so the service cannot verify (or be tricked
  into verifying) HS256 tokens, and holds nothing that can mint tokens.
- Distribute public keys by the same `kid` the issuer stamps in `JWT_SIGNING_KID`;
  follow the rotation procedure above, adding keys to verifiers *before* the
  issuer switches.
- The issuing mod-users instance sets `JWT_ALG=EdDSA`, `JWT_SIGNING_KEY`, and
  `JWT_SIGNING_KID`. A mod-users process that itself loads config via
  `config.Load()` still needs `JWT_SECRET` (OAuth state and step-up), so a
  verify-only service should build its verifier directly with the library
  options above rather than run the full mod-users config.
- Keep token lifetimes short; see "Revocation" below.

## Revocation

Removing a key from `JWT_VERIFY_PUBLIC_KEYS` is what revokes it: once removed
from every verifier, every token signed by that key is rejected immediately,
even if its `exp` has not passed. This holds equally after reverting to HS256:
under `JWT_ALG=HS256` the verifier still accepts EdDSA tokens for every key in
`JWT_VERIFY_PUBLIC_KEYS`, so reverting `JWT_ALG` does not revoke the old keys -
remove them from `JWT_VERIFY_PUBLIC_KEYS` explicitly (on verify-only services
too). Under `EdDSA`, the signing key's own public half is accepted
automatically for as long as it is the configured `JWT_SIGNING_KEY`.

What key removal cannot do is revoke an individual token while its key stays
trusted: there is no per-token revocation list, so a single leaked token stays
valid until `exp`. Rotation step 4 therefore waits for the longest token
lifetime only to avoid invalidating live sessions, not because removal would
fail to take effect earlier.

See also: [architecture](./architecture.md#configuration-and-secrets-bootstrap),
[spec security requirements](./mod-users-spec.md#security-requirements).
