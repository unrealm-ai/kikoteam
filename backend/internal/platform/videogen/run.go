package videogen

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	proxy "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy"
	repo "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/repository"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type Arguments struct {
	Prompt  string `json:"prompt"`
	Ratio   string `json:"aspect_ratio,omitempty"`
	Seconds int    `json:"seconds,omitempty"`
	Key     string `json:"request_key"`
}
type Scope = manager.GenerationScope
type owner struct {
	workspace, agent, session, task, conversation, space string
	stop                                                 int64
}
type Result struct {
	ID      string `json:"generation_id"`
	Status  string `json:"status"`
	Seconds int    `json:"seconds"`
	Billing string `json:"billing_status"`
	Price   string `json:"credits_per_second"`
	Path    string `json:"path,omitempty"`
	Error   string `json:"error_code,omitempty"`
}

func authority(ctx context.Context, db repo.RowReader, scope Scope) (o owner, err error) {
	err = db.QueryRowContext(ctx, `SELECT s.workspace_id,s.agent_id,s.id,s.task_id,s.stop_revision,coalesce(s.conversation_id::text,''),coalesce(s.task_space_id::text,'') FROM manager_sessions s JOIN manager_turns t ON t.id=s.active_turn_id JOIN manager_inputs i ON i.id=t.input_id JOIN platform_agents a ON a.id=s.agent_id
 LEFT JOIN manager_conversations c ON c.id=s.conversation_id LEFT JOIN manager_task_spaces p ON p.id=s.task_space_id
 WHERE s.id=$1 AND s.engine_id=$2 AND s.assignment_id=$3 AND s.generation=$4 AND t.input_id=$5 AND t.assignment_id=s.assignment_id AND t.generation=s.generation
 AND NOT EXISTS (SELECT FROM manager_human_requests h WHERE h.id=i.human_request_id AND h.state='revised')
 AND t.status='running' AND i.status='started' AND i.admission_epoch=a.admission_epoch AND manager_input_actor_allowed(i.id) AND a.enabled
 AND s.mode='task' AND s.lifecycle='active' AND s.stop_revision=s.applied_stop_revision AND COALESCE(c.lifecycle,p.lifecycle)='active'
 AND EXISTS(SELECT FROM jsonb_array_elements(t.capability_snapshot->'default_plugins') pin WHERE pin->>'plugin_id'=$6 AND pin->>'version_id'=$7 AND pin->>'handler_key'='video_generation' AND pin->'tools' @> '[{"name":"generate_video"}]') FOR SHARE OF s,t,i,a`, scope.SessionID, scope.EngineID, scope.AssignmentID, scope.Generation, scope.Input, pluginID, scope.Version).Scan(&o.workspace, &o.agent, &o.session, &o.task, &o.stop, &o.conversation, &o.space)
	if missing(err) {
		err = service.Fail("capability_forbidden", 403, "当前任务无视频生成权限")
	}
	return
}
func (s *Service) admit(ctx context.Context, scope Scope, a Arguments) (id string, c Settings, fresh bool, err error) {
	raw, e := json.Marshal(a)
	if e != nil {
		err = e
		return
	}
	fingerprint := sha256.Sum256(raw)
	err = s.Transaction(ctx, func(tx *sql.Tx) error {
		o, e := authority(ctx, tx, scope)
		if e != nil {
			return e
		}
		c, e = config(ctx, tx, true)
		if e != nil {
			return e
		}
		var same bool
		e = tx.QueryRowContext(ctx, "SELECT id,request_fingerprint=$4 FROM platform_video_generations WHERE workspace_id=$1 AND task_id=$2 AND request_key=$3", o.workspace, o.task, a.Key, fingerprint[:]).Scan(&id, &same)
		if e == nil {
			if !same {
				return service.Fail("idempotency_conflict", 409, "同一请求键的参数已变化")
			}
			return nil
		}
		if !missing(e) {
			return e
		}
		if !c.Enabled || !c.Configured {
			return service.Fail("plugin_disabled", 403, "平台未启用文生视频")
		}
		var payer string
		var balance int64
		e = tx.QueryRowContext(ctx, `SELECT k.payer_account_id,w.balance_units FROM platform_agent_model_keys k JOIN platform_credit_wallets w ON w.account_id=k.payer_account_id JOIN platform_members m ON m.workspace_id=k.workspace_id AND m.user_id=k.payer_account_id AND m.role='owner' AND m.status='active' WHERE k.workspace_id=$1 AND k.agent_id=$2 FOR SHARE OF k,m,w`, o.workspace, o.agent).Scan(&payer, &balance)
		if missing(e) {
			return service.Fail("payer_binding_required", 422, "员工需要有效的积分付款账户")
		}
		if e != nil {
			return e
		}
		if balance <= 0 {
			return service.Fail("insufficient_credits", 402, "积分不足")
		}
		var running, day int64
		e = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM platform_video_generations WHERE status IN ('submitting','queued','in_progress','saving')),coalesce(sum(seconds),0) FROM platform_video_generations WHERE workspace_id=$1 AND quota_date=(now() AT TIME ZONE 'UTC')::date AND status<>'failed'`, o.workspace).Scan(&running, &day)
		if e != nil {
			return e
		}
		if running >= int64(c.Concurrent) {
			return service.Fail("concurrency_limit_reached", 429, "平台视频并发已满")
		}
		if day+int64(a.Seconds) > int64(c.Daily) {
			return service.Fail("daily_limit_reached", 429, "工作区今日视频秒数已用完")
		}
		price, e := proxy.ParseAmount(c.Price)
		if e != nil {
			return e
		}
		id = service.NewID()
		_, e = tx.ExecContext(ctx, `INSERT INTO platform_video_generations(id,workspace_id,agent_id,payer_account_id,task_id,session_id,input_id,stop_revision,request_key,request_fingerprint,plugin_id,config_version,unit_price_units,seconds,aspect_ratio,status,next_check_at)VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,'submitting',now()+interval '2 minutes')`, id, o.workspace, o.agent, payer, o.task, o.session, scope.Input, o.stop, a.Key, fingerprint[:], pluginID, c.config, price, a.Seconds, a.Ratio)
		fresh = e == nil
		return e
	})
	return
}
func (s *Service) Run(ctx context.Context, scope Scope, a Arguments) (Result, error) {
	a.Prompt = strings.TrimSpace(a.Prompt)
	if a.Seconds == 0 {
		a.Seconds = 5
	}
	if a.Ratio == "" {
		a.Ratio = "landscape"
	}
	if a.Prompt == "" || utf8.RuneCountInString(a.Prompt) > 7000 || ratios[a.Ratio] == "" || a.Seconds < 1 || a.Seconds > 15 || !ids.MatchString(a.Key) {
		return Result{}, service.Fail("invalid_arguments", 400, "提示词、画幅、秒数或请求键无效")
	}
	id, c, fresh, err := s.admit(ctx, scope, a)
	if err != nil {
		return Result{}, err
	}
	if fresh {
		active, cancel := context.WithTimeout(context.WithoutCancel(ctx), 65*time.Second)
		defer cancel()
		key, e := s.Secrets.Open("video-provider", pluginID, c.cipher)
		if e != nil {
			return Result{}, e
		}
		upstream, e := s.submit(active, c, key, a)
		status, billing, code := "queued", "pending", ""
		if errors.Is(e, rejected) {
			status, billing, code = "failed", "no_charge", "video_provider_rejected"
		} else if e != nil {
			status, code = "pending_review", "submit_unknown"
		}
		_, err = s.DB.ExecContext(active, `UPDATE platform_video_generations SET upstream_id=nullif($2,''),status=$3,billing_status=$4,error_code=nullif($5,''),next_check_at=now() WHERE id=$1 AND status='submitting'`, id, upstream, status, billing, code)
		if err != nil {
			return Result{}, err
		}
	}
	o, err := authority(ctx, s.DB, scope)
	if err != nil {
		return Result{}, err
	}
	r, err := s.result(ctx, id)
	if err != nil || r.Status != "ready" {
		return r, err
	}
	b, err := manager.VideoOriginal(s.Root, o.workspace, id)
	if err != nil {
		return Result{}, err
	}
	if err = manager.MaterializeVideo(s.Root, o.workspace, o.agent, o.conversation, o.space, o.task, id, b); err != nil {
		return Result{}, err
	}
	return r, nil
}
func (s *Service) result(ctx context.Context, id string) (r Result, err error) {
	var price int64
	err = s.DB.QueryRowContext(ctx, `SELECT id,status,seconds,billing_status,unit_price_units,coalesce(error_code,'') FROM platform_video_generations WHERE id=$1`, id).Scan(&r.ID, &r.Status, &r.Seconds, &r.Billing, &price, &r.Error)
	r.Price = proxy.Amount(price)
	if r.Status == "ready" {
		r.Path = videoPath(id)
	}
	return
}
func videoPath(id string) string { return "outputs/videos/" + id + ".mp4" }
