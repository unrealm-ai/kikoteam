package videogen

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	repo "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/repository"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type Review struct {
	ID    string `json:"generation_id"`
	Error string `json:"error_code"`
	Known bool   `json:"has_upstream_id"`
}
type Resolution struct {
	Action string `json:"action"`
	Reason string `json:"reason"`
}

func (s *Service) Reviews(ctx context.Context, actor string) ([]Review, error) {
	if err := repo.Admin(ctx, s.DB, actor, false); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT id,coalesce(error_code,''),upstream_id IS NOT NULL FROM platform_video_generations WHERE status='pending_review' ORDER BY started_at,id LIMIT 100")
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // Iteration errors checked below.
	out := []Review{}
	for rows.Next() {
		var v Review
		if err = rows.Scan(&v.ID, &v.Error, &v.Known); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) Resolve(ctx context.Context, actor, id string, p Resolution) error {
	p.Reason = strings.TrimSpace(p.Reason)
	if !repo.UUID(id) || len(p.Reason) == 0 || len(p.Reason) > 1024 || (p.Action != "reconcile" && p.Action != "close_no_charge") {
		return service.Fail("invalid_argument", 400, "请填写有效的处理动作及原因")
	}
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		if e := repo.Admin(ctx, tx, actor, true); e != nil {
			return e
		}
		var workspace string
		var known bool
		if err := tx.QueryRowContext(ctx, "SELECT workspace_id,upstream_id IS NOT NULL FROM platform_video_generations WHERE id=$1 AND status='pending_review' FOR UPDATE", id).Scan(&workspace, &known); err != nil {
			if missing(err) {
				return service.Fail("generation_state_conflict", 409, "记录已处理")
			}
			return err
		}
		_, readErr := manager.VideoOriginal(s.Root, workspace, id)
		var absent *manager.Error
		if readErr != nil && (!errors.As(readErr, &absent) || absent.Status != 404) {
			return readErr
		}
		if p.Action == "close_no_charge" && readErr == nil {
			return service.Fail("original_available", 409, "已有固定原片，请恢复原记录")
		}
		if p.Action == "reconcile" && !known && readErr != nil {
			return service.Fail("generation_state_conflict", 409, "没有可恢复的原片或上游任务 ID")
		}
		status, bill := "queued", "pending"
		if p.Action == "close_no_charge" {
			status, bill = "failed", "no_charge"
		}
		res, e := tx.ExecContext(ctx, `UPDATE platform_video_generations SET status=$2,billing_status=$3,next_check_at=now(),check_token=NULL,reviewed_by=$4,review_reason=$5,reviewed_at=now()
 WHERE id=$1 AND status='pending_review'`, id, status, bill, actor, p.Reason)
		if e != nil {
			return e
		}
		n, e := res.RowsAffected()
		if e == nil && n == 0 {
			return service.Fail("generation_state_conflict", 409, "当前记录无法执行此操作")
		}
		return e
	})
}
