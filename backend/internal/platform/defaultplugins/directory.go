// Package defaultplugins reads the deployment-wide official capability directory.
package defaultplugins

import (
	"context"
	"database/sql"

	proxy "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy"
)

type reader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}
type Summary struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	Release int64  `json:"release_version"`
	Enabled bool   `json:"enabled"`
	Source  string `json:"source"`
	Status  string `json:"status"`
	Price   string `json:"credits_per_image,omitempty"`
}

// An employee view includes the validity of its payer, without identifying the payer.
func List(ctx context.Context, db reader, w, employee string) ([]Summary, error) {
	rows, err := db.QueryContext(ctx, `SELECT p.builtin_key,p.name,v.version,s.enabled,c.unit_price_units,
 EXISTS(SELECT FROM platform_agent_model_keys k JOIN platform_members m ON m.user_id=k.payer_account_id AND m.workspace_id=k.workspace_id AND m.role='owner' AND m.status='active'
 WHERE k.workspace_id=nullif($1,'')::uuid AND k.agent_id=nullif($2,'')::uuid)
 FROM platform_plugins p JOIN platform_plugin_versions v ON v.id=p.current_version_id JOIN platform_default_plugin_settings s ON s.plugin_id=p.id
 LEFT JOIN platform_image_generation_config_versions c ON c.plugin_id=p.id AND c.version=s.active_config_version
 WHERE p.workspace_id IS NULL AND p.listed AND p.enabled AND p.archived_at IS NULL AND v.distribution='all_agents' ORDER BY p.name,p.id`, w, employee)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // Iteration errors are returned below.
	out := []Summary{}
	for rows.Next() {
		var v Summary
		var price sql.NullInt64
		var payer bool
		if err = rows.Scan(&v.Key, &v.Name, &v.Release, &v.Enabled, &price, &payer); err != nil {
			return nil, err
		}
		v.Status = "available"
		v.Source = "platform"
		switch {
		case v.Key == "image-generation" && !price.Valid:
			v.Status = "unconfigured"
		case !v.Enabled:
			v.Status = "disabled"
		case price.Valid && employee != "" && !payer:
			v.Status = "payer_binding_required"
		}
		if price.Valid {
			v.Price = proxy.Amount(price.Int64)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
