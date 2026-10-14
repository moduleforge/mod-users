-- +goose Up

-- Allow purpose='credential_change' in email_codes. The step-up code flow
-- (sendStepUpCode) inserts that purpose, which the baseline CHECK rejected.
ALTER TABLE email_codes DROP CONSTRAINT IF EXISTS email_codes_purpose_check;
ALTER TABLE email_codes
  ADD CONSTRAINT email_codes_purpose_check
  CHECK (purpose IN ('login', 'verify_email', 'credential_change'));

-- +goose Down

-- credential_change rows would violate the narrower check; remove them first.
DELETE FROM email_codes WHERE purpose = 'credential_change';
ALTER TABLE email_codes DROP CONSTRAINT IF EXISTS email_codes_purpose_check;
ALTER TABLE email_codes
  ADD CONSTRAINT email_codes_purpose_check
  CHECK (purpose IN ('login', 'verify_email'));
