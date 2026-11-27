package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

// SyncCatalog serializes releases and publishes the entire catalog atomically.
func SyncCatalog(ctx context.Context, db *sql.DB, catalog service.TemplateCatalog, assets service.SkillAssets) error {
	data, err := json.Marshal(catalog)
	if err != nil {
		return err
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // Commit or the original error determines the outcome.
	var revision int64
	var previous string
	if err = tx.QueryRowContext(ctx, "SELECT revision,digest FROM platform_template_catalog_state WHERE singleton FOR UPDATE").Scan(&revision, &previous); err != nil {
		return err
	}
	if catalog.Revision < revision {
		return fmt.Errorf("catalog downgrade from %d to %d refused", revision, catalog.Revision)
	}
	if catalog.Revision == revision && previous != digest {
		return fmt.Errorf("catalog contents changed without incrementing revision %d", revision)
	}
	// Immutable assets must be complete before any competing release can read
	// them. Use the same database lock for staging as for publishing.
	versions, err := stageCatalog(ctx, catalog, assets)
	if err != nil {
		return err
	}
	if catalog.Revision == revision {
		return tx.Commit()
	}
	if err = syncCatalogResources(ctx, tx, catalog, versions); err != nil {
		return err
	}
	keys := make([]string, 0, len(catalog.Entries))
	for _, entry := range catalog.Entries {
		keys = append(keys, entry.Key)
		if err = applyCatalogEntry(ctx, tx, catalog.Revision, entry); err != nil {
			return fmt.Errorf("%s: %w", entry.Key, err)
		}
	}
	var missing string
	err = tx.QueryRowContext(ctx, "SELECT builtin_key FROM platform_agent_templates WHERE builtin_key IS NOT NULL AND NOT (builtin_key=ANY($1::text[])) LIMIT 1", keys).Scan(&missing)
	if err == nil {
		return fmt.Errorf("missing catalog entry %s; keep its file and set listed=false", missing)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE platform_template_catalog_state SET revision=$1,digest=$2 WHERE singleton", catalog.Revision, digest); err != nil {
		return err
	}
	return tx.Commit()
}

func applyCatalogEntry(ctx context.Context, tx *sql.Tx, revision int64, entry service.CatalogEntry) error {
	content, err := json.Marshal(entry.Content)
	if err != nil {
		return err
	}
	var id, versionID string
	var seq int64
	var equal bool
	err = tx.QueryRowContext(ctx, `SELECT t.id,v.id,v.sequence,v.content=$2::jsonb
 FROM platform_agent_templates t JOIN platform_agent_template_versions v ON v.id=t.latest_version_id
 WHERE t.builtin_key=$1 FOR UPDATE OF t`, entry.Key, content).Scan(&id, &versionID, &seq, &equal)
	create := errors.Is(err, sql.ErrNoRows)
	if err != nil && !create {
		return err
	}
	if create {
		id = service.NewID()
	}
	if !equal {
		versionID = service.NewID()
		_, err = tx.ExecContext(ctx, `INSERT INTO platform_agent_template_versions
   (id,template_id,sequence,content,change_note,published_at,catalog_revision)
   VALUES($1,$2,$3,$4,'官方目录发布',now(),$5)`, versionID, id, seq+1, content, revision)
		if err != nil {
			return err
		}
		if err = saveTemplateRefs(ctx, tx, versionID, entry.Content.Resources); err != nil {
			return err
		}
	}
	if create {
		_, err = tx.ExecContext(ctx, `INSERT INTO platform_agent_templates
   (id,builtin_key,scope,latest_version_id,published_version_id,recommended_rank,is_listed)
   VALUES($1,$2,'official',$3,$3,$4,$5)`, id, entry.Key, versionID, entry.Rank, *entry.Listed)
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE platform_agent_templates SET latest_version_id=$2,published_version_id=$2,
 recommended_rank=$3,is_listed=$4,version=version+1,updated_at=now() WHERE id=$1
 AND (latest_version_id,recommended_rank,is_listed) IS DISTINCT FROM ($2::uuid,$3::integer,$4::boolean)`, id, versionID, entry.Rank, *entry.Listed)
	return err
}
