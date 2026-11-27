-- +goose Up
ALTER TABLE platform_workspaces ALTER COLUMN starter_pending SET DEFAULT false;
UPDATE platform_workspaces SET starter_pending = false WHERE starter_pending;

-- +goose Down
ALTER TABLE platform_workspaces ALTER COLUMN starter_pending SET DEFAULT true;
