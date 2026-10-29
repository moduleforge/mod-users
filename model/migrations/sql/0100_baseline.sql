-- +goose Up

-- mod-users baseline schema.
--
-- Rewritten in place, pre-release, for the identity model (mod-core
-- docs/architecture/identity-model.md): users as a principal subtype,
-- identifiers, credentials with per-kind subtables, challenges, party
-- authentication policies, credential authorizations, sessions with factors and
-- refresh lineages, the persisted signing key, the availability and liveness
-- functions, and the authorization engine's owning-party gate
-- (session_meets_party_policy). The former 0101 to 0104 migrations are folded in. A
-- database created from an earlier baseline must be dropped and recreated
-- (make clean.data); there is no in-place upgrade path.
--
-- One schema rule: every mod-users table, function and trigger function lives
-- in the mod_users schema and is referenced schema-qualified. Objects owned by
-- mod-core and mod-authz are referenced public.-qualified. Every function is
-- SECURITY INVOKER and pinned to `search_path = pg_catalog, pg_temp`, so no
-- caller's search_path (including mod-core's SECURITY DEFINER functions that
-- write public.profiles) and no session-temporary relation can redirect a name.
-- The one exception is the public.accessible_user_ids_for_actor stub at the end,
-- which keeps the shape of mod-core's stubs because the access-function
-- generator replaces it at startup.
--
-- SQLSTATE contract raised by this file (Go helpers live in usersservice):
--   MF101  session invariant violation
--   MF102  credential invariant violation
--   MF103  user invariant violation
-- Unique violations stay standard 23505; these index names are part of the
-- contract: user_identifiers_kind_value_uq, cred_oidc_active_issuer_subject_uq,
-- cred_ssh_key_active_fingerprint_uq.
--
-- Requires mod-core (principals, profiles, legal_entities, natural_persons,
-- apps, profile_policy_confirmations, type_is_or_descends_from,
-- profile_own_subject, set_updated_at); the manifest's migrations.after: [core] orders it.

CREATE SCHEMA IF NOT EXISTS mod_users;

-- ===========================================================================
-- Credential kinds
-- ===========================================================================

-- credential_kinds is registry data: one row per credential kind, each with a
-- per-kind subtable (cred_<kind>) below. base_acr is the NIST AAL a single
-- factor of the kind provides; factor is the class mod_users.session_acr counts
-- toward multi-factor assurance; service_allowed marks the kinds a service
-- account may hold (non-interactive ones only). Reserved, not seeded: totp,
-- passkey, mtls, federated_jwt.
CREATE TABLE mod_users.credential_kinds (
  kind               TEXT PRIMARY KEY,
  amr                TEXT NOT NULL,
  base_acr           SMALLINT NOT NULL CHECK (base_acr BETWEEN 1 AND 3),
  class              TEXT NOT NULL CHECK (class IN ('interactive', 'non_interactive')),
  factor             TEXT NOT NULL CHECK (factor IN ('knowledge', 'possession', 'federated')),
  phishing_resistant BOOLEAN NOT NULL,
  service_allowed    BOOLEAN NOT NULL,
  CONSTRAINT credential_kinds_service_non_interactive
    CHECK (NOT service_allowed OR class = 'non_interactive')
);

INSERT INTO mod_users.credential_kinds
  (kind, amr, base_acr, class, factor, phishing_resistant, service_allowed)
VALUES
  ('password',  'pwd',  1, 'interactive',     'knowledge',  false, false),
  ('email_otp', 'otp',  1, 'interactive',     'possession', false, false),
  ('oidc',      'fed',  1, 'interactive',     'federated',  false, false),
  ('ssh_key',   'swk',  1, 'non_interactive', 'possession', false, true),
  ('api_key',   'swk',  1, 'non_interactive', 'knowledge',  false, true),
  ('anon',      'anon', 1, 'interactive',     'possession', false, false);

-- ===========================================================================
-- Users
-- ===========================================================================

-- users is the `user` principal subtype (the type is seeded by mod-core).
--
--   bring-your-own user: managing_party_id NULL, owns itself, has exactly one
--                        personal profile and party profiles in any party;
--   managed user:        managing_party_id = X, owned by X, has no personal
--                        profile and party profiles only in X;
--   guest:               is_guest, no person, no managing party, only the
--                        personal profile and only `anon` credentials.
--
-- Database invariants (SQLSTATE MF103):
--   1. the entity descends from `user` (users_validate_invariants);
--   2. managing_party_id is immutable and is_guest only goes true -> false
--      (users_validate_invariants);
--   3. the user entity's owner is the user itself (bring-your-own) or
--      managing_party_id (managed), checked on insert against the
--      already-inserted entities row (users_validate_invariants; owner_id and
--      managing_party_id are both immutable, so it holds for life);
--   4. default_profile_id, when set, names a profile held by this user
--      (users_validate_invariants);
--   5. a profile whose holder is a user is admissible: no personal profile for
--      a managed user, party profiles of a managed user only in its managing
--      party, no party profile for a guest (profiles_users_holder_rules on
--      public.profiles, BEFORE INSERT; holder_id and party_id are immutable in
--      mod-core, so INSERT is the only write that can break it);
--   6. at commit, a bring-your-own user has exactly one personal profile and a
--      managed user has none (deferred constraint triggers
--      users_personal_profile on users INSERT and
--      profiles_users_personal_profile on personal-profile DELETE).
CREATE TABLE mod_users.users (
  entity_id          BIGINT PRIMARY KEY REFERENCES public.principals(entity_id) ON DELETE RESTRICT,
  default_app_id     BIGINT REFERENCES public.apps(id) ON DELETE SET NULL,
  default_profile_id BIGINT REFERENCES public.profiles(entity_id) ON DELETE SET NULL,
  is_guest           BOOLEAN NOT NULL DEFAULT false,
  managing_party_id  BIGINT REFERENCES public.legal_entities(entity_id) ON DELETE RESTRICT,
  -- Not unique: several users may link the same person record.
  person_id          BIGINT REFERENCES public.natural_persons(entity_id) ON DELETE RESTRICT,
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT users_guest_unlinked
    CHECK (NOT is_guest OR (person_id IS NULL AND managing_party_id IS NULL))
);

CREATE INDEX users_managing_party_id_idx
  ON mod_users.users (managing_party_id) WHERE managing_party_id IS NOT NULL;

CREATE INDEX users_person_id_idx
  ON mod_users.users (person_id) WHERE person_id IS NOT NULL;

CREATE TRIGGER users_set_updated_at
  BEFORE UPDATE ON mod_users.users
  FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();

-- Invariants 1 to 4.
-- +goose StatementBegin
CREATE FUNCTION mod_users.users_check_invariants() RETURNS TRIGGER
LANGUAGE plpgsql
SECURITY INVOKER
SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
  v_type_id  BIGINT;
  v_owner_id BIGINT;
BEGIN
  IF TG_OP = 'INSERT' THEN
    SELECT e.fundamental_type_id, e.owner_id INTO v_type_id, v_owner_id
      FROM public.entities e WHERE e.id = NEW.entity_id;
    IF v_type_id IS NULL OR NOT public.type_is_or_descends_from(v_type_id, 'user') THEN
      RAISE EXCEPTION 'users: entity % does not descend from user', NEW.entity_id
        USING ERRCODE = 'MF103';
    END IF;
    IF v_owner_id IS DISTINCT FROM COALESCE(NEW.managing_party_id, NEW.entity_id) THEN
      RAISE EXCEPTION 'users: the owner of user % must be %, not %', NEW.entity_id, COALESCE(NEW.managing_party_id, NEW.entity_id), v_owner_id
        USING ERRCODE = 'MF103';
    END IF;
  ELSE
    IF OLD.entity_id IS DISTINCT FROM NEW.entity_id THEN
      RAISE EXCEPTION 'users: entity_id is immutable (user %)', OLD.entity_id
        USING ERRCODE = 'MF103';
    END IF;
    IF OLD.managing_party_id IS DISTINCT FROM NEW.managing_party_id THEN
      RAISE EXCEPTION 'users: managing_party_id is immutable (user %)', NEW.entity_id
        USING ERRCODE = 'MF103';
    END IF;
    IF NOT OLD.is_guest AND NEW.is_guest THEN
      RAISE EXCEPTION 'users: a user cannot become a guest (user %)', NEW.entity_id
        USING ERRCODE = 'MF103';
    END IF;
  END IF;

  IF NEW.default_profile_id IS NOT NULL
     AND (TG_OP = 'INSERT' OR NEW.default_profile_id IS DISTINCT FROM OLD.default_profile_id)
     AND NOT EXISTS (
       SELECT 1 FROM public.profiles p
        WHERE p.entity_id = NEW.default_profile_id AND p.holder_id = NEW.entity_id
     )
  THEN
    RAISE EXCEPTION 'users: default profile % is not held by user %', NEW.default_profile_id, NEW.entity_id
      USING ERRCODE = 'MF103';
  END IF;

  RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER users_validate_invariants
  BEFORE INSERT OR UPDATE ON mod_users.users
  FOR EACH ROW EXECUTE FUNCTION mod_users.users_check_invariants();

-- Invariant 5, a mod-users-owned trigger on mod-core's profiles table. A
-- profile held by an entity of type `user` requires the users row to exist
-- first, so the managed/guest rules can never be bypassed by inserting the
-- profile before the users row. The trigger name sorts after mod-core's
-- profiles_type_check and before profiles_validate_invariants, so the profile
-- type is already validated when it runs.
-- +goose StatementBegin
CREATE FUNCTION mod_users.profiles_check_user_holder() RETURNS TRIGGER
LANGUAGE plpgsql
SECURITY INVOKER
SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
  v_is_guest          BOOLEAN;
  v_managing_party_id BIGINT;
  v_holder_type_id    BIGINT;
BEGIN
  SELECT u.is_guest, u.managing_party_id INTO v_is_guest, v_managing_party_id
    FROM mod_users.users u WHERE u.entity_id = NEW.holder_id;
  IF NOT FOUND THEN
    SELECT e.fundamental_type_id INTO v_holder_type_id
      FROM public.entities e WHERE e.id = NEW.holder_id;
    IF v_holder_type_id IS NOT NULL
       AND public.type_is_or_descends_from(v_holder_type_id, 'user')
    THEN
      RAISE EXCEPTION 'profiles: user % has no mod_users.users row; create it before its profiles', NEW.holder_id
        USING ERRCODE = 'MF103';
    END IF;
    RETURN NEW;
  END IF;

  IF NEW.party_id IS NULL THEN
    IF v_managing_party_id IS NOT NULL THEN
      RAISE EXCEPTION 'profiles: managed user % cannot hold a personal profile', NEW.holder_id
        USING ERRCODE = 'MF103';
    END IF;
  ELSIF v_is_guest THEN
    RAISE EXCEPTION 'profiles: guest user % cannot hold a party profile', NEW.holder_id
      USING ERRCODE = 'MF103';
  ELSIF v_managing_party_id IS NOT NULL AND NEW.party_id <> v_managing_party_id THEN
    RAISE EXCEPTION 'profiles: managed user % can hold party profiles only in its managing party %, not %', NEW.holder_id, v_managing_party_id, NEW.party_id
      USING ERRCODE = 'MF103';
  END IF;

  RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER profiles_users_holder_rules
  BEFORE INSERT ON public.profiles
  FOR EACH ROW EXECUTE FUNCTION mod_users.profiles_check_user_holder();

