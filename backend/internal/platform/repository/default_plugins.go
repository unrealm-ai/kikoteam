package repository

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func defaultPluginPins(ctx context.Context, tx *sql.Tx) ([]service.DefaultPluginPin, error) {
	scene := "direct"
	if _, ok := service.WorkExecutionFrom(ctx); ok {
		return nil, nil
	}
	if _, ok := service.TriggerExecutionFrom(ctx); ok {
		scene = "trigger"
	}
	if _, ok := service.WebGroupExecutionFrom(ctx); ok {
		scene = "group"
	}
	if ch, ok := service.ChannelExecutionFrom(ctx); ok {
		if err := tx.QueryRowContext(ctx, "SELECT channel_chat_type FROM manager_conversations WHERE id=$1", ch.ConversationID).Scan(&scene); err != nil {
			return nil, err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT p.id,v.id,v.handler_key,v.default_tools FROM platform_default_plugin_settings s
 JOIN platform_plugins p ON p.id=s.plugin_id JOIN platform_plugin_versions v ON v.id=p.current_version_id
 WHERE s.enabled AND p.listed AND p.enabled AND p.archived_at IS NULL AND p.workspace_id IS NULL AND v.distribution='all_agents'
 AND v.execution_kind='platform_managed' AND v.default_scenes ? $1 ORDER BY p.id`, scene)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // Iteration errors are returned.
	var pins []service.DefaultPluginPin
	for rows.Next() {
		var pin service.DefaultPluginPin
		var raw []byte
		if err := rows.Scan(&pin.PluginID, &pin.VersionID, &pin.Handler, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &pin.Tools); err != nil {
			return nil, err
		}
		pins = append(pins, pin)
	}
	return pins, rows.Err()
}
