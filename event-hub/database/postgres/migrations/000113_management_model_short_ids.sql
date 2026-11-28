-- +goose Up
-- Both catalogs allocate non-recycled model references from the same sequence.
-- UUIDs remain the primary keys and all existing foreign keys are unchanged.
CREATE SEQUENCE platform_management_model_number_seq AS bigint NO CYCLE;
ALTER TABLE platform_model_profiles
    ADD COLUMN management_number bigint NOT NULL DEFAULT nextval('platform_management_model_number_seq'),
    ADD UNIQUE (management_number);
ALTER TABLE platform_proxy_models
    ADD COLUMN management_number bigint NOT NULL DEFAULT nextval('platform_management_model_number_seq'),
    ADD UNIQUE (management_number);

-- +goose Down
ALTER TABLE platform_proxy_models DROP COLUMN management_number;
ALTER TABLE platform_model_profiles DROP COLUMN management_number;
DROP SEQUENCE platform_management_model_number_seq;