-- Invariant 6, deferred to commit because the users row is inserted before
-- the personal profile (which needs it, invariant 5). The profile partial
-- unique index already allows at most one personal profile; this adds "at
-- least one" for bring-your-own users and "none" for managed users. A users row
-- deleted again in the same transaction is skipped.
-- +goose StatementBegin
CREATE FUNCTION mod_users.users_check_personal_profile() RETURNS TRIGGER
LANGUAGE plpgsql
SECURITY INVOKER
SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
  v_user_id           BIGINT;
  v_managing_party_id BIGINT;
  v_personal          BIGINT;
BEGIN
  IF TG_TABLE_NAME = 'profiles' THEN
    v_user_id := OLD.holder_id;
  ELSE
    v_user_id := NEW.entity_id;
  END IF;

  SELECT u.managing_party_id INTO v_managing_party_id
    FROM mod_users.users u WHERE u.entity_id = v_user_id;
  IF NOT FOUND THEN
    RETURN NULL;
  END IF;

  SELECT count(*) INTO v_personal
    FROM public.profiles p
   WHERE p.holder_id = v_user_id AND p.party_id IS NULL;

  IF v_managing_party_id IS NULL AND v_personal <> 1 THEN
    RAISE EXCEPTION 'users: bring-your-own user % must have exactly one personal profile at commit (has %)', v_user_id, v_personal
      USING ERRCODE = 'MF103';
  END IF;
  IF v_managing_party_id IS NOT NULL AND v_personal <> 0 THEN
    RAISE EXCEPTION 'users: managed user % must have no personal profile (has %)', v_user_id, v_personal
      USING ERRCODE = 'MF103';
  END IF;

  RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER users_personal_profile
  AFTER INSERT ON mod_users.users
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION mod_users.users_check_personal_profile();

CREATE CONSTRAINT TRIGGER profiles_users_personal_profile
  AFTER DELETE ON public.profiles
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW
  WHEN (OLD.party_id IS NULL)
  EXECUTE FUNCTION mod_users.users_check_personal_profile();

-- ===========================================================================
-- User identifiers
-- ===========================================================================

-- user_identifiers holds contact and lookup data (email, phone, username).
-- Identifiers are never evidence on their own: only a credential bound to one
-- (cred_password, cred_email_otp) is. The foreign key to users means only
-- users have identifiers, never service accounts or system actors.
--
-- Binding read surface for other modules: the table name and the columns
-- user_id, kind, value_canon, verified_at and is_primary, with these meanings.
-- value_canon for an email is trimmed and lowercased (enforced below).
-- UNIQUE (kind, value_canon) keeps first-come uniqueness, including for
-- unverified addresses.
CREATE TABLE mod_users.user_identifiers (
  id          BIGSERIAL PRIMARY KEY,
  uuid        UUID NOT NULL UNIQUE DEFAULT gen_random_uuid(),
  user_id     BIGINT NOT NULL REFERENCES mod_users.users(entity_id) ON DELETE RESTRICT,
  is_primary  BOOLEAN NOT NULL DEFAULT false,
  kind        TEXT NOT NULL CHECK (kind IN ('email', 'phone', 'username')),
  value_canon TEXT NOT NULL CHECK (value_canon <> ''),
  verified_at TIMESTAMPTZ,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT user_identifiers_email_canonical
    CHECK (kind <> 'email' OR value_canon = lower(btrim(value_canon)))
);

CREATE UNIQUE INDEX user_identifiers_kind_value_uq
  ON mod_users.user_identifiers (kind, value_canon);

CREATE UNIQUE INDEX user_identifiers_one_primary_email_uq
  ON mod_users.user_identifiers (user_id) WHERE is_primary AND kind = 'email';

CREATE INDEX user_identifiers_user_id_idx
  ON mod_users.user_identifiers (user_id);

-- ===========================================================================
-- OIDC configuration and providers
-- ===========================================================================

