package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"time"

	proxy "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy"
	platform "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func (s Store) Admit(ctx context.Context, key, id, protocol string, deadline time.Time) (call proxy.Call, e error) {
	digest := sha256.Sum256([]byte(key))
	e = s.Transaction(ctx, func(tx *sql.Tx) error {
		var w, agent, payer string
		e := tx.QueryRowContext(ctx, `SELECT workspace_id,agent_id,payer_account_id FROM platform_agent_model_keys WHERE key_hash=$1`, digest[:]).Scan(&w, &agent, &payer)
		if errors.Is(e, sql.ErrNoRows) {
			return platform.Fail("invalid_api_key", 401, "代理凭据无效")
		}
		if e != nil {
			return e
		}
		if e = admissionIdentity(ctx, tx, w, agent, payer, digest[:]); e != nil {
			return e
		}
		call.Model, e = scanModel(tx.QueryRowContext(ctx, `SELECT `+columns+` FROM platform_proxy_models WHERE id=$1 AND protocol=$2 AND enabled AND validated FOR SHARE`, id, protocol))
		if errors.Is(e, sql.ErrNoRows) {
			return platform.Fail("model_unavailable", 422, "官方模型不可用")
		}
		if e != nil {
			return e
		}
		var balance int64
		if e = tx.QueryRowContext(ctx, `SELECT balance_units FROM platform_credit_wallets WHERE account_id=$1`, payer).Scan(&balance); e != nil {
			return e
		}
		if balance <= 0 {
			return platform.Fail("insufficient_credits", 402, "积分不足")
		}
		rates, e := call.Model.Snapshot()
		if e != nil {
			return e
		}
		snapshot, e := json.Marshal(rates)
		if e != nil {
			return e
		}
		call.ID = platform.NewID()
		_, e = tx.ExecContext(ctx, `INSERT INTO platform_model_usage(id,workspace_id,agent_id,payer_account_id,model_id,price_snapshot,status,deadline_at)
   VALUES($1,$2,$3,$4,$5,$6,'running',$7)`, call.ID, w, agent, payer, id, snapshot, deadline)
		return e
	})
	return call, e
}

func admissionIdentity(ctx context.Context, tx *sql.Tx, w, agent, payer string, digest []byte) error {
	// Workspace changes serialize membership and employee updates.
	var workspace string
	if e := tx.QueryRowContext(ctx, `SELECT id FROM platform_workspaces WHERE id=$1 FOR SHARE`, w).Scan(&workspace); e != nil {
		return e
	}
	var enabled bool
	e := tx.QueryRowContext(ctx, `SELECT a.enabled FROM platform_agents a
 JOIN platform_agent_model_keys k ON k.agent_id=a.id AND k.payer_account_id=$4
 JOIN platform_members p ON p.workspace_id=a.workspace_id AND p.user_id=k.payer_account_id AND p.role='owner' AND p.status='active'
 WHERE a.workspace_id=$1 AND a.id=$2 AND k.key_hash=$3 FOR SHARE OF a,k,p`, w, agent, digest, payer).Scan(&enabled)
	if errors.Is(e, sql.ErrNoRows) || e == nil && !enabled {
		return platform.Fail("employee_unavailable", 403, "AI 员工或扣费账户不可用")
	}
	return e
}

func (s Store) Record(ctx context.Context, id, status, errorCode, upstreamID string, u *proxy.Usage) error {
	var input, output, read, write any
	if u != nil {
		input, output, read, write = u.Input, u.Output, u.CacheRead, u.CacheWrite
		status = "usage_recorded"
	}
	_, e := s.DB.ExecContext(ctx, `UPDATE platform_model_usage SET status=$2,error_code=NULLIF($3,''),upstream_request_id=NULLIF($4,''),
 input_tokens=$5,output_tokens=$6,cache_read_tokens=$7,cache_write_tokens=$8,finished_at=now()
 WHERE id=$1 AND (status='running' OR (status='pending_review' AND resolved_by IS NULL AND $2='usage_recorded'))`,
		id, status, errorCode, upstreamID, input, output, read, write)
	return e
}

