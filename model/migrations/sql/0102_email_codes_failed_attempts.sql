-- +goose Up

-- Count of failed verification attempts per code; used to exhaust a code after
-- a bounded number of wrong guesses.
ALTER TABLE email_codes ADD COLUMN failed_attempts INT NOT NULL DEFAULT 0;

-- +goose Down

ALTER TABLE email_codes DROP COLUMN failed_attempts;