-- oidc_config holds the singleton row (id = 1) that captures the operator's
-- confirmed choices for the OIDC onboarding flow. Per-provider on/off state
-- is owned by oidc_providers.enabled.
--
--   opt_out                : persists a "local-auth only" choice made through
--                            the confirm UI; equivalent to NO_OIDC_ACCOUNTS
--                            but survives env changes.
--   setup_token_hash       : sha256 hex of the active one-time setup token;
--                            NULL when no token is active.
--   setup_token_created_at : emission timestamp, for debugging and auditing.
--   saved_at               : last-saved wall-clock time (GUI "last saved").
CREATE TABLE mod_users.oidc_config (
  id                     INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
  opt_out                BOOLEAN NOT NULL DEFAULT false,
  setup_token_created_at TIMESTAMPTZ,
  setup_token_hash       TEXT,
  saved_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Seed the singleton so queries can always UPDATE rather than UPSERT.
INSERT INTO mod_users.oidc_config (id) VALUES (1);

-- oidc_providers stores per-provider DB overrides for the OIDC provider
-- registry. A NULL override column means "no override: use the env value if
-- set, otherwise the well-known default"; the merge layer
-- (api/internal/config/provider_merge.go) produces the effective provider.
--
--   id            : slug (lowercase letters, digits, dashes; 2 to 32 chars),
--                   matching the AUTH_PROVIDER_{ID}_* env naming.
--   client_secret : plaintext, matching the env model; the GUI never reads it
--                   back.
--   party_id      : the party the provider is bound to (NULL: unbound).
--   jit_mode      : what an unknown identity creates on first sign-in: none,
--                   a bring-your-own user ('byo'), or a managed user of
--                   party_id with an active idp_jit party profile ('managed').
--   trust_acr     : whether the IdP's asserted acr counts toward the session
--                   acr (mod_users.session_acr).
-- Env-only providers behave as party_id NULL, jit_mode 'byo' ('none' when
-- NO_OIDC_ACCOUNTS is set), trust_acr false.
CREATE TABLE mod_users.oidc_providers (
  id            TEXT PRIMARY KEY CHECK (id ~ '^[a-z][a-z0-9-]{0,30}[a-z0-9]$'),
  party_id      BIGINT REFERENCES public.legal_entities(entity_id) ON DELETE RESTRICT,
  claim_style   TEXT,
  client_id     TEXT,
  client_secret TEXT,
  display_name  TEXT,
  enabled       BOOLEAN NOT NULL DEFAULT true,
  issuer_url    TEXT,
  jit_mode      TEXT NOT NULL DEFAULT 'byo' CHECK (jit_mode IN ('none', 'byo', 'managed')),
  scopes        TEXT[],
  trust_acr     BOOLEAN NOT NULL DEFAULT false,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT oidc_providers_managed_requires_party
    CHECK (jit_mode <> 'managed' OR party_id IS NOT NULL)
);

CREATE TRIGGER oidc_providers_set_updated_at
  BEFORE UPDATE ON mod_users.oidc_providers
  FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();

-- ===========================================================================
-- Credentials
-- ===========================================================================

-- credentials is the common row of every credential; the kind-specific data
-- lives in exactly one cred_<kind> subtable row with the same credential_id.
-- Credentials are revoked (revoked_at, revoke_reason), never deleted.
--
--   confined_to_party_id  : a confined credential makes only that party's
--                           profiles available to a session using it.
--   default_profile_id    : the landing profile for a login with it.
--   created_by_profile_id : the acting profile that created it (audit only).
--
-- Database invariants (SQLSTATE MF102):
--   1. principal_id and kind are immutable (credentials_validate_invariants);
--   2. by principal type: a service account holds only service_allowed kinds,
--      a system actor holds none, a guest user holds only `anon`, and a
--      non-guest user cannot gain a new `anon` (on insert; principal and kind
--      are immutable and is_guest only goes true -> false);
--   3. default_profile_id, when set, names a profile held by principal_id;
--   4. the cred_<kind> row's kind matches credentials.kind, and a bound
--      identifier (cred_password, cred_email_otp) belongs to the principal
--      (cred_subtable_validate on each subtable).
-- confined_to_party_id names a legal entity through its foreign key.
CREATE TABLE mod_users.credentials (
  id                    BIGSERIAL PRIMARY KEY,
  uuid                  UUID NOT NULL UNIQUE DEFAULT gen_random_uuid(),
  principal_id          BIGINT NOT NULL REFERENCES public.principals(entity_id) ON DELETE RESTRICT,
  kind                  TEXT NOT NULL REFERENCES mod_users.credential_kinds(kind) ON DELETE RESTRICT,
  confined_to_party_id  BIGINT REFERENCES public.legal_entities(entity_id) ON DELETE RESTRICT,
  default_profile_id    BIGINT REFERENCES public.profiles(entity_id) ON DELETE SET NULL,
  expires_at            TIMESTAMPTZ,
  label                 TEXT NOT NULL DEFAULT '' CHECK (char_length(label) <= 100),
  revoke_reason         TEXT CHECK (revoke_reason IN ('user', 'admin', 'departure', 'superseded', 'guest_upgraded', 'expired')),
  revoked_at            TIMESTAMPTZ,
  created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_by_profile_id BIGINT REFERENCES public.profiles(entity_id) ON DELETE RESTRICT,
  last_used_at          TIMESTAMPTZ,
  CONSTRAINT credentials_revocation_complete
    CHECK ((revoked_at IS NULL) = (revoke_reason IS NULL))
);

CREATE INDEX credentials_principal_id_idx
  ON mod_users.credentials (principal_id);

-- Departure cleanup revokes the credentials confined to a party.
CREATE INDEX credentials_confined_to_party_id_idx
  ON mod_users.credentials (confined_to_party_id) WHERE confined_to_party_id IS NOT NULL;

-- Invariants 1 to 3.
-- +goose StatementBegin
CREATE FUNCTION mod_users.credentials_check_invariants() RETURNS TRIGGER
LANGUAGE plpgsql
SECURITY INVOKER
SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
  v_type_id         BIGINT;
  v_is_guest        BOOLEAN;
  v_service_allowed BOOLEAN;
BEGIN
  IF TG_OP = 'UPDATE' THEN
    IF OLD.principal_id IS DISTINCT FROM NEW.principal_id
       OR OLD.kind IS DISTINCT FROM NEW.kind
    THEN
      RAISE EXCEPTION 'credentials: principal_id and kind are immutable (credential %)', OLD.id
        USING ERRCODE = 'MF102';
    END IF;
  ELSE
    SELECT e.fundamental_type_id INTO v_type_id
      FROM public.entities e WHERE e.id = NEW.principal_id;
    SELECT k.service_allowed INTO v_service_allowed
      FROM mod_users.credential_kinds k WHERE k.kind = NEW.kind;

    IF public.type_is_or_descends_from(v_type_id, 'system_actor') THEN
      RAISE EXCEPTION 'credentials: system actor % cannot hold credentials', NEW.principal_id
        USING ERRCODE = 'MF102';
    ELSIF public.type_is_or_descends_from(v_type_id, 'service_account') THEN
      IF NOT COALESCE(v_service_allowed, false) THEN
        RAISE EXCEPTION 'credentials: service account % cannot hold a % credential', NEW.principal_id, NEW.kind
          USING ERRCODE = 'MF102';
      END IF;
    ELSIF public.type_is_or_descends_from(v_type_id, 'user') THEN
      SELECT u.is_guest INTO v_is_guest
        FROM mod_users.users u WHERE u.entity_id = NEW.principal_id;
      IF NOT FOUND THEN
        RAISE EXCEPTION 'credentials: user % has no mod_users.users row', NEW.principal_id
          USING ERRCODE = 'MF102';
      END IF;
      IF v_is_guest AND NEW.kind <> 'anon' THEN
        RAISE EXCEPTION 'credentials: guest user % can hold only anon credentials, not %', NEW.principal_id, NEW.kind
          USING ERRCODE = 'MF102';
      END IF;
      IF NOT v_is_guest AND NEW.kind = 'anon' THEN
        RAISE EXCEPTION 'credentials: non-guest user % cannot gain an anon credential', NEW.principal_id
          USING ERRCODE = 'MF102';
      END IF;
    ELSE
      RAISE EXCEPTION 'credentials: principal % is not a user, service account or system actor', NEW.principal_id
        USING ERRCODE = 'MF102';
    END IF;
  END IF;

  IF NEW.default_profile_id IS NOT NULL
     AND (TG_OP = 'INSERT' OR NEW.default_profile_id IS DISTINCT FROM OLD.default_profile_id)
     AND NOT EXISTS (
       SELECT 1 FROM public.profiles p
        WHERE p.entity_id = NEW.default_profile_id AND p.holder_id = NEW.principal_id
     )
  THEN
    RAISE EXCEPTION 'credentials: default profile % is not held by principal %', NEW.default_profile_id, NEW.principal_id
      USING ERRCODE = 'MF102';
  END IF;

  RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER credentials_validate_invariants
  BEFORE INSERT OR UPDATE ON mod_users.credentials
  FOR EACH ROW EXECUTE FUNCTION mod_users.credentials_check_invariants();

-- Per-kind subtables. Rows are removed only together with their credentials
-- row (ON DELETE CASCADE), which itself is never deleted in normal operation.
--
-- Mirror columns. A partial unique index can only see its own table, but
-- "active" uniqueness depends on credentials.revoked_at. So the subtables whose
-- uniqueness is scoped to active credentials carry `active` (= revoked_at IS
-- NULL), and cred_password also carries principal_id for "one active unbound
-- password per principal". Both are maintained by triggers, never by callers:
-- cred_subtable_validate overwrites them from credentials on every subtable
-- write, and credentials_sync_active re-touches the subtable row whenever
-- credentials.revoked_at changes.

-- argon2id-encoded hash. identifier_id NULL: an unbound password (at most one
-- active per principal); otherwise bound to the identifier it logs in with (at
-- most one active per identifier). Deleting the bound identifier sets the
-- column to NULL (ON DELETE SET NULL); the identifier-removal flow revokes the
-- credential first, so the row left behind is a revoked, unbound history row
-- and the active-unbound index below never sees it.
CREATE TABLE mod_users.cred_password (
  credential_id BIGINT PRIMARY KEY REFERENCES mod_users.credentials(id) ON DELETE CASCADE,
  identifier_id BIGINT REFERENCES mod_users.user_identifiers(id) ON DELETE SET NULL,
  hash          TEXT NOT NULL,
  -- Mirrors, maintained by trigger.
  active        BOOLEAN NOT NULL DEFAULT true,
  principal_id  BIGINT NOT NULL
);

CREATE UNIQUE INDEX cred_password_active_identifier_uq
  ON mod_users.cred_password (identifier_id) WHERE active AND identifier_id IS NOT NULL;

CREATE UNIQUE INDEX cred_password_active_unbound_uq
  ON mod_users.cred_password (principal_id) WHERE active AND identifier_id IS NULL;

-- Serves ListActivePasswordsByPrincipal: every active password of a principal,
-- bound or unbound (the unbound-only unique index above cannot).
CREATE INDEX cred_password_principal_id_idx
  ON mod_users.cred_password (principal_id) WHERE active;

-- A one-time code sent to the bound identifier; created when the identifier is
-- verified, so passwordless login keeps working. Deleting the identifier sets
-- identifier_id to NULL (ON DELETE SET NULL), which is only legal for a revoked
-- credential: an active email_otp must keep its identifier (the CHECK below).
-- The identifier-removal flow revokes the credential before deleting.
CREATE TABLE mod_users.cred_email_otp (
  credential_id BIGINT PRIMARY KEY REFERENCES mod_users.credentials(id) ON DELETE CASCADE,
  identifier_id BIGINT REFERENCES mod_users.user_identifiers(id) ON DELETE SET NULL,
  -- Mirror, maintained by trigger.
  active        BOOLEAN NOT NULL DEFAULT true,
  CONSTRAINT cred_email_otp_active_needs_identifier
    CHECK (identifier_id IS NOT NULL OR NOT active)
);

CREATE UNIQUE INDEX cred_email_otp_active_identifier_uq
  ON mod_users.cred_email_otp (identifier_id) WHERE active;

-- An external IdP identity. provider_id is an oidc_providers id or an env-only
-- provider id, so it has no foreign key. An active (issuer, subject) pair logs
-- in exactly one principal; it is never linked by email.
CREATE TABLE mod_users.cred_oidc (
  credential_id BIGINT PRIMARY KEY REFERENCES mod_users.credentials(id) ON DELETE CASCADE,
  email_at_link TEXT,
  issuer        TEXT NOT NULL,
  provider_id   TEXT NOT NULL,
  subject       TEXT NOT NULL,
  -- Mirror, maintained by trigger.
  active        BOOLEAN NOT NULL DEFAULT true
);

CREATE UNIQUE INDEX cred_oidc_active_issuer_subject_uq
  ON mod_users.cred_oidc (issuer, subject) WHERE active;

-- An SSH public key. An active fingerprint resolves to exactly one credential,
-- or one key could authenticate as two principals; the unique index (not a Go
-- pre-check) is what wins a concurrent-registration race, and it doubles as
-- the resolver's lookup index.
CREATE TABLE mod_users.cred_ssh_key (
  credential_id      BIGINT PRIMARY KEY REFERENCES mod_users.credentials(id) ON DELETE CASCADE,
  fingerprint_sha256 TEXT NOT NULL CHECK (fingerprint_sha256 LIKE 'SHA256:%'),
  key_type           TEXT NOT NULL CHECK (key_type IN (
                       'ssh-ed25519', 'sk-ssh-ed25519@openssh.com',
                       'ecdsa-sha2-nistp256', 'ecdsa-sha2-nistp384', 'ecdsa-sha2-nistp521',
                       'sk-ecdsa-sha2-nistp256@openssh.com', 'ssh-rsa')),
  public_key         TEXT NOT NULL,
  -- Mirror, maintained by trigger.
  active             BOOLEAN NOT NULL DEFAULT true
);

CREATE UNIQUE INDEX cred_ssh_key_active_fingerprint_uq
  ON mod_users.cred_ssh_key (fingerprint_sha256) WHERE active;

-- Client credentials (client_id plus a hashed secret) for the client
-- credentials grant.
CREATE TABLE mod_users.cred_api_key (
  credential_id BIGINT PRIMARY KEY REFERENCES mod_users.credentials(id) ON DELETE CASCADE,
  client_id     TEXT NOT NULL UNIQUE,
  secret_hash   TEXT NOT NULL
);

-- A guest's device token (hash only).
CREATE TABLE mod_users.cred_anon (
  credential_id BIGINT PRIMARY KEY REFERENCES mod_users.credentials(id) ON DELETE CASCADE,
  device_id     TEXT NOT NULL,
  token_hash    TEXT NOT NULL UNIQUE
);

-- Invariant 4 plus the mirror columns. TG_ARGV[0] is the subtable's kind. The
-- branches on TG_TABLE_NAME only touch columns their table has.
-- +goose StatementBegin
CREATE FUNCTION mod_users.cred_subtable_check() RETURNS TRIGGER
LANGUAGE plpgsql
SECURITY INVOKER
SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
  v_kind         TEXT;
  v_principal_id BIGINT;
  v_revoked_at   TIMESTAMPTZ;
BEGIN
  SELECT c.kind, c.principal_id, c.revoked_at INTO v_kind, v_principal_id, v_revoked_at
    FROM mod_users.credentials c WHERE c.id = NEW.credential_id;
  IF NOT FOUND THEN
    -- Left to the foreign key, which reports it precisely.
    RETURN NEW;
  END IF;
  IF v_kind <> TG_ARGV[0] THEN
    RAISE EXCEPTION '%: credential % is of kind %, not %', TG_TABLE_NAME, NEW.credential_id, v_kind, TG_ARGV[0]
      USING ERRCODE = 'MF102';
  END IF;

  IF TG_TABLE_NAME IN ('cred_password', 'cred_email_otp') THEN
    IF NEW.identifier_id IS NOT NULL AND NOT EXISTS (
      SELECT 1 FROM mod_users.user_identifiers i
       WHERE i.id = NEW.identifier_id AND i.user_id = v_principal_id
    ) THEN
      RAISE EXCEPTION '%: identifier % does not belong to principal %', TG_TABLE_NAME, NEW.identifier_id, v_principal_id
        USING ERRCODE = 'MF102';
    END IF;
  END IF;

  -- An active password must not lose its identifier: unbinding it would collide
  -- with (or silently join) the principal's unbound password slot. Only a revoked
  -- password may be unbound, which is what ON DELETE SET NULL does to history
  -- rows. 23503 makes the cascade from user_identifiers fail as a foreign-key
  -- violation, which the identifier-removal flow maps to ErrIdentifierInUse.
  -- Nested, not one AND chain: OLD.identifier_id is only valid on cred_password.
  IF TG_TABLE_NAME = 'cred_password' AND TG_OP = 'UPDATE' THEN
    IF OLD.identifier_id IS NOT NULL AND NEW.identifier_id IS NULL AND v_revoked_at IS NULL THEN
      RAISE EXCEPTION '%: active password % cannot be unbound from identifier %', TG_TABLE_NAME, NEW.credential_id, OLD.identifier_id
        USING ERRCODE = '23503';
    END IF;
  END IF;

  IF TG_TABLE_NAME IN ('cred_password', 'cred_email_otp', 'cred_oidc', 'cred_ssh_key') THEN
    NEW.active := v_revoked_at IS NULL;
  END IF;
  IF TG_TABLE_NAME = 'cred_password' THEN
    NEW.principal_id := v_principal_id;
  END IF;

  RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER cred_password_validate
  BEFORE INSERT OR UPDATE ON mod_users.cred_password
  FOR EACH ROW EXECUTE FUNCTION mod_users.cred_subtable_check('password');

CREATE TRIGGER cred_email_otp_validate
  BEFORE INSERT OR UPDATE ON mod_users.cred_email_otp
  FOR EACH ROW EXECUTE FUNCTION mod_users.cred_subtable_check('email_otp');

CREATE TRIGGER cred_oidc_validate
  BEFORE INSERT OR UPDATE ON mod_users.cred_oidc
  FOR EACH ROW EXECUTE FUNCTION mod_users.cred_subtable_check('oidc');

CREATE TRIGGER cred_ssh_key_validate
  BEFORE INSERT OR UPDATE ON mod_users.cred_ssh_key
  FOR EACH ROW EXECUTE FUNCTION mod_users.cred_subtable_check('ssh_key');

CREATE TRIGGER cred_api_key_validate
  BEFORE INSERT OR UPDATE ON mod_users.cred_api_key
  FOR EACH ROW EXECUTE FUNCTION mod_users.cred_subtable_check('api_key');

CREATE TRIGGER cred_anon_validate
  BEFORE INSERT OR UPDATE ON mod_users.cred_anon
  FOR EACH ROW EXECUTE FUNCTION mod_users.cred_subtable_check('anon');

-- Keeps the `active` mirrors in step with credentials.revoked_at. The no-op
-- UPDATE fires cred_subtable_check, which recomputes the mirror from the
-- credentials row (already updated, since this is an AFTER trigger). A
-- re-activation that would duplicate an active identity fails on the partial
-- unique index, as it should.
-- +goose StatementBegin
CREATE FUNCTION mod_users.credentials_sync_active() RETURNS TRIGGER
LANGUAGE plpgsql
SECURITY INVOKER
SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
  CASE NEW.kind
    WHEN 'password' THEN
      UPDATE mod_users.cred_password SET active = active WHERE credential_id = NEW.id;
    WHEN 'email_otp' THEN
      UPDATE mod_users.cred_email_otp SET active = active WHERE credential_id = NEW.id;
    WHEN 'oidc' THEN
      UPDATE mod_users.cred_oidc SET active = active WHERE credential_id = NEW.id;
    WHEN 'ssh_key' THEN
      UPDATE mod_users.cred_ssh_key SET active = active WHERE credential_id = NEW.id;
    ELSE
      NULL;
  END CASE;
  RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER credentials_sync_active
  AFTER UPDATE OF revoked_at ON mod_users.credentials
  FOR EACH ROW
  WHEN (OLD.revoked_at IS DISTINCT FROM NEW.revoked_at)
  EXECUTE FUNCTION mod_users.credentials_sync_active();

-- ===========================================================================
-- Party authentication policies and credential authorizations
-- ===========================================================================

-- party_authn_policies is a party's published gate for its profiles. No row
-- means credential_rule 'any' and min_acr 1.
--
--   any        : every credential passes;
--   authorized : a session factor must be authorized for the party
--                (credential_party_authorizations) or confined to it;
--   provider   : a session factor must be an oidc credential of
--                required_oidc_provider_id.
-- min_acr always applies (an outside-collaborator confirmation in
-- public.profile_policy_confirmations exempts only from the credential rule).
-- There is no party-level invite_only: only apps are invite-only.
CREATE TABLE mod_users.party_authn_policies (
  party_id                  BIGINT PRIMARY KEY REFERENCES public.legal_entities(entity_id) ON DELETE RESTRICT,
  required_oidc_provider_id TEXT REFERENCES mod_users.oidc_providers(id) ON DELETE RESTRICT,
  credential_rule           TEXT NOT NULL DEFAULT 'any' CHECK (credential_rule IN ('any', 'authorized', 'provider')),
  min_acr                   SMALLINT NOT NULL DEFAULT 1 CHECK (min_acr BETWEEN 1 AND 3),
  created_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT party_authn_policies_provider_rule
    CHECK ((credential_rule = 'provider') = (required_oidc_provider_id IS NOT NULL))
);

CREATE TRIGGER party_authn_policies_set_updated_at
  BEFORE UPDATE ON mod_users.party_authn_policies
  FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();

-- credential_party_authorizations: a party admin (or a session already
-- satisfying the gate) authorized this credential for the party's
-- 'authorized' credential rule.
CREATE TABLE mod_users.credential_party_authorizations (
  credential_id            BIGINT NOT NULL REFERENCES mod_users.credentials(id) ON DELETE CASCADE,
  party_id                 BIGINT NOT NULL REFERENCES public.legal_entities(entity_id) ON DELETE RESTRICT,
  authorized_by_profile_id BIGINT NOT NULL REFERENCES public.profiles(entity_id) ON DELETE RESTRICT,
  authorized_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (credential_id, party_id)
);

-- Party listings and departure cleanup.
CREATE INDEX credential_party_authorizations_party_id_idx
  ON mod_users.credential_party_authorizations (party_id);

-- ===========================================================================
-- Sessions
-- ===========================================================================

-- sessions is the revocation unit of an interactive sign-in. The active
-- profile rides on the access token, not on this row, so a profile switch
-- writes nothing here. An impersonation session is a separate row whose
-- principal is the target profile's holder, with impersonated_profile_id
-- fixed, impersonator_session_id naming the administrator's own session and
-- impersonator_profile_id the profile the administrator acted as when starting
-- it (renewal re-authorizes impersonate as that profile, decision I).
--
-- Database invariants (SQLSTATE MF101):
--   1. a session's principal is a user (service accounts authenticate only
--      through non-interactive credentials; system actors never do);
--   2. an impersonation session's parent is not itself an impersonation;
--   3. impersonated_profile_id is held by principal_id, and
--      impersonator_profile_id by the parent session's principal;
--   4. principal_id and the impersonation columns are immutable;
--   5. a factor's credential belongs to the session's principal
--      (session_factors_validate).
CREATE TABLE mod_users.sessions (
  id                      BIGSERIAL PRIMARY KEY,
  uuid                    UUID NOT NULL UNIQUE DEFAULT gen_random_uuid(),
  principal_id            BIGINT NOT NULL REFERENCES public.principals(entity_id) ON DELETE RESTRICT,
  app_id                  BIGINT REFERENCES public.apps(id) ON DELETE RESTRICT,
  impersonated_profile_id BIGINT REFERENCES public.profiles(entity_id) ON DELETE RESTRICT,
  impersonator_session_id BIGINT REFERENCES mod_users.sessions(id) ON DELETE RESTRICT,
  impersonator_profile_id BIGINT REFERENCES public.profiles(entity_id) ON DELETE RESTRICT,
  acr                     SMALLINT NOT NULL CHECK (acr BETWEEN 1 AND 3),
  amr                     TEXT[] NOT NULL,
  auth_time               TIMESTAMPTZ NOT NULL,
  impersonation_reason    TEXT,
  revoke_reason           TEXT CHECK (revoke_reason IN (
                            'logout', 'admin', 'credential_revoked', 'principal_disabled',
                            'refresh_reuse', 'impersonation_ended', 'parent_revoked', 'profile_ended')),
  revoked_at              TIMESTAMPTZ,
  created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at              TIMESTAMPTZ NOT NULL,
  last_seen_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT sessions_impersonation_complete
    CHECK ((impersonator_session_id IS NULL) = (impersonated_profile_id IS NULL)
       AND (impersonator_session_id IS NULL) = (impersonator_profile_id IS NULL)
       AND (impersonator_session_id IS NULL) = (impersonation_reason IS NULL)),
  CONSTRAINT sessions_revocation_complete
    CHECK ((revoked_at IS NULL) = (revoke_reason IS NULL))
);

