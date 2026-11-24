-- +goose Up
CREATE TABLE manager_task_detail_deliveries (
    delivery_id uuid PRIMARY KEY REFERENCES manager_channel_deliveries(id) ON DELETE CASCADE,
    grant_id uuid NOT NULL UNIQUE REFERENCES manager_task_detail_grants(id) ON DELETE CASCADE,
    source_input_id uuid NOT NULL REFERENCES manager_inputs(id) ON DELETE CASCADE,
    title text NOT NULL,
    state text NOT NULL,
    sealed_url bytea,
    presentation text CHECK (presentation IN ('task_detail_card','task_detail_link')),
    context_settled_at timestamptz
);
CREATE INDEX manager_task_detail_delivery_source ON manager_task_detail_deliveries (source_input_id);
CREATE INDEX manager_task_detail_delivery_context ON manager_task_detail_deliveries (delivery_id) WHERE context_settled_at IS NULL;

-- +goose Down
DELETE FROM manager_channel_deliveries WHERE kind LIKE 'task_detail:%';
DROP TABLE manager_task_detail_deliveries;
