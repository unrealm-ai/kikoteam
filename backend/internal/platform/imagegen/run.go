package imagegen

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	proxy "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy"
	repo "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/repository"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type Arguments struct {
	Prompt string `json:"prompt"`
	Ratio  string `json:"aspect_ratio,omitempty"`
	Key    string `json:"request_key"`
}

var requestKey = regexp.MustCompile(`^[a-zA-Z0-9_.:-]{1,128}$`)

type Scope struct {
	manager.DelegationIdentity
	Input, Version string
}
type owner struct{ workspace, agent, conversation, space, task, turn string }
type Result struct {
	ID      string  `json:"generation_id"`
	Status  string  `json:"status"`
	Billing string  `json:"billing_status"`
	Price   string  `json:"credits_per_image"`
	Charged *string `json:"charged_credits"`
	Count   *int    `json:"image_count"`
	Path    string  `json:"path,omitempty"`
	MIME    string  `json:"mime_type,omitempty"`
	Width   int     `json:"width,omitempty"`
	Height  int     `json:"height,omitempty"`
	Error   string  `json:"error_code,omitempty"`
}

func scopeOwner(ctx context.Context, db repo.RowReader, scope Scope) (v owner, err error) {
	err = db.QueryRowContext(ctx, `SELECT s.workspace_id,s.agent_id,COALESCE(s.conversation_id::text,''),COALESCE(s.task_space_id::text,''),s.task_id,t.id
 FROM manager_sessions s JOIN manager_turns t ON t.id=s.active_turn_id JOIN manager_inputs i ON i.id=t.input_id JOIN platform_agents a ON a.id=s.agent_id
 LEFT JOIN manager_conversations c ON c.id=s.conversation_id LEFT JOIN manager_task_spaces p ON p.id=s.task_space_id
 WHERE s.id=$1 AND s.engine_id=$2 AND s.assignment_id=$3 AND s.generation=$4 AND t.input_id=$5 AND t.assignment_id=s.assignment_id AND t.generation=s.generation
 AND NOT EXISTS(SELECT FROM manager_human_requests h WHERE h.id=i.human_request_id AND h.state='revised')
 AND t.status='running' AND i.status='started' AND i.admission_epoch=a.admission_epoch AND manager_input_actor_allowed(i.id) AND a.enabled
 AND s.mode='task' AND s.lifecycle='active' AND s.stop_revision=s.applied_stop_revision AND COALESCE(c.lifecycle,p.lifecycle)='active'
 AND EXISTS (SELECT FROM jsonb_array_elements(t.capability_snapshot->'default_plugins') pin WHERE pin->>'plugin_id'=$6 AND pin->>'version_id'=$7
 AND pin->>'handler_key'='image_generation' AND pin->'tools' @> '[{"name":"generate_image"}]')
 FOR SHARE OF s,t,i,a`, scope.SessionID, scope.EngineID, scope.AssignmentID, scope.Generation, scope.Input, pluginID, scope.Version).Scan(&v.workspace, &v.agent, &v.conversation, &v.space, &v.task, &v.turn)
	if errors.Is(err, sql.ErrNoRows) {
		err = service.Fail("capability_forbidden", 403, "当前任务无出图权限")
	}
	return
}
func (s *Service) admit(ctx context.Context, scope Scope, args Arguments) (o owner, c Settings, id string, fresh bool, err error) {
	encoded, e := json.Marshal(args)
	if e != nil {
		err = e
		return
	}
	fingerprint := sha256.Sum256(encoded)
	err = s.Transaction(ctx, func(tx *sql.Tx) error {
		var e error
		o, e = scopeOwner(ctx, tx, scope)
		if e != nil {
			return e
		}
		// One admission lock for all replicas. No supplier I/O while this lock is held.
		c, e = config(ctx, tx, true)
		if e != nil {
			return e
		}
		var matches bool
		e = tx.QueryRowContext(ctx, `SELECT id,request_fingerprint=$4 FROM platform_image_generations WHERE workspace_id=$1 AND task_id=$2 AND request_key=$3`, o.workspace, o.task, args.Key, fingerprint[:]).Scan(&id, &matches)
		if e == nil {
			if !matches {
				return service.Fail("idempotency_conflict", 409, "相同 request_key 的参数已变化")
			}
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if !c.Enabled || !c.listed {
			return service.Fail("plugin_disabled", 403, "平台已停用文生图")
		}
		var payer string
		var balance int64
		e = tx.QueryRowContext(ctx, `SELECT k.payer_account_id,w.balance_units FROM platform_agent_model_keys k JOIN platform_credit_wallets w ON w.account_id=k.payer_account_id
  JOIN platform_members m ON m.workspace_id=k.workspace_id AND m.user_id=k.payer_account_id AND m.role='owner' AND m.status='active'
  WHERE k.workspace_id=$1 AND k.agent_id=$2 FOR SHARE OF k,m`, o.workspace, o.agent).Scan(&payer, &balance)
		if errors.Is(e, sql.ErrNoRows) {
			return service.Fail("payer_binding_required", 422, "员工需要有效的积分付款账户")
		}
		if e != nil {
			return e
		}
		if balance <= 0 {
			return service.Fail("insufficient_credits", 402, "积分不足")
		}
		var running, daily int
		if e = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM platform_image_generations WHERE generation_status IN ('admitted','dispatched')),
  (SELECT count(*) FROM platform_image_generations WHERE workspace_id=$1 AND quota_date=(now() AT TIME ZONE 'UTC')::date AND generation_status<>'failed')`, o.workspace).Scan(&running, &daily); e != nil {
			return e
		}
		if running >= c.Concurrent {
			return service.Fail("concurrency_limit_reached", 429, "平台出图并发已满")
		}
		if daily >= c.Limit {
			return service.Fail("daily_limit_reached", 429, "工作区今日出图额度已用完")
		}
		price, e := proxy.ParseAmount(c.Price)
		if e != nil {
			return e
		}
		id = service.NewID()
		_, e = tx.ExecContext(ctx, `INSERT INTO platform_image_generations(id,workspace_id,agent_id,payer_account_id,task_id,session_id,input_id,turn_id,request_key,request_fingerprint,plugin_id,plugin_version_id,config_version,unit_price_units,aspect_ratio,generation_status,billing_status,deadline_at)
  VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,'admitted','pending',now()+make_interval(secs=>$16))`, id, o.workspace, o.agent, payer, o.task, scope.SessionID, scope.Input, o.turn, args.Key, fingerprint[:], pluginID, scope.Version, c.ConfigVersion, price, args.Ratio, c.Timeout)
		fresh = e == nil
		return e
	})
	return
}
func (s *Service) Run(ctx context.Context, scope Scope, args Arguments) (Result, error) {
	args.Prompt = strings.TrimSpace(args.Prompt)
	if args.Ratio == "" {
		args.Ratio = "square"
	}
	if !requestKey.MatchString(args.Key) || imageSizes[args.Ratio] == "" || args.Prompt == "" || utf8.RuneCountInString(args.Prompt) > 16000 {
		return Result{}, service.Fail("invalid_arguments", 400, "提示词、画幅或请求键无效")
	}
	o, c, id, fresh, err := s.admit(ctx, scope, args)
	if err != nil {
		return Result{}, err
	}
	if fresh {
		// The committed request belongs to its payer even when the MCP connection closes.
		active, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Duration(c.Timeout+35)*time.Second)
		start := time.Now()
		err = s.execute(active, c, o, id, args)
		cancel()
		if s.Logger != nil {
			code := ""
			if err != nil {
				code = "generation_incomplete"
				var p *service.Error
				if errors.As(err, &p) {
					code = p.Code
				}
			}
			s.Logger.Info("Image generation finished", "generation_id", id, "config_version", c.ConfigVersion, "duration_ms", time.Since(start).Milliseconds(), "error_code", code)
		}
		if err != nil {
			return Result{}, err
		}
	}
	if _, err = scopeOwner(ctx, s.DB, scope); err != nil {
		return Result{}, err
	}
	result, err := s.result(ctx, id)
	if err == nil && result.Status == "ready" {
		var data []byte
		data, err = manager.ImageOriginal(s.Root, o.workspace, id)
		if err == nil {
			err = manager.MaterializeImage(s.Root, o.workspace, o.agent, o.conversation, o.space, o.task, id, data)
		}
	}
	return result, err
}
func (s *Service) execute(ctx context.Context, c Settings, o owner, id string, args Arguments) error {
	key, err := s.Secrets.Open("image-provider", pluginID, c.cipher)
	if err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE platform_image_generations SET generation_status='dispatched',dispatched_at=now() WHERE id=$1 AND generation_status='admitted'`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil || n != 1 {
		return err
	}
	data, upstream, providerErr := s.generate(ctx, c, key, args.Prompt, args.Ratio, id)
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if providerErr != nil {
		var p *service.Error
		if errors.As(providerErr, &p) && p.Code == "image_provider_rejected" {
			_, err = s.DB.ExecContext(finish, `UPDATE platform_image_generations SET generation_status='failed',billing_status='no_charge',image_count=0,error_code=$2,upstream_request_id=nullif($3,''),finished_at=now() WHERE id=$1 AND generation_status='dispatched'`, id, p.Code, upstream)
		} else {
			_, err = s.DB.ExecContext(finish, `UPDATE platform_image_generations SET generation_status='pending_review',error_code='generation_pending_review',upstream_request_id=nullif($2,'') WHERE id=$1 AND generation_status='dispatched'`, id, upstream)
		}
		return errors.Join(providerErr, err)
	}
	if err = manager.SaveImageOriginal(s.Root, o.workspace, id, data); err != nil {
		return err
	}
	if err = s.recordOriginal(finish, id, c.sizes[args.Ratio], data, upstream); err != nil {
		return err
	}
	// If settlement is delayed, expose the durable ready/pending result. The hub retries the ledger transaction.
	if err = s.settle(finish, id); err != nil {
		if s.Logger != nil {
			s.Logger.Warn("Image settlement pending", "generation_id", id, "config_version", c.ConfigVersion)
		}
		return finish.Err()
	}
	return nil
}
func (s *Service) result(ctx context.Context, id string) (r Result, err error) {
	var price int64
	err = s.DB.QueryRowContext(ctx, `SELECT id,generation_status,billing_status,unit_price_units,image_count,coalesce(output_path,''),coalesce(mime_type,''),coalesce(width,0),coalesce(height,0),coalesce(error_code,'') FROM platform_image_generations WHERE id=$1`, id).Scan(&r.ID, &r.Status, &r.Billing, &price, &r.Count, &r.Path, &r.MIME, &r.Width, &r.Height, &r.Error)
	r.Price = proxy.Amount(price)
	switch r.Billing {
	case "settled":
		r.Charged = &r.Price
	case "no_charge":
		zero := proxy.Amount(0)
		r.Charged = &zero
	}
	return
}
func (s *Service) recordOriginal(ctx context.Context, id, expectedSize string, data []byte, upstream string) error {
	m, err := validatePNG(data, expectedSize)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE platform_image_generations SET generation_status='ready',image_count=1,asset_version=$2,
 output_path='outputs/images/'||id||'.png',mime_type='image/png',size_bytes=$3,width=$4,height=$5,finished_at=now(),
 upstream_request_id=coalesce(nullif($6,''),upstream_request_id),error_code=NULL WHERE id=$1 AND generation_status IN ('dispatched','pending_review') AND billing_status='pending'`, id, m.Digest, m.Bytes, m.Width, m.Height, upstream)
	return err
}
func (s *Service) settle(ctx context.Context, id string) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		var payer, agent, state, billing string
		var price int64
		e := tx.QueryRowContext(ctx, `SELECT payer_account_id,agent_id,generation_status,billing_status,unit_price_units FROM platform_image_generations WHERE id=$1 FOR UPDATE`, id).Scan(&payer, &agent, &state, &billing, &price)
		if e != nil || state != "ready" || billing != "pending" {
			return e
		}
		if e = repo.ChargeImage(ctx, tx, payer, agent, id, price); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "UPDATE platform_image_generations SET billing_status='settled',settled_at=now() WHERE id=$1", id)
		return e
	})
}