CREATE INDEX sessions_principal_id_idx
  ON mod_users.sessions (principal_id);

-- Ending or revoking a parent session finds its impersonation children.
CREATE INDEX sessions_impersonator_session_id_idx
  ON mod_users.sessions (impersonator_session_id) WHERE impersonator_session_id IS NOT NULL;

-- Invariants 1 to 4.
-- +goose StatementBegin
CREATE FUNCTION mod_users.sessions_check_invariants() RETURNS TRIGGER
LANGUAGE plpgsql
SECURITY INVOKER
SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
  IF TG_OP = 'UPDATE' THEN
    IF OLD.principal_id IS DISTINCT FROM NEW.principal_id
       OR OLD.impersonator_session_id IS DISTINCT FROM NEW.impersonator_session_id
       OR OLD.impersonated_profile_id IS DISTINCT FROM NEW.impersonated_profile_id
       OR OLD.impersonator_profile_id IS DISTINCT FROM NEW.impersonator_profile_id
       OR OLD.impersonation_reason IS DISTINCT FROM NEW.impersonation_reason
    THEN
      RAISE EXCEPTION 'sessions: principal_id and the impersonation columns are immutable (session %)', OLD.id
        USING ERRCODE = 'MF101';
    END IF;
    RETURN NEW;
  END IF;

  IF NOT EXISTS (SELECT 1 FROM mod_users.users u WHERE u.entity_id = NEW.principal_id) THEN
    RAISE EXCEPTION 'sessions: principal % is not a user', NEW.principal_id
      USING ERRCODE = 'MF101';
  END IF;

  IF NEW.impersonator_session_id IS NOT NULL THEN
    IF EXISTS (
      SELECT 1 FROM mod_users.sessions parent
       WHERE parent.id = NEW.impersonator_session_id
         AND parent.impersonator_session_id IS NOT NULL
    ) THEN
      RAISE EXCEPTION 'sessions: session % is an impersonation and cannot start another (no nesting)', NEW.impersonator_session_id
        USING ERRCODE = 'MF101';
    END IF;
    IF NOT EXISTS (
      SELECT 1 FROM public.profiles p
       WHERE p.entity_id = NEW.impersonated_profile_id AND p.holder_id = NEW.principal_id
    ) THEN
      RAISE EXCEPTION 'sessions: impersonated profile % is not held by principal %', NEW.impersonated_profile_id, NEW.principal_id
        USING ERRCODE = 'MF101';
    END IF;
    IF NOT EXISTS (
      SELECT 1 FROM mod_users.sessions parent
        JOIN public.profiles p ON p.holder_id = parent.principal_id
       WHERE parent.id = NEW.impersonator_session_id AND p.entity_id = NEW.impersonator_profile_id
    ) THEN
      RAISE EXCEPTION 'sessions: impersonator profile % is not held by the principal of session %', NEW.impersonator_profile_id, NEW.impersonator_session_id
        USING ERRCODE = 'MF101';
    END IF;
  END IF;

  RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER sessions_validate_invariants
  BEFORE INSERT OR UPDATE ON mod_users.sessions
  FOR EACH ROW EXECUTE FUNCTION mod_users.sessions_check_invariants();

-- session_factors: the credentials verified in a session. asserted_acr is the
-- acr an external IdP asserted for an oidc factor; it counts only when the
-- provider has trust_acr (mod_users.session_acr).
CREATE TABLE mod_users.session_factors (
  session_id    BIGINT NOT NULL REFERENCES mod_users.sessions(id) ON DELETE CASCADE,
  credential_id BIGINT NOT NULL REFERENCES mod_users.credentials(id) ON DELETE RESTRICT,
  asserted_acr  SMALLINT CHECK (asserted_acr BETWEEN 1 AND 3),
  verified_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (session_id, credential_id)
);

-- Revoking a credential revokes every session holding it as a factor.
CREATE INDEX session_factors_credential_id_idx
  ON mod_users.session_factors (credential_id);

-- Invariant 5.
-- +goose StatementBegin
CREATE FUNCTION mod_users.session_factors_check_principal() RETURNS TRIGGER
LANGUAGE plpgsql
SECURITY INVOKER
SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
  IF NOT EXISTS (
    SELECT 1
      FROM mod_users.sessions s
      JOIN mod_users.credentials c ON c.principal_id = s.principal_id
     WHERE s.id = NEW.session_id AND c.id = NEW.credential_id
  ) THEN
    RAISE EXCEPTION 'session_factors: credential % does not belong to the principal of session %', NEW.credential_id, NEW.session_id
      USING ERRCODE = 'MF101';
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER session_factors_validate
  BEFORE INSERT OR UPDATE ON mod_users.session_factors
  FOR EACH ROW EXECUTE FUNCTION mod_users.session_factors_check_principal();

