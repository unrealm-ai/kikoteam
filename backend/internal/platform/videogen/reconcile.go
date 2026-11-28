package videogen

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	repo "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/repository"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type job struct {
	id, token, status, upstream                              string
	base, workspace, agent, conversation, space, task, ratio string
	cipher                                                   []byte
	seconds                                                  int
	started                                                  time.Time
}

func (s *Service) claim(ctx context.Context) (j job, err error) {
	j.token = service.NewID()
	err = s.DB.QueryRowContext(ctx, `WITH candidate AS(SELECT id FROM platform_video_generations WHERE NOT notified AND status<>'pending_review' AND next_check_at<=now() ORDER BY next_check_at,id LIMIT 1 FOR UPDATE SKIP LOCKED),
 claimed AS(UPDATE platform_video_generations g SET check_token=$1,next_check_at=now()+interval '2 minutes' FROM candidate WHERE candidate.id=g.id RETURNING g.*)
 SELECT g.id,g.status,coalesce(g.upstream_id,''),c.base_url,c.credential_ciphertext,g.workspace_id,g.agent_id,coalesce(s.conversation_id::text,''),coalesce(s.task_space_id::text,''),g.task_id,g.aspect_ratio,g.seconds,coalesce(g.reviewed_at,g.started_at)
 FROM claimed g JOIN platform_video_generation_configs c ON(c.plugin_id,c.version)=(g.plugin_id,g.config_version) JOIN manager_sessions s ON s.id=g.session_id`, j.token).Scan(&j.id, &j.status, &j.upstream, &j.base, &j.cipher, &j.workspace, &j.agent, &j.conversation, &j.space, &j.task, &j.ratio, &j.seconds, &j.started)
	return
}
func (s *Service) update(ctx context.Context, j job, status, billing, code string, delay int) error {
	_, e := s.DB.ExecContext(ctx, `UPDATE platform_video_generations SET status=$3,billing_status=$4,error_code=nullif($5,''),next_check_at=now()+make_interval(secs=>$6),check_token=NULL WHERE id=$1 AND check_token=$2`, j.id, j.token, status, billing, code, delay)
	return e
}
func (s *Service) advance(ctx context.Context, j job) error {
	switch j.status {
	case "submitting":
		return s.update(ctx, j, "pending_review", "pending", "submit_unknown", 0)
	case "failed", "ready":
		return s.finish(ctx, j)
	}
	b, err := manager.VideoOriginal(s.Root, j.workspace, j.id)
	if err == nil {
		return s.save(ctx, j, b)
	}
	var missingFile *manager.Error
	if !errors.As(err, &missingFile) || missingFile.Status != 404 {
		return err
	}
	key, err := s.Secrets.Open("video-provider", pluginID, j.cipher)
	if err != nil {
		return err
	}
	u, err := s.poll(ctx, j.base, key, j.upstream)
	if err != nil {
		return s.retry(ctx, j, "video_query_failed")
	}
	switch u.Status {
	case "failed":
		return s.update(ctx, j, "failed", "no_charge", "video_provider_failed", 0)
	case "queued", "in_progress":
		if time.Since(j.started) > 24*time.Hour {
			return s.update(ctx, j, "pending_review", "pending", "video_wait_timeout", 0)
		}
		return s.update(ctx, j, u.Status, "pending", "", 8)
	case "completed":
		if j.status != "saving" {
			return s.update(ctx, j, "saving", "pending", "", 0)
		}
		b, err = s.download(ctx, u)
		if errors.Is(err, invalidMedia) {
			return s.update(ctx, j, "failed", "no_charge", "video_too_large", 0)
		}
		if err != nil {
			return s.retry(ctx, j, "video_download_failed")
		}
		return s.save(ctx, j, b)
	default:
		return s.retry(ctx, j, "video_unknown_state")
	}
}
func (s *Service) retry(ctx context.Context, j job, code string) error {
	status := j.status
	if time.Since(j.started) > 24*time.Hour {
		status = "pending_review"
	}
	return s.update(ctx, j, status, "pending", code, 20)
}
func (s *Service) save(ctx context.Context, j job, b []byte) error {
	if err := inspect(ctx, b, j.ratio, j.seconds); err != nil {
		if errors.Is(err, invalidMedia) {
			return s.update(ctx, j, "failed", "no_charge", "video_invalid", 0)
		}
		return err
	}
	if err := manager.SaveVideoOriginal(s.Root, j.workspace, j.id, b); err != nil {
		return err
	}
	if err := manager.MaterializeVideo(s.Root, j.workspace, j.agent, j.conversation, j.space, j.task, j.id, b); err != nil {
		return err
	}
	digest := sha256.Sum256(b)
	_, err := s.DB.ExecContext(ctx, `UPDATE platform_video_generations SET status='ready',asset_version=$3,next_check_at=now(),check_token=NULL,error_code=NULL WHERE id=$1 AND check_token=$2`, j.id, j.token, hex.EncodeToString(digest[:]))
	return err
}
func (s *Service) finish(ctx context.Context, j job) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		var payer, billing string
		var amount int64
		err := tx.QueryRowContext(ctx, `SELECT payer_account_id,billing_status,unit_price_units*seconds FROM platform_video_generations WHERE id=$1 AND check_token=$2 AND NOT notified FOR UPDATE`, j.id, j.token).Scan(&payer, &billing, &amount)
		if missing(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if j.status == "ready" && billing == "pending" {
			if err = repo.ChargeVideo(ctx, tx, payer, j.agent, j.id, amount); err != nil {
				return err
			}
			billing = "settled"
		}
		if s.Notify == nil {
			return errors.New("missing video completion handler")
		}
		if err = s.Notify(ctx, tx, j.id, j.status); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE platform_video_generations SET notified=true,billing_status=$2,check_token=NULL WHERE id=$1", j.id, billing)
		return err
	})
}

// Reconcile advances a bounded batch using recoverable leases.
func (s *Service) Reconcile(ctx context.Context) error {
	for range 24 {
		j, err := s.claim(ctx)
		if missing(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if err = s.advance(ctx, j); err != nil {
			return err
		}
	}
	return nil
}
