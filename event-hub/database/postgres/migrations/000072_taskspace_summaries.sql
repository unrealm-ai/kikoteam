-- +goose Up
-- Intentionally separate from result_summary, which may contain original model output.
CREATE TABLE manager_task_space_summaries (
 turn_id uuid PRIMARY KEY REFERENCES manager_turns(id) ON DELETE CASCADE,
 progress_text text CHECK (char_length(progress_text) BETWEEN 1 AND 600),
 progress_updated_at timestamptz,
 result_text text CHECK (char_length(result_text) BETWEEN 1 AND 1600),
 result_updated_at timestamptz,
 CHECK ((progress_text IS NULL) = (progress_updated_at IS NULL)),
 CHECK ((result_text IS NULL) = (result_updated_at IS NULL)),
 CHECK (progress_text IS NOT NULL OR result_text IS NOT NULL)
);

-- +goose Down
DROP TABLE manager_task_space_summaries;
