-- +goose Up
ALTER TABLE platform_email_challenges DROP CONSTRAINT platform_email_challenges_purpose_check;
ALTER TABLE platform_email_challenges ADD CONSTRAINT platform_email_challenges_purpose_check
 CHECK (purpose IN ('register', 'login', 'reset_password', 'verify_login'));

-- +goose Down
DELETE FROM platform_email_challenges WHERE purpose = 'verify_login';
ALTER TABLE platform_email_challenges DROP CONSTRAINT platform_email_challenges_purpose_check;
ALTER TABLE platform_email_challenges ADD CONSTRAINT platform_email_challenges_purpose_check
 CHECK (purpose IN ('register', 'login', 'reset_password'));