-- session_refresh_tokens: one refresh lineage per browser tab (lineage_uuid),
-- rotated on every use. A token presented again after use (used_at set) is
-- reuse and revokes the whole session. The profile is not stored here: the
-- refresh request names it and availability is re-run.
CREATE TABLE mod_users.session_refresh_tokens (
  id             BIGSERIAL PRIMARY KEY,
  session_id     BIGINT NOT NULL REFERENCES mod_users.sessions(id) ON DELETE CASCADE,
  replaced_by_id BIGINT REFERENCES mod_users.session_refresh_tokens(id) ON DELETE SET NULL,
  lineage_uuid   UUID NOT NULL,
  token_hash     TEXT NOT NULL UNIQUE,
  used_at        TIMESTAMPTZ,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at     TIMESTAMPTZ NOT NULL,
  CONSTRAINT session_refresh_tokens_replaced_after_use
    CHECK (replaced_by_id IS NULL OR used_at IS NOT NULL)
);

CREATE INDEX session_refresh_tokens_session_id_idx
  ON mod_users.session_refresh_tokens (session_id);

CREATE INDEX session_refresh_tokens_lineage_uuid_idx
  ON mod_users.session_refresh_tokens (lineage_uuid);

-- ===========================================================================
-- Challenges
-- ===========================================================================

