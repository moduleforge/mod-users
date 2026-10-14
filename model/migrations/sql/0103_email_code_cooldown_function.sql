-- +goose Up

-- create_email_code_with_cooldown atomically mints one email code per
-- (user, purpose) per cooldown window. A single INSERT ... WHERE NOT EXISTS is
-- not enough under READ COMMITTED: two concurrent statements each take their
-- snapshot before either commits, so both see no recent code and both insert.
-- The transaction-scoped advisory lock serializes concurrent callers for the
-- same (user, purpose), and because each statement inside a plpgsql function
-- takes a fresh snapshot, the NOT EXISTS check made after acquiring the lock
-- sees the row the previous lock holder committed.
--
-- The cooldown considers a code of ANY consumed state (unconsumed, exhausted,
-- or consumed by success) created within p_cooldown_secs. Returns the inserted
-- row, or no row when a recent code exists.
-- +goose StatementBegin
CREATE FUNCTION create_email_code_with_cooldown(
  p_user_account_id BIGINT,
  p_code_hash       TEXT,
  p_purpose         TEXT,
  p_expires_at      TIMESTAMPTZ,
  p_cooldown_secs   INT
) RETURNS SETOF email_codes
LANGUAGE plpgsql AS $$
BEGIN
  PERFORM pg_advisory_xact_lock(
    hashtextextended('email_code_cooldown:' || p_user_account_id::text || ':' || p_purpose, 0));

  RETURN QUERY
  INSERT INTO email_codes (user_account_id, code_hash, purpose, expires_at)
  SELECT p_user_account_id, p_code_hash, p_purpose, p_expires_at
  WHERE NOT EXISTS (
    SELECT 1
    FROM email_codes ec
    WHERE ec.user_account_id = p_user_account_id
      AND ec.purpose = p_purpose
      AND ec.created_at > clock_timestamp() - make_interval(secs => p_cooldown_secs)
  )
  RETURNING *;
END;
$$;
-- +goose StatementEnd

-- +goose Down

DROP FUNCTION create_email_code_with_cooldown(BIGINT, TEXT, TEXT, TIMESTAMPTZ, INT);
