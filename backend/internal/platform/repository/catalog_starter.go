package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type starterCandidate struct {
	w, a    string
	request service.TemplateCreation
}

func starterCandidates(ctx context.Context, db *sql.DB) ([]starterCandidate, error) {
	rows, err := db.QueryContext(ctx, `SELECT w.id, owner.user_id, t.id, t.published_version_id, coalesce(m.id::text,''), CASE WHEN m.id IS NULL THEN pm.id::text ELSE '' END
 FROM platform_workspaces w
 JOIN platform_members owner ON owner.workspace_id=w.id AND owner.role='owner' AND owner.status='active'
 JOIN platform_agent_templates t ON t.builtin_key='market-reporter' AND t.is_listed
 LEFT JOIN LATERAL (SELECT id FROM platform_model_profiles WHERE workspace_id=w.id AND enabled AND validated ORDER BY is_default DESC,created_at,id LIMIT 1) m ON true
 LEFT JOIN platform_proxy_models pm ON pm.is_default AND pm.enabled AND pm.validated
 WHERE w.starter_pending AND (m.id IS NOT NULL OR pm.id IS NOT NULL) ORDER BY w.created_at,w.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // Iteration returns Rows.Err below.
	var ready []starterCandidate
	for rows.Next() {
		var c starterCandidate
		if err = rows.Scan(&c.w, &c.a, &c.request.TemplateID, &c.request.TemplateVersionID, &c.request.ModelProfileID, &c.request.PlatformModelID); err != nil {
			return nil, err
		}
		c.request.Name = "行情简报员"
		c.request.Starter = true
		ready = append(ready, c)
	}
	return ready, rows.Err()
}

// The migration marks pre-existing workspaces as already handled. New spaces
// wait for a usable model. CommitCreation consumes the marker atomically.
func ProvisionCatalogStarters(ctx context.Context, db *sql.DB, templates *service.TemplateService) error {
	ready, err := starterCandidates(ctx, db)
	if err != nil {
		return err
	}
	for _, c := range ready {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_, e := templates.CreateFromTemplate(ctx, c.w, c.a, c.request)
		err = errors.Join(err, e)
	}
	return err
}