-- auth_challenges: one-time codes and tokens. Challenges are not credentials.
--
--   email_code        : a sign-in code for an email_otp credential;
--   verify_identifier : proves control of an identifier;
--   password_reset    : resets a password;
--   step_up           : adds or re-verifies a factor in session_id;
--   login_handoff     : the single-use, 60 s code the OIDC callback redirect
--                       carries instead of a token (exchanged at
--                       POST /v1/auth/handoff). Its binding_hash is the PKCE-style
--                       challenge (base64url SHA-256 of a browser-held verifier)
--                       the exchange must answer with the verifier.
CREATE TABLE mod_users.auth_challenges (
  id              BIGSERIAL PRIMARY KEY,
  uuid            UUID NOT NULL UNIQUE DEFAULT gen_random_uuid(),
  credential_id   BIGINT REFERENCES mod_users.credentials(id) ON DELETE CASCADE,
  identifier_id   BIGINT REFERENCES mod_users.user_identifiers(id) ON DELETE CASCADE,
  principal_id    BIGINT REFERENCES public.principals(entity_id) ON DELETE CASCADE,
  session_id      BIGINT REFERENCES mod_users.sessions(id) ON DELETE CASCADE,
  code_hash       TEXT NOT NULL,
  binding_hash    TEXT,
  consumed_at     TIMESTAMPTZ,
  expires_at      TIMESTAMPTZ NOT NULL,
  failed_attempts INT NOT NULL DEFAULT 0 CHECK (failed_attempts >= 0),
  kind            TEXT NOT NULL CHECK (kind IN ('email_code', 'verify_identifier', 'password_reset', 'step_up', 'login_handoff')),
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Handoff and password-reset exchanges look a challenge up by its code alone.
CREATE INDEX auth_challenges_code_hash_idx
  ON mod_users.auth_challenges (code_hash);

-- Serves the cooldown probe in create_challenge_with_cooldown, which matches
-- the challenge subject with NULLs equal (COALESCE to 0; ids are never 0). Not
-- partial: the probe counts challenges in any consumed state.
CREATE INDEX auth_challenges_cooldown_idx
  ON mod_users.auth_challenges (
    kind,
    (COALESCE(principal_id, 0)),
    (COALESCE(identifier_id, 0)),
    (COALESCE(session_id, 0)),
    created_at DESC
  );

-- Foreign-key lookups: the ON DELETE CASCADE scans and open-challenge queries.
CREATE INDEX auth_challenges_principal_id_idx
  ON mod_users.auth_challenges (principal_id) WHERE principal_id IS NOT NULL;
CREATE INDEX auth_challenges_identifier_id_idx
  ON mod_users.auth_challenges (identifier_id) WHERE identifier_id IS NOT NULL;
CREATE INDEX auth_challenges_session_id_idx
  ON mod_users.auth_challenges (session_id) WHERE session_id IS NOT NULL;
CREATE INDEX auth_challenges_credential_id_idx
  ON mod_users.auth_challenges (credential_id) WHERE credential_id IS NOT NULL;

-- create_challenge_with_cooldown mints at most one challenge per subject
-- (kind, principal, identifier, session) per cooldown window, and returns the
-- inserted row, or no row when a challenge of any consumed state was created
-- within p_cooldown_secs. An INSERT ... WHERE NOT EXISTS alone is not enough
-- under READ COMMITTED: two concurrent statements each take their snapshot
-- before either commits, so both would insert. The transaction-scoped advisory
-- lock serializes callers for the same subject, and because each statement in
-- a plpgsql function takes a fresh snapshot, the NOT EXISTS probe made after
-- acquiring the lock sees the row the previous holder committed.
--
-- EXECUTE stays granted to PUBLIC: the function is SECURITY INVOKER, so a caller
-- without INSERT/SELECT on auth_challenges gains nothing from calling it.
-- +goose StatementBegin
CREATE FUNCTION mod_users.create_challenge_with_cooldown(
  p_kind          TEXT,
  p_principal_id  BIGINT,
  p_identifier_id BIGINT,
  p_session_id    BIGINT,
  p_code_hash     TEXT,
  p_expires_at    TIMESTAMPTZ,
  p_cooldown_secs INT
) RETURNS SETOF mod_users.auth_challenges
LANGUAGE plpgsql
VOLATILE
SECURITY INVOKER
SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
  PERFORM pg_advisory_xact_lock(hashtextextended(
    'mod_users.auth_challenge_cooldown:' || p_kind
      || ':' || COALESCE(p_principal_id, 0)::text
      || ':' || COALESCE(p_identifier_id, 0)::text
      || ':' || COALESCE(p_session_id, 0)::text,
    0));

  RETURN QUERY
  INSERT INTO mod_users.auth_challenges (kind, principal_id, identifier_id, session_id, code_hash, expires_at)
  SELECT p_kind, p_principal_id, p_identifier_id, p_session_id, p_code_hash, p_expires_at
  WHERE NOT EXISTS (
    SELECT 1
      FROM mod_users.auth_challenges ac
     WHERE ac.kind = p_kind
       AND COALESCE(ac.principal_id, 0) = COALESCE(p_principal_id, 0)
       AND COALESCE(ac.identifier_id, 0) = COALESCE(p_identifier_id, 0)
       AND COALESCE(ac.session_id, 0) = COALESCE(p_session_id, 0)
       AND ac.created_at > clock_timestamp() - make_interval(secs => p_cooldown_secs)
  )
  RETURNING *;
END;
$$;
-- +goose StatementEnd

-- ===========================================================================
-- Signing key
-- ===========================================================================

-- auth_signing_keys holds the single persisted Ed25519 access-token signing
-- key (PKCS#8 DER) used when JWT_SIGNING_KEY is unset. The id = 1 CHECK plus
-- PRIMARY KEY caps it at one row, so concurrent first-boot callers race on the
-- constraint (INSERT ... ON CONFLICT (id) DO NOTHING) and all converge on the
-- winner's key. The length CHECK guards against a truncated key being
-- persisted (an Ed25519 PKCS#8 DER encoding is at least 48 bytes).
--
-- CREATE ... IF NOT EXISTS, deliberately: the config bootstrap runs before
-- this module's migrations in every generated composition root, so it may
-- already have created the schema and this table, idempotently, with this
-- exact DDL, before goose gets here. Not an entity: bootstrap data.
CREATE TABLE IF NOT EXISTS mod_users.auth_signing_keys (
  id          SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
  private_key BYTEA NOT NULL CHECK (octet_length(private_key) >= 48),
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ===========================================================================
-- Assurance, availability and liveness
-- ===========================================================================

-- session_acr_excluding(p_session_id, p_exclude_credential_id): the session's
-- acr (NIST AAL 1 to 3) derived from its unrevoked factors, leaving out the
-- credential p_exclude_credential_id when it is not NULL: the highest effective
-- acr of any factor, raised to 2 when factors of two distinct factor classes
-- are present, capped at 3. A factor's effective acr is its kind's base_acr,
-- or for an oidc factor the IdP's asserted acr when higher and the provider
-- has trust_acr (env-only providers never do). 3 is otherwise reserved for
-- phishing-resistant verification, which no seeded kind provides. NULL when
-- no unrevoked factor remains (an impersonation session carries its parent's
-- acr). The exclusion serves profile_availability_excluding.
-- +goose StatementBegin
CREATE FUNCTION mod_users.session_acr_excluding(p_session_id BIGINT, p_exclude_credential_id BIGINT) RETURNS SMALLINT
LANGUAGE sql
STABLE
SECURITY INVOKER
SET search_path = pg_catalog, pg_temp
AS $$
  SELECT CASE
    WHEN count(*) = 0 THEN NULL
    ELSE LEAST(3, GREATEST(
           max(GREATEST(k.base_acr, CASE WHEN op.trust_acr THEN f.asserted_acr END)),
           CASE WHEN count(DISTINCT k.factor) >= 2 THEN 2 ELSE 1 END))
  END::SMALLINT
    FROM mod_users.session_factors f
    JOIN mod_users.credentials c ON c.id = f.credential_id
    JOIN mod_users.credential_kinds k ON k.kind = c.kind
    LEFT JOIN mod_users.cred_oidc o ON o.credential_id = c.id
    LEFT JOIN mod_users.oidc_providers op ON op.id = o.provider_id
   WHERE f.session_id = p_session_id
     AND c.revoked_at IS NULL
     AND (p_exclude_credential_id IS NULL OR c.id <> p_exclude_credential_id);
$$;
-- +goose StatementEnd

-- session_acr(p_session_id): the session's acr from all its unrevoked factors
-- (session_acr_excluding with nothing excluded).
-- +goose StatementBegin
CREATE FUNCTION mod_users.session_acr(p_session_id BIGINT) RETURNS SMALLINT
LANGUAGE sql
STABLE
SECURITY INVOKER
SET search_path = pg_catalog, pg_temp
AS $$
  SELECT mod_users.session_acr_excluding(p_session_id, NULL);
$$;
-- +goose StatementEnd

-- session_policy_factors(p_session_id, p_exclude_credential_id): the factors
-- a session presents to a party's authentication policy (decision G, as
-- resolved). For an ordinary session they are its own unrevoked factors and
-- its stored acr. An impersonation session holds no factors of its own: while
-- impersonating, the impersonated profile's rules apply to every operation
-- (its own party's policy and the policy of every party whose content it
-- reaches), and they are evaluated on the administrator's authentication, the
-- parent session (impersonator_session_id): its unrevoked factors for the
-- credential rule, and its stored acr for min_acr. The parent's acr, not the
-- child's copy, so a policy's min_acr reflects the administrator's session as
-- it is now. The credential exclusion (p_exclude_credential_id, NULL for none)
-- applies to whichever session supplies the factors.
--
-- skip_credential_rule is true only for an impersonation started through a
-- platform operator profile (decision J): the impersonation session's
-- recorded acting profile (impersonator_profile_id) is active and is an
-- operator profile (public.core_is_operator_profile). Decision D extended: an
-- operator's credentials can never be authorized for a customer party, so a
-- customer party's credential rule never binds an operator impersonation;
-- min_acr still does. Only the recorded acting profile counts: an
-- administrator who impersonates through a non-operator profile skips no
-- party's credential rule, even when the same principal also holds an
-- operator profile.
--
-- No row (all NULL) for an unknown session.
-- +goose StatementBegin
CREATE FUNCTION mod_users.session_policy_factors(
  p_session_id             BIGINT,
  p_exclude_credential_id  BIGINT,
  OUT credential_ids       BIGINT[],
  OUT acr                  SMALLINT,
  OUT skip_credential_rule BOOLEAN
)
LANGUAGE plpgsql
STABLE
SECURITY INVOKER
SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
  v_factor_session_id     BIGINT;
  v_impersonation         BOOLEAN;
  v_impersonator_profile  BIGINT;
BEGIN
  SELECT COALESCE(s.impersonator_session_id, s.id), s.impersonator_session_id IS NOT NULL,
         s.impersonator_profile_id
    INTO v_factor_session_id, v_impersonation, v_impersonator_profile
    FROM mod_users.sessions s WHERE s.id = p_session_id;
  IF NOT FOUND THEN
    RETURN;
  END IF;

  SELECT fs.acr INTO acr
    FROM mod_users.sessions fs WHERE fs.id = v_factor_session_id;

  credential_ids := ARRAY(
    SELECT f.credential_id
      FROM mod_users.session_factors f
      JOIN mod_users.credentials c ON c.id = f.credential_id
     WHERE f.session_id = v_factor_session_id
       AND c.revoked_at IS NULL
       AND (p_exclude_credential_id IS NULL OR c.id <> p_exclude_credential_id));

  skip_credential_rule := v_impersonation AND EXISTS (
    SELECT 1 FROM public.profiles p
     WHERE p.entity_id = v_impersonator_profile
       AND p.status = 'active'
       AND public.core_is_operator_profile(p.entity_id));
END;
$$;
-- +goose StatementEnd

-- party_policy_gate_for(p_party_id, p_profile_id, p_skip_credential_rule,
-- p_credential_ids, p_acr): the party authentication policy part of the gate,
-- shared by profile_gate_for (the acting profile's own party) and
-- session_meets_party_policy (the owning party of content reached from outside
-- it), so the two cannot drift. Returns 'available' or the first failing lock
-- reason: credential_not_authorized (the 'authorized' rule: no factor is
-- authorized for the party or confined to it), provider_required (the
-- 'provider' rule: no oidc factor of the required provider), acr_too_low
-- (p_acr is NULL or below min_acr). No policy row means 'any' with min_acr 1.
--
-- The credential rule is skipped when p_skip_credential_rule is true (an
-- impersonation started through an active operator profile, the session's
-- recorded impersonator_profile_id; session_policy_factors) and when a
-- profile_policy_confirmations row exists for (p_profile_id, p_party_id);
-- min_acr is never skipped.
-- +goose StatementBegin
CREATE FUNCTION mod_users.party_policy_gate_for(
  p_party_id             BIGINT,
  p_profile_id           BIGINT,
  p_skip_credential_rule BOOLEAN,
  p_credential_ids       BIGINT[],
  p_acr                  SMALLINT
) RETURNS TEXT
LANGUAGE plpgsql
STABLE
SECURITY INVOKER
SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
  v_rule        TEXT;
  v_provider_id TEXT;
  v_min_acr     SMALLINT;
BEGIN
  SELECT pol.credential_rule, pol.required_oidc_provider_id, pol.min_acr
    INTO v_rule, v_provider_id, v_min_acr
    FROM mod_users.party_authn_policies pol WHERE pol.party_id = p_party_id;
  IF NOT FOUND THEN
    v_rule := 'any';
    v_min_acr := 1;
  END IF;

  IF v_rule <> 'any'
     AND NOT p_skip_credential_rule
     AND NOT EXISTS (
       SELECT 1 FROM public.profile_policy_confirmations ppc
        WHERE ppc.profile_id = p_profile_id AND ppc.party_id = p_party_id
     )
  THEN
    IF v_rule = 'authorized' AND NOT EXISTS (
      SELECT 1 FROM mod_users.credentials c
       WHERE c.id = ANY (p_credential_ids)
         AND (c.confined_to_party_id = p_party_id
              OR EXISTS (
                SELECT 1 FROM mod_users.credential_party_authorizations cpa
                 WHERE cpa.credential_id = c.id AND cpa.party_id = p_party_id
              ))
    ) THEN
      RETURN 'credential_not_authorized';
    END IF;
    IF v_rule = 'provider' AND NOT EXISTS (
      SELECT 1 FROM mod_users.cred_oidc o
       WHERE o.credential_id = ANY (p_credential_ids)
         AND o.provider_id = v_provider_id
    ) THEN
      RETURN 'provider_required';
    END IF;
  END IF;

  IF p_acr IS NULL OR p_acr < v_min_acr THEN
    RETURN 'acr_too_low';
  END IF;

  RETURN 'available';
END;
$$;
-- +goose StatementEnd

-- profile_gate_for: the status-agnostic core of profile_availability_for. It
-- evaluates everything but the profile's own status, so it answers "would the
-- principal's session satisfy the profile's gate" for a profile in any status
-- (the leave path uses it for a suspended profile, which profile_availability
-- reports as 'inactive' before the party gate is reached). Returns 'available'
-- or the first failing lock reason, in this order:
--
--   not_held                  the profile is not held by the principal, or an
--                             impersonation targets another profile;
--   principal_disabled        the principal's kill switch is set;
--   confined                  a factor is confined to another party (a
--                             personal profile is hidden by any confinement);
--   credential_not_authorized the party's 'authorized' rule: no factor is
--                             authorized for the party or confined to it;
--   provider_required         the party's 'provider' rule: no oidc factor of
--                             the required provider;
--   acr_too_low               p_acr is below the party's min_acr.
--
-- The party gate applies only to party profiles and is party_policy_gate_for.
-- Its credential rule is skipped when p_skip_credential_rule is true (an
-- impersonation started through an active operator profile, the session's
-- recorded impersonator_profile_id) and for a profile with a
-- profile_policy_confirmations row for the party (an outside collaborator);
-- min_acr is never skipped for a party profile.
--
-- Under impersonation (p_impersonated_profile_id set) p_credential_ids and
-- p_acr are the administrator's parent-session factors
-- (session_policy_factors), which apply to the party gate only: the
-- confinement check is not applied to them, since they are not the
-- impersonated holder's credentials (the child session holds none, so it has
-- never been confined).
-- +goose StatementBegin
CREATE FUNCTION mod_users.profile_gate_for(
  p_principal_id            BIGINT,
  p_impersonated_profile_id BIGINT,
  p_credential_ids          BIGINT[],
  p_acr                     SMALLINT,
  p_profile_id              BIGINT,
  p_skip_credential_rule    BOOLEAN
) RETURNS TEXT
LANGUAGE plpgsql
STABLE
SECURITY INVOKER
SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
  v_holder_id   BIGINT;
  v_party_id    BIGINT;
BEGIN
  SELECT p.holder_id, p.party_id INTO v_holder_id, v_party_id
    FROM public.profiles p WHERE p.entity_id = p_profile_id;
  IF NOT FOUND
     OR v_holder_id IS DISTINCT FROM p_principal_id
     OR (p_impersonated_profile_id IS NOT NULL AND p_profile_id <> p_impersonated_profile_id)
  THEN
    RETURN 'not_held';
  END IF;

  IF EXISTS (
    SELECT 1 FROM public.principals pr
     WHERE pr.entity_id = p_principal_id AND pr.disabled_at IS NOT NULL
  ) THEN
    RETURN 'principal_disabled';
  END IF;

  IF p_impersonated_profile_id IS NULL AND EXISTS (
    SELECT 1 FROM mod_users.credentials c
     WHERE c.id = ANY (p_credential_ids)
       AND c.confined_to_party_id IS NOT NULL
       AND c.confined_to_party_id IS DISTINCT FROM v_party_id
  ) THEN
    RETURN 'confined';
  END IF;

  IF v_party_id IS NULL THEN
    RETURN 'available';
  END IF;

  RETURN mod_users.party_policy_gate_for(
    v_party_id, p_profile_id, COALESCE(p_skip_credential_rule, false), p_credential_ids, p_acr);
END;
$$;
-- +goose StatementEnd

-- profile_availability_for: the shared rule behind profile_availability
-- (session factors) and credential_profile_availability (one credential), so
-- the interactive and non-interactive paths cannot drift. It is
-- profile_gate_for with the profile's status applied between the held check
-- and the rest: 'not_held' first, then 'inactive' when the profile is not
-- active, then profile_gate_for's order (principal_disabled, confined,
-- credential_not_authorized, provider_required, acr_too_low), else
-- 'available'.
-- +goose StatementBegin
CREATE FUNCTION mod_users.profile_availability_for(
  p_principal_id            BIGINT,
  p_impersonated_profile_id BIGINT,
  p_credential_ids          BIGINT[],
  p_acr                     SMALLINT,
  p_profile_id              BIGINT,
  p_skip_credential_rule    BOOLEAN
) RETURNS TEXT
LANGUAGE plpgsql
STABLE
SECURITY INVOKER
SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
  v_gate TEXT;
BEGIN
  v_gate := mod_users.profile_gate_for(
    p_principal_id, p_impersonated_profile_id, p_credential_ids, p_acr, p_profile_id, p_skip_credential_rule);
  IF v_gate <> 'not_held' AND EXISTS (
    SELECT 1 FROM public.profiles p
     WHERE p.entity_id = p_profile_id AND p.status <> 'active'
  ) THEN
    RETURN 'inactive';
  END IF;
  RETURN v_gate;
END;
$$;
-- +goose StatementEnd

-- holder_policy_mismatch(p_holder_id, p_party_id, p_invite_time): whether the holder's current
-- credentials can meet the party's authentication policy, for the
-- outside-collaborator check on an invitation or on adding a profile to a
-- party's actor group (no profile exists yet, so profile_availability cannot
-- answer). reason is NULL when they can, else the first failing lock reason in
-- profile_availability_for's order:
--
--   credential_not_authorized the 'authorized' rule. Invite time
--                             (p_invite_time true): the holder has no usable
--                             interactive credential at all. No profile exists
--                             yet, so no credential can be authorized for the
--                             party; the rule is treated as satisfied by any
--                             usable interactive credential, and the member
--                             stays locked (profile_availability_for returns
--                             credential_not_authorized) after acceptance until
--                             a credential is authorized or confined. Strict
--                             (p_invite_time false, the outside-collaborator
--                             path): no usable interactive credential is
--                             authorized for the party (a
--                             credential_party_authorizations row) or confined
--                             to it. A collaborator holds no profile in the
--                             owning party, so it can never be authorized there
--                             implicitly and always needs a confirmation;
--   provider_required         the 'provider' rule: no usable oidc credential of
--                             the required provider;
--   acr_too_low               no combination of usable credentials reaches
--                             min_acr.
--
-- Usable credentials are the holder's unrevoked, unexpired interactive
-- credentials that are not confined to another party. holder_acr is the best
-- acr they can reach by session_acr_excluding's composition rule (the highest
-- base_acr, raised to 2 by two distinct factor classes, capped at 3; an IdP's
-- asserted acr is per sign-in and not counted), NULL with none. A policy of
-- 'any' with min_acr 1 never mismatches. The other columns echo the policy.
-- +goose StatementBegin
CREATE FUNCTION mod_users.holder_policy_mismatch(
  p_holder_id                BIGINT,
  p_party_id                 BIGINT,
  p_invite_time              BOOLEAN,
  OUT reason                 TEXT,
  OUT credential_rule        TEXT,
  OUT required_provider_id   TEXT,
  OUT min_acr                SMALLINT,
  OUT holder_acr             SMALLINT
)
LANGUAGE plpgsql
STABLE
SECURITY INVOKER
SET search_path = pg_catalog, pg_temp
AS $$
BEGIN
  SELECT pol.credential_rule, pol.required_oidc_provider_id, pol.min_acr
    INTO credential_rule, required_provider_id, min_acr
    FROM mod_users.party_authn_policies pol WHERE pol.party_id = p_party_id;
  IF NOT FOUND THEN
    credential_rule := 'any';
    required_provider_id := NULL;
    min_acr := 1;
  END IF;

  SELECT CASE
           WHEN count(*) = 0 THEN NULL
           ELSE LEAST(3, GREATEST(max(k.base_acr), CASE WHEN count(DISTINCT k.factor) >= 2 THEN 2 ELSE 1 END))
         END::SMALLINT
    INTO holder_acr
    FROM mod_users.credentials c
    JOIN mod_users.credential_kinds k ON k.kind = c.kind
   WHERE c.principal_id = p_holder_id
     AND c.revoked_at IS NULL
     AND (c.expires_at IS NULL OR c.expires_at > now())
     AND k.class = 'interactive'
     AND (c.confined_to_party_id IS NULL OR c.confined_to_party_id = p_party_id);

  IF credential_rule = 'authorized' AND p_invite_time THEN
    -- No profile exists yet, so no credential can be authorized for the party:
    -- the rule is met by any usable interactive credential (holder_acr is
    -- non-NULL exactly then). Authorization follows acceptance.
    IF holder_acr IS NULL THEN
      reason := 'credential_not_authorized';
      RETURN;
    END IF;
  ELSIF credential_rule = 'authorized' AND NOT EXISTS (
    SELECT 1
      FROM mod_users.credentials c
      JOIN mod_users.credential_kinds k ON k.kind = c.kind
     WHERE c.principal_id = p_holder_id
       AND c.revoked_at IS NULL
       AND (c.expires_at IS NULL OR c.expires_at > now())
       AND k.class = 'interactive'
       AND (c.confined_to_party_id = p_party_id
            OR (c.confined_to_party_id IS NULL
                AND EXISTS (
                  SELECT 1 FROM mod_users.credential_party_authorizations cpa
                   WHERE cpa.credential_id = c.id AND cpa.party_id = p_party_id
                )))
  ) THEN
    reason := 'credential_not_authorized';
    RETURN;
  END IF;

  IF credential_rule = 'provider' AND NOT EXISTS (
    SELECT 1
      FROM mod_users.credentials c
      JOIN mod_users.cred_oidc o ON o.credential_id = c.id
     WHERE c.principal_id = p_holder_id
       AND c.revoked_at IS NULL
       AND (c.expires_at IS NULL OR c.expires_at > now())
       AND (c.confined_to_party_id IS NULL OR c.confined_to_party_id = p_party_id)
       AND o.provider_id = required_provider_id
  ) THEN
    reason := 'provider_required';
    RETURN;
  END IF;

  IF min_acr > 1 AND (holder_acr IS NULL OR holder_acr < min_acr) THEN
    reason := 'acr_too_low';
  END IF;
