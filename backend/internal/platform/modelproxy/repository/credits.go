package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	proxy "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy"
	platform "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type Entry struct {
	ID      string    `json:"id"`
	Kind    string    `json:"kind"`
	Amount  string    `json:"amount"`
	Balance string    `json:"balance_after"`
	Reason  string    `json:"reason,omitempty"`
	At      time.Time `json:"created_at"`
}

func entry(ctx context.Context, tx *sql.Tx, account string, delta int64, kind, key, fingerprint, operator, reason, agent, call string) (v Entry, e error) {
	var balance, version int64
	e = tx.QueryRowContext(ctx, `UPDATE platform_credit_wallets SET balance_units=balance_units+$2,version=version+1,updated_at=now()
 WHERE account_id=$1 RETURNING balance_units,version`, account, delta).Scan(&balance, &version)
	if e != nil {
		return v, Missing(e)
	}
	v = Entry{ID: platform.NewID(), Kind: kind, Amount: proxy.Amount(delta), Balance: proxy.Amount(balance), Reason: reason}
	e = tx.QueryRowContext(ctx, `INSERT INTO platform_credit_ledger(id,account_id,wallet_version,kind,amount_units,balance_after_units,biz_key,
 request_fingerprint,operator_id,reason,agent_id,usage_id) VALUES($1,$2,$3,$4,$5,$6,$7,nullif($8,''),nullif($9,'')::uuid,nullif($10,''),nullif($11,'')::uuid,nullif($12,'')::uuid)
 RETURNING created_at`, v.ID, account, version, kind, delta, balance, key, fingerprint, operator, reason, agent, call).Scan(&v.At)
	return v, e
}

// EnsureWallet also covers accounts created by an old Backend during rollout.
func EnsureWallet(ctx context.Context, tx *sql.Tx, id string) error {
	_, e := tx.ExecContext(ctx, `INSERT INTO platform_credit_wallets(account_id)
	  SELECT id FROM platform_accounts WHERE id=$1 ON CONFLICT(account_id) DO NOTHING`, id)
	return e
}

func Signup(ctx context.Context, tx *sql.Tx, id string) error {
	if e := EnsureWallet(ctx, tx, id); e != nil {
		return e
	}
	var amount int64
	if e := tx.QueryRowContext(ctx, `SELECT signup_bonus_units FROM platform_credit_settings WHERE id=1`).Scan(&amount); e != nil {
		return e
	}
	_, e := entry(ctx, tx, id, amount, "signup_bonus", "signup:"+id, "", "", "", "", "")
	return e
}

func (s Store) Grant(ctx context.Context, actor, account, key, reason string, amount int64) (v Entry, replay bool, e error) {
	key = fmt.Sprintf("grant:%x", sha256.Sum256([]byte(actor+":"+account+":"+key)))
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(fmt.Appendf(nil, "%d:%s", amount, reason)))
	e = s.Transaction(ctx, func(tx *sql.Tx) error {
		if e := Admin(ctx, tx, actor, true); e != nil {
			return e
		}
		if e := EnsureWallet(ctx, tx, account); e != nil {
			return e
		}
		var current int64
		if e := tx.QueryRowContext(ctx, `SELECT balance_units FROM platform_credit_wallets WHERE account_id=$1 FOR UPDATE`, account).Scan(&current); e != nil {
			return Missing(e)
		}
		var old string
		var delta, balance int64
		err := tx.QueryRowContext(ctx, `SELECT id,kind,amount_units,balance_after_units,reason,created_at,request_fingerprint FROM platform_credit_ledger WHERE biz_key=$1`, key).
			Scan(&v.ID, &v.Kind, &delta, &balance, &v.Reason, &v.At, &old)
		if err == nil {
			if old != fingerprint {
				return platform.Fail("idempotency_conflict", 409, "该幂等键已用于其他赠送内容")
			}
			v.Amount, v.Balance, replay = proxy.Amount(delta), proxy.Amount(balance), true
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if current > math.MaxInt64-amount {
			return platform.Fail("credit_amount_out_of_range", 409, "赠送后余额超出积分范围")
		}
		v, err = entry(ctx, tx, account, amount, "admin_grant", key, fingerprint, actor, reason, "", "")
		return err
	})
	return
}

