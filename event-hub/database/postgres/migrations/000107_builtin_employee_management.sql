-- +goose Up
DELETE FROM platform_capability_packages WHERE assistant_tool_set='platform';

-- +goose Down
-- Placeholder packages had no business capabilities and are not recreated.
SELECT 1;
