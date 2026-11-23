-- +goose Up
CREATE TABLE manager_dingtalk_proactive_send_observations (
    binding_id uuid PRIMARY KEY REFERENCES platform_channel_bindings(id) ON DELETE CASCADE,
    connection_generation bigint NOT NULL CHECK (connection_generation > 0),
    authorized_at timestamptz NOT NULL,
    attempt_token uuid NOT NULL,
    permission_missing boolean NOT NULL
);

-- +goose Down
DROP TABLE manager_dingtalk_proactive_send_observations;