type Settings struct {
	Bonus   string `json:"signup_bonus"`
	Version int64  `json:"version"`
}

func (s Store) Settings(ctx context.Context, actor string) (v Settings, e error) {
	if e = Admin(ctx, s.DB, actor, false); e != nil {
		return
	}
	var amount int64
	e = s.DB.QueryRowContext(ctx, `SELECT signup_bonus_units,version FROM platform_credit_settings WHERE id=1`).Scan(&amount, &v.Version)
	v.Bonus = proxy.Amount(amount)
	return
}

func (s Store) UpdateSettings(ctx context.Context, actor string, version, amount int64) (v Settings, e error) {
	e = s.Transaction(ctx, func(tx *sql.Tx) error {
		if e := Admin(ctx, tx, actor, true); e != nil {
			return e
		}
		e := tx.QueryRowContext(ctx, `UPDATE platform_credit_settings SET signup_bonus_units=$2,version=version+1,updated_by=$3,updated_at=now()
   WHERE id=1 AND version=$1 RETURNING version`, version, amount, actor).Scan(&v.Version)
		if errors.Is(e, sql.ErrNoRows) {
			return platform.Fail("credit_settings_conflict", 409, "积分设置已更新，请刷新")
		}
		v.Bonus = proxy.Amount(amount)
		return e
	})
	return
}

func authorizeAccount(ctx context.Context, db RowReader, actor, account string) error {
	if actor == account {
		return nil
	}
	return Admin(ctx, db, actor, false)
}

type Wallet struct {
	Account string `json:"account_id"`
	Balance string `json:"balance"`
}

func (s Store) Wallet(ctx context.Context, actor, account string) (v Wallet, e error) {
	if e = authorizeAccount(ctx, s.DB, actor, account); e != nil {
		return
	}
	var balance int64
	e = s.DB.QueryRowContext(ctx, `SELECT coalesce(w.balance_units,0)
	 FROM platform_accounts a LEFT JOIN platform_credit_wallets w ON w.account_id=a.id WHERE a.id=$1`, account).Scan(&balance)
	return Wallet{Account: account, Balance: proxy.Amount(balance)}, Missing(e)
}

type Filter struct {
	Account, Agent, Model, Status, Kind string
	From, To                            *time.Time
}

type position struct {
	At    time.Time
	ID    string
	Scope string
}
type CreditPage struct {
	Items   []json.RawMessage `json:"items"`
	Next    string            `json:"next_cursor"`
	Summary map[string]string `json:"summary,omitempty"`
}

func creditCursor(cursor string, f Filter, ledger bool) (position, error) {
	scope := fmt.Sprintf("%x", sha256.Sum256(fmt.Appendf(nil, "%t:%s:%s:%s:%s:%s:%v:%v", ledger, f.Account, f.Agent, f.Model, f.Status, f.Kind, f.From, f.To)))
	if cursor == "" {
		return position{Scope: scope}, nil
	}
	var p position
	raw, e := base64.RawURLEncoding.DecodeString(cursor)
	if e != nil || json.Unmarshal(raw, &p) != nil || p.Scope != scope || p.At.IsZero() || !UUID(p.ID) {
		return p, platform.Fail("invalid_cursor", 400, "账单游标无效")
	}
	return p, nil
}

const callJSON = `jsonb_build_object('id',u.id,'account_id',u.payer_account_id,'agent_id',u.agent_id,'agent_name',a.name,'model_id',u.model_id,
 'price_snapshot',u.price_snapshot,'input_tokens',u.input_tokens,'output_tokens',u.output_tokens,'cache_read_tokens',u.cache_read_tokens,
 'cache_write_tokens',u.cache_write_tokens,'status',u.status,'started_at',u.started_at,'error_code',u.error_code,'finished_at',u.finished_at,
 'settled_at',u.settled_at,'resolution_reason',u.resolution_reason,'upstream_request_id',u.upstream_request_id,
 'charge',(-l.amount_units::numeric/1000000)::numeric(25,6)::text)`

