-- +goose Up

-- Supporting index for the any-state cooldown probe in
-- create_email_code_with_cooldown (0103): the NOT EXISTS subquery filters on
-- (user_account_id, purpose) and range-scans created_at. The index is
-- deliberately NOT partial: the probe counts codes of any consumed state
-- (unconsumed, exhausted, or consumed), so a WHERE predicate would exclude
-- rows the probe must see. The baseline email_codes_user_account_purpose_idx
-- is partial and cannot serve it.
CREATE INDEX email_codes_cooldown_idx
  ON email_codes (user_account_id, purpose, created_at DESC);

-- Name resolution (search_path pin) for create_email_code_with_cooldown. The
-- function has no SET clause, so it resolves email_codes against the caller's
-- search_path, which a displaced or shadowing schema layout (or a
-- session-temporary table) can redirect. Pin it to pg_catalog, the schema
-- email_codes actually landed in, and pg_temp (last, so a temp relation cannot
-- shadow the real table). The schema is read at migration time via
-- current_schema(), exactly as the baseline entities trigger does, and baked
-- in with dynamic SQL; hardcoding `public` would break the displaced-schema
-- layout.
--
-- EXECUTE is intentionally NOT revoked from PUBLIC: doing so would require
-- re-granting it to the application role, which this module cannot name. The
-- function is SECURITY INVOKER, so it runs with the caller's own privileges
-- and a caller who lacks INSERT/SELECT on email_codes gains nothing by being
-- able to call it; the default PUBLIC EXECUTE grant therefore confers no
-- additional access.
-- +goose StatementBegin
DO $do$
BEGIN
  EXECUTE format(
    'ALTER FUNCTION %I.create_email_code_with_cooldown(BIGINT, TEXT, TEXT, TIMESTAMPTZ, INT) '
    'SET search_path = pg_catalog, %I, pg_temp',
    current_schema(), current_schema());
END $do$;
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
DO $do$
BEGIN
  EXECUTE format(
    'ALTER FUNCTION %I.create_email_code_with_cooldown(BIGINT, TEXT, TEXT, TIMESTAMPTZ, INT) '
    'RESET search_path',
    current_schema());
END $do$;
-- +goose StatementEnd

DROP INDEX email_codes_cooldown_idx;