END;
$$;
-- +goose StatementEnd

-- profile_availability(p_session_id, p_profile_id): whether the session may
-- act as the profile, with every unrevoked session factor counted and sessions.acr as
-- the assurance (for an impersonation session, the parent session's factors and
-- acr, with the credential rule skipped only for an operator administrator;
-- session_policy_factors). Used by login landing, profile switch, refresh,
-- per-request liveness and the profile list.
-- +goose StatementBegin
CREATE FUNCTION mod_users.profile_availability(p_session_id BIGINT, p_profile_id BIGINT) RETURNS TEXT
LANGUAGE sql
STABLE
SECURITY INVOKER
SET search_path = pg_catalog, pg_temp
AS $$
  SELECT COALESCE((
    SELECT mod_users.profile_availability_for(
             s.principal_id,
             s.impersonated_profile_id,
             pf.credential_ids,
             pf.acr,
             p_profile_id,
             pf.skip_credential_rule)
      FROM mod_users.sessions s
     CROSS JOIN LATERAL mod_users.session_policy_factors(s.id, NULL) pf
     WHERE s.id = p_session_id
  ), 'not_held');
$$;
-- +goose StatementEnd

-- profile_gate(p_session_id, p_profile_id): profile_availability without the
-- profile's status check (profile_gate_for over the session's policy factors,
-- session_policy_factors): whether the session satisfies the profile's gate
-- whatever the profile's status. Used by the leave path for a suspended
-- profile.
-- +goose StatementBegin
CREATE FUNCTION mod_users.profile_gate(p_session_id BIGINT, p_profile_id BIGINT) RETURNS TEXT
LANGUAGE sql
STABLE
SECURITY INVOKER
SET search_path = pg_catalog, pg_temp
AS $$
  SELECT COALESCE((
    SELECT mod_users.profile_gate_for(
             s.principal_id,
             s.impersonated_profile_id,
             pf.credential_ids,
             pf.acr,
             p_profile_id,
             pf.skip_credential_rule)
      FROM mod_users.sessions s
     CROSS JOIN LATERAL mod_users.session_policy_factors(s.id, NULL) pf
     WHERE s.id = p_session_id
  ), 'not_held');
$$;
-- +goose StatementEnd

-- profile_availability_excluding(p_session_id, p_profile_id,
-- p_exclude_credential_id): profile_availability evaluated as if the session
-- did not hold the credential p_exclude_credential_id: it is left out of the
-- factors and of the acr. It answers "does the session satisfy the party's
-- gate through another factor", so a credential cannot authorize itself (a
-- party admin or a gated session holder writing a credential_party_authorizations
-- row). The rules themselves stay in profile_availability_for. An
-- impersonation session is evaluated on its parent session's factors with
-- the credential left out of them, and the parent's stored acr
-- (session_policy_factors). A NULL p_exclude_credential_id excludes nothing,
-- so the result equals profile_availability.
-- +goose StatementBegin
CREATE FUNCTION mod_users.profile_availability_excluding(
  p_session_id BIGINT, p_profile_id BIGINT, p_exclude_credential_id BIGINT
) RETURNS TEXT
LANGUAGE sql
STABLE
SECURITY INVOKER
SET search_path = pg_catalog, pg_temp
AS $$
  SELECT COALESCE((
    SELECT mod_users.profile_availability_for(
             s.principal_id,
             s.impersonated_profile_id,
             pf.credential_ids,
             CASE WHEN s.impersonated_profile_id IS NULL
                  THEN mod_users.session_acr_excluding(s.id, p_exclude_credential_id)
                  ELSE pf.acr END,
             p_profile_id,
             pf.skip_credential_rule)
      FROM mod_users.sessions s
     CROSS JOIN LATERAL mod_users.session_policy_factors(s.id, p_exclude_credential_id) pf
     WHERE s.id = p_session_id
  ), 'not_held');
$$;
-- +goose StatementEnd

-- credential_profile_availability(p_credential_id, p_profile_id): the same
-- rules with the single credential as the only factor and its kind's base_acr
-- as the assurance. Used by client credentials and SSH.
-- +goose StatementBegin
CREATE FUNCTION mod_users.credential_profile_availability(p_credential_id BIGINT, p_profile_id BIGINT) RETURNS TEXT
LANGUAGE sql
STABLE
SECURITY INVOKER
SET search_path = pg_catalog, pg_temp
AS $$
  SELECT COALESCE((
    SELECT mod_users.profile_availability_for(
             c.principal_id,
             NULL,
             ARRAY[c.id],
             k.base_acr,
             p_profile_id,
             false)
      FROM mod_users.credentials c
      JOIN mod_users.credential_kinds k ON k.kind = c.kind
     WHERE c.id = p_credential_id
  ), 'not_held');
$$;
-- +goose StatementEnd

-- session_meets_party_policy(p_session_id, p_credential_id, p_profile_id,
-- p_party_id): the owning-party gate of the authorization engine. Per-request
-- liveness (profile_availability) applies only the acting profile's own party;
-- an acting profile outside the party that owns the target (an outside
-- collaborator reaching the party's content through actor groups or grants, or
-- a personal profile through the SSH fallback) must also meet the owning
-- party's credential_rule and min_acr. p_party_id is one gate party of the
-- check: the target's owner (entities.owner_id; the owner argument of an
-- owner-scoped type check) or, when the target is itself a legal_entity, the
-- target (decision F: a party is its own owning party). The engine calls this
-- function once per gate party and requires every call to pass; nested
-- ownership is one level only (no ancestor walk).
--
-- True, without consulting the factors, when p_party_id is NULL, the acting
-- profile p_profile_id belongs to p_party_id, p_party_id is the acting
-- profile's own subject (a personal profile reaching its own records), the
-- acting profile is a platform operator profile (public.core_is_operator_profile;
-- decision D: a customer party's policy never binds the operator, so the
-- platform can always administer, support and impersonate; operation-level
-- min_acr and step-up still apply), or the
-- owner carries no restrictive policy (no row, or 'any' with min_acr 1; this
-- also covers owners that are not parties). Otherwise the factors are those of
-- the interactive session p_session_id (session_policy_factors: its unrevoked
-- factors and sessions.acr; for an impersonation session, which holds no
-- factors of its own, the parent session's factors and acr, so the
-- administrator's authentication must meet the party's policy, and the
-- credential rule is skipped only when the administrator is a platform
-- operator, decision G as resolved), else of the single unrevoked credential
-- p_credential_id with its kind's base_acr, and the result is
-- party_policy_gate_for(...) = 'available': a profile_policy_confirmations row
-- for (p_profile_id, p_party_id) exempts the credential rule only, never
-- min_acr. An unknown session, a revoked or unknown credential, or neither id
-- fails closed (false): trusted contexts without a session or credential are
-- the engine's to skip, not this function's.
-- +goose StatementBegin
CREATE FUNCTION mod_users.session_meets_party_policy(
  p_session_id    BIGINT,
  p_credential_id BIGINT,
  p_profile_id    BIGINT,
  p_party_id      BIGINT
) RETURNS BOOLEAN
LANGUAGE plpgsql
STABLE
SECURITY INVOKER
SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
  v_credential_ids BIGINT[];
  v_acr            SMALLINT;
  v_skip_rule      BOOLEAN := false;
BEGIN
  IF p_party_id IS NULL THEN
    RETURN true;
  END IF;

  IF EXISTS (
    SELECT 1 FROM public.profiles p
     WHERE p.entity_id = p_profile_id AND p.party_id = p_party_id
  ) THEN
    RETURN true;
  END IF;

  IF p_party_id = public.profile_own_subject(p_profile_id) THEN
    RETURN true;
  END IF;

  IF public.core_is_operator_profile(p_profile_id) THEN
    RETURN true;
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM mod_users.party_authn_policies pol
     WHERE pol.party_id = p_party_id
       AND (pol.credential_rule <> 'any' OR pol.min_acr > 1)
  ) THEN
    RETURN true;
  END IF;

  IF p_session_id IS NOT NULL THEN
    SELECT pf.acr, pf.skip_credential_rule, pf.credential_ids
      INTO v_acr, v_skip_rule, v_credential_ids
      FROM mod_users.session_policy_factors(p_session_id, NULL) pf;
    IF v_credential_ids IS NULL THEN
      -- Unknown session: session_policy_factors returns all NULL.
      RETURN false;
    END IF;
  ELSIF p_credential_id IS NOT NULL THEN
    SELECT ARRAY[c.id], k.base_acr
      INTO v_credential_ids, v_acr
      FROM mod_users.credentials c
      JOIN mod_users.credential_kinds k ON k.kind = c.kind
     WHERE c.id = p_credential_id
       AND c.revoked_at IS NULL;
    IF NOT FOUND THEN
      RETURN false;
    END IF;
  ELSE
    RETURN false;
  END IF;

  RETURN mod_users.party_policy_gate_for(
    p_party_id, p_profile_id, COALESCE(v_skip_rule, false), v_credential_ids, v_acr) = 'available';