func (s Store) ListCredits(ctx context.Context, actor string, f Filter, cursor string, limit int, ledger bool) (out CreditPage, e error) {
	out.Items = []json.RawMessage{}
	if f.Account == "" {
		// Empty account is permitted only for the platform-wide admin usage view.
		if e = Admin(ctx, s.DB, actor, false); e != nil {
			return out, e
		}
	} else if e = authorizeAccount(ctx, s.DB, actor, f.Account); e != nil {
		return out, e
	}
	p, e := creditCursor(cursor, f, ledger)
	if e != nil {
		return out, e
	}
	from, at, id, selectJSON, where := creditQuery(ledger)
	args := []any{f.Account, f.Agent, f.Model, f.From, f.To, f.Status, f.Kind}
	if ledger {
		var spent, granted string
		e = s.DB.QueryRowContext(ctx, `SELECT (COALESCE(sum(-l.amount_units::numeric) FILTER(WHERE l.kind='model_charge'),0)/1000000)::numeric(25,6)::text,
   (COALESCE(sum(l.amount_units::numeric) FILTER(WHERE l.kind<>'model_charge'),0)/1000000)::numeric(25,6)::text `+from+where, args...).Scan(&spent, &granted)
		if e != nil {
			return out, e
		}
		out.Summary = map[string]string{"spent": spent, "granted": granted}
	}
	var before any
	if !p.At.IsZero() {
		before = p.At
	}
	args = append(args, before, p.ID, limit+1)
	rows, e := s.DB.QueryContext(ctx, `SELECT `+at+`,`+id+`,`+selectJSON+from+where+
		` AND ($8::timestamptz IS NULL OR (`+at+`,`+id+`)<($8,NULLIF($9,'')::uuid)) ORDER BY `+at+` DESC,`+id+` DESC LIMIT $10`, args...)
	if e != nil {
		return out, e
	}
	defer rows.Close() //nolint:errcheck // Iteration checks rows.Err.
	for rows.Next() {
		var item json.RawMessage
		var current position
		if e = rows.Scan(&current.At, &current.ID, &item); e != nil {
			return out, e
		}
		if len(out.Items) == limit {
			raw, err := json.Marshal(p)
			if err != nil {
				return out, err
			}
			out.Next = base64.RawURLEncoding.EncodeToString(raw)
			break
		}
		out.Items = append(out.Items, item)
		p.At, p.ID = current.At, current.ID
	}
	return out, rows.Err()
}

func creditQuery(ledger bool) (from, at, id, value, where string) {
	if ledger {
		from = ` FROM platform_credit_ledger l LEFT JOIN platform_model_usage u ON u.id=l.usage_id LEFT JOIN platform_agents a ON a.id=l.agent_id`
		at, id = "l.created_at", "l.id"
		value = `jsonb_build_object('id',l.id,'kind',l.kind,'amount',(l.amount_units::numeric/1000000)::numeric(25,6)::text,
   'balance_after',(l.balance_after_units::numeric/1000000)::numeric(25,6)::text,'created_at',l.created_at,'reason',l.reason,
   'usage',CASE WHEN u.id IS NOT NULL THEN ` + callJSON + ` END)`
		where = ` WHERE l.account_id=NULLIF($1,'')::uuid AND ($2='' OR l.agent_id=NULLIF($2,'')::uuid)
    AND ($3='' OR u.model_id=NULLIF($3,'')::uuid) AND ($4::timestamptz IS NULL OR l.created_at >= $4)
    AND ($5::timestamptz IS NULL OR l.created_at < $5) AND ($6='' OR u.status=$6) AND ($7='' OR l.kind=$7)`
		return
	}
	from = ` FROM platform_model_usage u JOIN platform_agents a ON a.id=u.agent_id LEFT JOIN platform_credit_ledger l ON l.usage_id=u.id`
	at, id, value = "u.started_at", "u.id", callJSON
	where = ` WHERE ($1='' OR u.payer_account_id=NULLIF($1,'')::uuid) AND ($2='' OR u.agent_id=NULLIF($2,'')::uuid)
   AND ($3='' OR u.model_id=NULLIF($3,'')::uuid) AND ($4::timestamptz IS NULL OR u.started_at >= $4)
   AND ($5::timestamptz IS NULL OR u.started_at < $5) AND ($6='' OR u.status=$6) AND $7=''`
	return
}
