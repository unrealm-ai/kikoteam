package imagegen

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	proxy "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy"
	repo "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/repository"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type Filter struct {
	Account, Agent, Status, Billing string
	From, To                        *time.Time
}
type Usage struct {
	Result
	Account   string    `json:"payer_account_id"`
	Agent     string    `json:"agent_id"`
	AgentName string    `json:"agent_name"`
	Started   time.Time `json:"started_at"`
}
type cursor struct {
	At        time.Time
	ID, Scope string
}

func (s *Service) List(ctx context.Context, actor string, f Filter, raw string, limit int) (any, error) {
	if f.Account != actor {
		if err := repo.Admin(ctx, s.DB, actor, false); err != nil {
			return nil, err
		}
	}
	encoded, e := json.Marshal(f)
	if e != nil {
		return nil, e
	}
	scope := fmt.Sprintf("%x", sha256.Sum256(encoded))
	var after cursor
	var at any
	if raw != "" {
		b, e := base64.RawURLEncoding.DecodeString(raw)
		if e != nil || json.Unmarshal(b, &after) != nil || after.Scope != scope || !repo.UUID(after.ID) || after.At.IsZero() {
			return nil, service.Fail("invalid_cursor", 400, "出图记录游标无效")
		}
		at = after.At
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT g.id,g.payer_account_id,g.agent_id,a.name,g.generation_status,g.billing_status,g.unit_price_units,g.image_count,g.started_at,coalesce(g.error_code,'')
 FROM platform_image_generations g JOIN platform_agents a ON a.id=g.agent_id WHERE ($1='' OR g.payer_account_id=nullif($1,'')::uuid)
 AND ($2='' OR g.agent_id=nullif($2,'')::uuid) AND ($3='' OR g.generation_status=$3) AND ($4='' OR g.billing_status=$4)
 AND ($5::timestamptz IS NULL OR g.started_at >= $5) AND ($6::timestamptz IS NULL OR g.started_at<$6)
 AND ($7::timestamptz IS NULL OR (g.started_at,g.id)<($7,nullif($8,'')::uuid))
 ORDER BY g.started_at DESC,g.id DESC LIMIT $9`, f.Account, f.Agent, f.Status, f.Billing, f.From, f.To, at, after.ID, limit+1)
	if e != nil {
		return nil, e
	}
	defer rows.Close() //nolint:errcheck // Iteration errors are checked.
	items := []Usage{}
	next := ""
	for rows.Next() {
		if len(items) == limit {
			last := items[len(items)-1]
			b, e := json.Marshal(cursor{last.Started, last.ID, scope})
			if e != nil {
				return nil, e
			}
			next = base64.RawURLEncoding.EncodeToString(b)
			break
		}
		var u Usage
		var price int64
		if e = rows.Scan(&u.ID, &u.Account, &u.Agent, &u.AgentName, &u.Status, &u.Billing, &price, &u.Count, &u.Started, &u.Error); e != nil {
			return nil, e
		}
		u.Price = proxy.Amount(price)
		switch u.Billing {
		case "settled":
			charge := u.Price
			u.Charged = &charge
		case "no_charge":
			zero := proxy.Amount(0)
			u.Charged = &zero
		}
		items = append(items, u)
	}
	return map[string]any{"items": items, "next_cursor": next}, rows.Err()
}
func (s *Service) recoverOne(ctx context.Context, id string) error {
	var workspace, size, state string
	err := s.DB.QueryRowContext(ctx, `SELECT g.workspace_id,c.supported_sizes->>g.aspect_ratio,g.generation_status FROM platform_image_generations g
 JOIN platform_image_generation_config_versions c ON (c.plugin_id,c.version)=(g.plugin_id,g.config_version)
 WHERE g.id=$1 AND g.billing_status='pending'`, id).Scan(&workspace, &size, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if state == "ready" {
		return s.settle(ctx, id)
	}
	data, err := manager.ImageOriginal(s.Root, workspace, id)
	if err == nil {
		if err = s.recordOriginal(ctx, id, size, data, ""); err != nil {
			return err
		}
		return s.settle(ctx, id)
	}
	var problem *manager.Error
	if !errors.As(err, &problem) || problem.Status != 404 {
		return err
	}
	// A committed admission without a dispatch cannot have reached the provider.
	_, err = s.DB.ExecContext(ctx, `UPDATE platform_image_generations SET
 generation_status=CASE WHEN generation_status='admitted' THEN 'failed' ELSE 'pending_review' END,
 billing_status=CASE WHEN generation_status='admitted' THEN 'no_charge' ELSE 'pending' END,
 image_count=CASE WHEN generation_status='admitted' THEN 0 END,
 finished_at=CASE WHEN generation_status='admitted' THEN now() END,
 error_code=CASE WHEN generation_status='admitted' THEN 'dispatch_expired' ELSE 'generation_pending_review' END,
 deadline_at=now()+interval '10 minutes'
 WHERE id=$1 AND generation_status IN ('dispatched','admitted','pending_review') AND deadline_at<now()`, id)
	return err
}
func (s *Service) Reconcile(ctx context.Context) error {
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM platform_image_generations WHERE billing_status='pending' AND (generation_status='ready' OR deadline_at<now()-interval '30 seconds') ORDER BY deadline_at,id LIMIT 50`)
	if err != nil {
		return err
	}
	defer rows.Close() //nolint:errcheck // Check iteration and close before starting recovery.
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err = errors.Join(err, rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, id := range ids {
		err = errors.Join(err, s.recoverOne(ctx, id))
	}
	return err
}
func (s *Service) Resolve(ctx context.Context, actor, id, action, reason string) (Result, error) {
	if err := repo.Admin(ctx, s.DB, actor, true); err != nil {
		return Result{}, err
	}
	err := s.Transaction(ctx, func(tx *sql.Tx) error {
		if e := repo.Admin(ctx, tx, actor, true); e != nil {
			return e
		}
		var state, workspace string
		if e := tx.QueryRowContext(ctx, "SELECT generation_status,workspace_id FROM platform_image_generations WHERE id=$1 FOR UPDATE", id).Scan(&state, &workspace); e != nil {
			return repo.Missing(e)
		}
		if state != "pending_review" {
			return service.Fail("generation_state_conflict", 409, "只可处理待核对的记录")
		}
		query := `UPDATE platform_image_generations SET resolved_by=$2,resolved_at=now(),resolution_reason=$3 WHERE id=$1`
		if action == "close_no_charge" {
			_, err := manager.ImageOriginal(s.Root, workspace, id)
			if err == nil {
				return service.Fail("original_available", 409, "已有固定原图，请核对并按受理单价结算")
			}
			var absent *manager.Error
			if !errors.As(err, &absent) || absent.Status != 404 {
				return err
			}
			query = `UPDATE platform_image_generations SET generation_status='failed',billing_status='no_charge',image_count=0,finished_at=now(),resolved_by=$2,resolved_at=now(),resolution_reason=$3 WHERE id=$1`
		}
		_, e := tx.ExecContext(ctx, query, id, actor, reason)
		return e
	})
	if err == nil && action == "reconcile" {
		err = s.recoverOne(ctx, id)
	}
	if err != nil {
		return Result{}, err
	}
	return s.result(ctx, id)
}