END;
$$;
-- +goose StatementEnd

-- session_liveness: the per-request check for an interactive access token, in
-- one round trip. Always returns exactly one row (OUT parameters rather than
-- RETURNS TABLE, so sqlc can type the result). status is 'ok' or the first
-- failure among session_revoked (also for an unknown session),
-- session_expired, parent_session_ended (an impersonation whose parent is
-- revoked or expired), principal_mismatch (the token's sub is not the session
-- principal), not_held (also for an unknown profile), or another
-- profile_availability lock reason. Under impersonation,
-- impersonator_principal_id is the parent session's own principal.
-- +goose StatementBegin
CREATE FUNCTION mod_users.session_liveness(
  p_session_uuid                UUID,
  p_principal_uuid              UUID,
  p_profile_uuid                UUID,
  OUT session_id                BIGINT,
  OUT principal_id              BIGINT,
  OUT profile_id                BIGINT,
  OUT party_id                  BIGINT,
  OUT impersonator_principal_id BIGINT,
  OUT app_id                    BIGINT,
  OUT acr                       SMALLINT,
  OUT amr                       TEXT[],
  OUT auth_time                 TIMESTAMPTZ,
  OUT status                    TEXT
)
LANGUAGE plpgsql
STABLE
SECURITY INVOKER
SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
  v_session        mod_users.sessions%ROWTYPE;
  v_principal_uuid UUID;
  v_parent_live    BOOLEAN;
BEGIN
  SELECT s.* INTO v_session FROM mod_users.sessions s WHERE s.uuid = p_session_uuid;
  IF NOT FOUND THEN
    status := 'session_revoked';
    RETURN;
  END IF;

  session_id := v_session.id;
  principal_id := v_session.principal_id;
  app_id := v_session.app_id;
  acr := v_session.acr;
  amr := v_session.amr;
  auth_time := v_session.auth_time;

  SELECT pf.entity_id, pf.party_id INTO profile_id, party_id
    FROM public.entities e
    JOIN public.profiles pf ON pf.entity_id = e.id
   WHERE e.uuid = p_profile_uuid;

  IF v_session.impersonator_session_id IS NOT NULL THEN
    SELECT parent.principal_id, parent.revoked_at IS NULL AND parent.expires_at > now()
      INTO impersonator_principal_id, v_parent_live
      FROM mod_users.sessions parent
     WHERE parent.id = v_session.impersonator_session_id;
  END IF;

  SELECT e.uuid INTO v_principal_uuid FROM public.entities e WHERE e.id = v_session.principal_id;

  IF v_session.revoked_at IS NOT NULL THEN
    status := 'session_revoked';
  ELSIF v_session.expires_at <= now() THEN
    status := 'session_expired';
  ELSIF v_session.impersonator_session_id IS NOT NULL AND v_parent_live IS NOT TRUE THEN
    status := 'parent_session_ended';
  ELSIF v_principal_uuid IS DISTINCT FROM p_principal_uuid THEN
    status := 'principal_mismatch';
  ELSIF profile_id IS NULL THEN
    status := 'not_held';
  ELSE
    status := mod_users.profile_availability(v_session.id, profile_id);
    IF status = 'available' THEN
      status := 'ok';
    END IF;
  END IF;

  RETURN;
END;
$$;
-- +goose StatementEnd

-- credential_liveness: the per-request check for a client-credentials access
-- token (cid). Always returns exactly one row; acr is the kind's base_acr.
-- status is 'ok' or the first failure among credential_revoked (also for an
-- unknown credential), credential_expired, principal_mismatch, not_held (also
-- for an unknown profile), or another profile_availability lock reason.
-- +goose StatementBegin
CREATE FUNCTION mod_users.credential_liveness(
  p_credential_uuid UUID,
  p_principal_uuid  UUID,
  p_profile_uuid    UUID,
  OUT credential_id BIGINT,
  OUT principal_id  BIGINT,
  OUT profile_id    BIGINT,
  OUT party_id      BIGINT,
  OUT acr           SMALLINT,
  OUT status        TEXT
)
LANGUAGE plpgsql
STABLE
SECURITY INVOKER
SET search_path = pg_catalog, pg_temp
AS $$
DECLARE
  v_revoked_at     TIMESTAMPTZ;
  v_expires_at     TIMESTAMPTZ;
  v_principal_uuid UUID;
BEGIN
  SELECT c.id, c.principal_id, k.base_acr, c.revoked_at, c.expires_at
    INTO credential_id, principal_id, acr, v_revoked_at, v_expires_at
    FROM mod_users.credentials c
    JOIN mod_users.credential_kinds k ON k.kind = c.kind
   WHERE c.uuid = p_credential_uuid;
  IF NOT FOUND THEN
    status := 'credential_revoked';
    RETURN;
  END IF;

  SELECT pf.entity_id, pf.party_id INTO profile_id, party_id
    FROM public.entities e
    JOIN public.profiles pf ON pf.entity_id = e.id
   WHERE e.uuid = p_profile_uuid;

  SELECT e.uuid INTO v_principal_uuid FROM public.entities e WHERE e.id = principal_id;

  IF v_revoked_at IS NOT NULL THEN
    status := 'credential_revoked';
  ELSIF v_expires_at IS NOT NULL AND v_expires_at <= now() THEN
    status := 'credential_expired';
  ELSIF v_principal_uuid IS DISTINCT FROM p_principal_uuid THEN
    status := 'principal_mismatch';
  ELSIF profile_id IS NULL THEN
    status := 'not_held';
  ELSE
    status := mod_users.credential_profile_availability(credential_id, profile_id);
    IF status = 'available' THEN
      status := 'ok';
    END IF;
  END IF;

  RETURN;
END;
$$;
-- +goose StatementEnd

-- ===========================================================================
-- Access function stub
-- ===========================================================================

-- accessible_user_ids_for_actor: the row-level scoping function the principal
-- list path joins against. Same shape as mod-core's stubs (public, empty set):
-- at startup the host's setup.ApplyFuncs replaces the body via CREATE OR
-- REPLACE FUNCTION with the generated policy. p_actor_entity_id is the acting
-- profile's entity id.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.accessible_user_ids_for_actor(p_actor_entity_id BIGINT, p_op_ids INT[])
RETURNS TABLE(entity_id BIGINT) LANGUAGE sql STABLE AS $$
    SELECT 0::BIGINT AS entity_id WHERE FALSE
$$;
-- +goose StatementEnd

-- +goose Down

-- Reverse order of the Up section. Objects are dropped by name, never with
-- CASCADE, so a dependency another module added on a mod_users table (for
-- example a foreign key to mod_users.users) stops the rollback instead of
-- being silently removed with it.

DROP FUNCTION IF EXISTS public.accessible_user_ids_for_actor(BIGINT, INT[]);

DROP FUNCTION IF EXISTS mod_users.credential_liveness(UUID, UUID, UUID);
DROP FUNCTION IF EXISTS mod_users.session_liveness(UUID, UUID, UUID);
DROP FUNCTION IF EXISTS mod_users.session_meets_party_policy(BIGINT, BIGINT, BIGINT, BIGINT);
DROP FUNCTION IF EXISTS mod_users.credential_profile_availability(BIGINT, BIGINT);
DROP FUNCTION IF EXISTS mod_users.profile_availability_excluding(BIGINT, BIGINT, BIGINT);
DROP FUNCTION IF EXISTS mod_users.profile_availability(BIGINT, BIGINT);
DROP FUNCTION IF EXISTS mod_users.profile_gate(BIGINT, BIGINT);
DROP FUNCTION IF EXISTS mod_users.holder_policy_mismatch(BIGINT, BIGINT, BOOLEAN);
DROP FUNCTION IF EXISTS mod_users.profile_availability_for(BIGINT, BIGINT, BIGINT[], SMALLINT, BIGINT, BOOLEAN);
DROP FUNCTION IF EXISTS mod_users.profile_gate_for(BIGINT, BIGINT, BIGINT[], SMALLINT, BIGINT, BOOLEAN);
DROP FUNCTION IF EXISTS mod_users.party_policy_gate_for(BIGINT, BIGINT, BOOLEAN, BIGINT[], SMALLINT);
DROP FUNCTION IF EXISTS mod_users.session_policy_factors(BIGINT, BIGINT);
DROP FUNCTION IF EXISTS mod_users.session_acr(BIGINT);
DROP FUNCTION IF EXISTS mod_users.session_acr_excluding(BIGINT, BIGINT);

DROP TABLE IF EXISTS mod_users.auth_signing_keys;

DROP FUNCTION IF EXISTS mod_users.create_challenge_with_cooldown(TEXT, BIGINT, BIGINT, BIGINT, TEXT, TIMESTAMPTZ, INT);
DROP TABLE IF EXISTS mod_users.auth_challenges;

DROP TABLE IF EXISTS mod_users.session_refresh_tokens;
DROP TABLE IF EXISTS mod_users.session_factors;
DROP FUNCTION IF EXISTS mod_users.session_factors_check_principal();
DROP TABLE IF EXISTS mod_users.sessions;
DROP FUNCTION IF EXISTS mod_users.sessions_check_invariants();

DROP TABLE IF EXISTS mod_users.credential_party_authorizations;
DROP TABLE IF EXISTS mod_users.party_authn_policies;

DROP TABLE IF EXISTS mod_users.cred_anon;
DROP TABLE IF EXISTS mod_users.cred_api_key;
DROP TABLE IF EXISTS mod_users.cred_ssh_key;
DROP TABLE IF EXISTS mod_users.cred_oidc;
DROP TABLE IF EXISTS mod_users.cred_email_otp;
DROP TABLE IF EXISTS mod_users.cred_password;
DROP FUNCTION IF EXISTS mod_users.cred_subtable_check();
DROP TABLE IF EXISTS mod_users.credentials;
DROP FUNCTION IF EXISTS mod_users.credentials_sync_active();
DROP FUNCTION IF EXISTS mod_users.credentials_check_invariants();

DROP TABLE IF EXISTS mod_users.oidc_providers;
DROP TABLE IF EXISTS mod_users.oidc_config;

DROP TABLE IF EXISTS mod_users.user_identifiers;

DROP TRIGGER IF EXISTS profiles_users_personal_profile ON public.profiles;
DROP TRIGGER IF EXISTS profiles_users_holder_rules ON public.profiles;
DROP TABLE IF EXISTS mod_users.users;
DROP FUNCTION IF EXISTS mod_users.users_check_personal_profile();
DROP FUNCTION IF EXISTS mod_users.profiles_check_user_holder();
DROP FUNCTION IF EXISTS mod_users.users_check_invariants();

DROP TABLE IF EXISTS mod_users.credential_kinds;

DROP SCHEMA IF EXISTS mod_users;