func settle(ctx context.Context, tx *sql.Tx, id string) error {
	var payer, agent, status string
	var price []byte
	var u proxy.Usage
	e := tx.QueryRowContext(ctx, `SELECT payer_account_id,agent_id,status,price_snapshot,COALESCE(input_tokens,0),COALESCE(output_tokens,0),COALESCE(cache_read_tokens,0),COALESCE(cache_write_tokens,0)
 FROM platform_model_usage WHERE id=$1 FOR UPDATE`, id).Scan(&payer, &agent, &status, &price, &u.Input, &u.Output, &u.CacheRead, &u.CacheWrite)
	if e != nil || status != "usage_recorded" {
		return e
	}
	var rates proxy.Rates
	if e = json.Unmarshal(price, &rates); e != nil {
		return e
	}
	cost, e := u.Cost(rates)
	if e != nil {
		return e
	}
	var balance int64
	if e = tx.QueryRowContext(ctx, `SELECT balance_units FROM platform_credit_wallets WHERE account_id=$1 FOR UPDATE`, payer).Scan(&balance); e != nil {
		return e
	}
	if balance < math.MinInt64+cost {
		return proxy.ErrChargeRange
	}
	if _, e = entry(ctx, tx, payer, -cost, "model_charge", "usage:"+id, "", "", "", agent, id); e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `UPDATE platform_model_usage SET status='settled',settled_at=now() WHERE id=$1`, id)
	return e
}

func (s Store) Settle(ctx context.Context, id string) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		e := settle(ctx, tx, id)
		if errors.Is(e, proxy.ErrChargeRange) {
			_, e = tx.ExecContext(ctx, `UPDATE platform_model_usage SET status='pending_review',error_code='credit_amount_out_of_range' WHERE id=$1`, id)
		}
		return e
	})
}

func (s Store) Resolve(ctx context.Context, actor, id, reason string, u *proxy.Usage) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		if e := Admin(ctx, tx, actor, true); e != nil {
			return e
		}
		var status string
		if e := tx.QueryRowContext(ctx, `SELECT status FROM platform_model_usage WHERE id=$1 FOR UPDATE`, id).Scan(&status); e != nil {
			return Missing(e)
		}
		if status != "pending_review" {
			return platform.Fail("usage_state_conflict", 409, "该调用已处理或无需核对")
		}
		if u == nil {
			_, e := tx.ExecContext(ctx, `UPDATE platform_model_usage SET status='no_charge',resolved_by=$2,resolution_reason=$3 WHERE id=$1`, id, actor, reason)
			return e
		}
		_, e := tx.ExecContext(ctx, `UPDATE platform_model_usage SET status='usage_recorded',resolved_by=$2,resolution_reason=$3,
   input_tokens=$4,output_tokens=$5,cache_read_tokens=$6,cache_write_tokens=$7 WHERE id=$1`, id, actor, reason, u.Input, u.Output, u.CacheRead, u.CacheWrite)
		if e != nil {
			return e
		}
		e = settle(ctx, tx, id)
		if errors.Is(e, proxy.ErrChargeRange) {
			return platform.Fail("credit_amount_out_of_range", 409, "结算金额超出积分范围")
		}
		return e
	})
}

func (s Store) Reconcile(ctx context.Context) error {
	_, e := s.DB.ExecContext(ctx, `UPDATE platform_model_usage SET status='pending_review',error_code='usage_unavailable',finished_at=now()
 WHERE id IN (SELECT id FROM platform_model_usage WHERE status='running' AND deadline_at<now()-interval '30 seconds' ORDER BY deadline_at,id LIMIT 100 FOR UPDATE SKIP LOCKED)`)
	if e != nil {
		return e
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT id FROM platform_model_usage WHERE status='usage_recorded' ORDER BY deadline_at,id LIMIT 100`)
	if e != nil {
		return e
	}
	defer rows.Close() //nolint:errcheck // Iteration checks rows.Err.
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return e
		}
		ids = append(ids, id)
	}
	if e = rows.Err(); e != nil {
		return e
	}
	for _, id := range ids {
		e = errors.Join(e, s.Settle(ctx, id))
	}
	return e
}
