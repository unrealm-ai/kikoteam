package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	proxy "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy"
	platform "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

const columns = `id,name,protocol,base_url,upstream_model_id,input_price_units,output_price_units,
 cache_read_price_units,cache_write_price_units,enabled,is_default,validated,version,tested_at,credential_ciphertext`

type scanner interface{ Scan(...any) error }

func scanModel(row scanner) (m proxy.Model, e error) {
	var in, out int64
	var read, write *int64
	e = row.Scan(&m.ID, &m.Name, &m.Protocol, &m.BaseURL, &m.UpstreamID, &in, &out, &read, &write, &m.Enabled, &m.Default, &m.Validated, &m.Version, &m.TestedAt, &m.Credential)
	m.HasKey = len(m.Credential) > 0
	m.Prices.Input, m.Prices.Output = proxy.Amount(in), proxy.Amount(out)
	if read != nil {
		p := proxy.Amount(*read)
		m.Prices.CacheRead = &p
	}
	if write != nil {
		p := proxy.Amount(*write)
		m.Prices.CacheWrite = &p
	}
	return m, e
}

func Model(ctx context.Context, db RowReader, id string, lock bool) (proxy.Model, error) {
	q := `SELECT ` + columns + ` FROM platform_proxy_models WHERE id=$1`
	if lock {
		q += ` FOR UPDATE`
	}
	m, e := scanModel(db.QueryRowContext(ctx, q, id))
	return m, Missing(e)
}

type ModelPage struct {
	Items    []proxy.Model `json:"items"`
	Total    int           `json:"total"`
	Page     int           `json:"page"`
	PageSize int           `json:"page_size"`
}

func (s Store) Models(ctx context.Context, actor string, p, n int) (out ModelPage, e error) {
	out = ModelPage{Items: []proxy.Model{}, Page: p, PageSize: n}
	if e = Admin(ctx, s.DB, actor, false); e != nil {
		return out, e
	}
	if e = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM platform_proxy_models`).Scan(&out.Total); e != nil {
		return out, e
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT `+columns+` FROM platform_proxy_models ORDER BY created_at DESC,id DESC LIMIT $1 OFFSET $2`, n, (p-1)*n)
	if e != nil {
		return out, e
	}
	defer rows.Close() //nolint:errcheck // Iteration checks rows.Err.
	for rows.Next() {
		m, e := scanModel(rows)
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, m)
	}
	return out, rows.Err()
}

func (s Store) SaveModel(ctx context.Context, actor string, m proxy.Model, create, targetChanged bool) (proxy.Model, error) {
	prices, e := m.Prices.Values()
	if e != nil {
		return m, e
	}
	e = s.Transaction(ctx, func(tx *sql.Tx) error {
		if e := Admin(ctx, tx, actor, true); e != nil {
			return e
		}
		// This singleton also serializes default changes when no default exists.
		if _, e := tx.ExecContext(ctx, `SELECT id FROM platform_credit_settings WHERE id=1 FOR UPDATE`); e != nil {
			return e
		}
		if !create {
			old, e := Model(ctx, tx, m.ID, true)
			if e != nil {
				return e
			}
			if old.Version != m.Version {
				return platform.Fail("model_version_conflict", 409, "模型已更新，请刷新")
			}
			if targetChanged {
				var referenced bool
				e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM platform_agent_versions WHERE platform_model_id=$1)
     OR EXISTS(SELECT 1 FROM platform_model_usage WHERE model_id=$1)
     OR EXISTS(SELECT 1 FROM manager_sessions WHERE configuration->>'platform_model_id'=$1::text)`, m.ID).Scan(&referenced)
				if e != nil {
					return e
				}
				if referenced {
					return platform.Fail("model_target_immutable", 409, "已引用模型的协议和型号不能更改")
				}
			}
		}
		if m.Default {
			if _, e := tx.ExecContext(ctx, `UPDATE platform_proxy_models SET is_default=false,version=version+1,updated_at=now(),updated_by=$2 WHERE is_default AND id<>$1`, m.ID, actor); e != nil {
				return e
			}
		}
		args := []any{m.ID, m.Name, m.Protocol, m.BaseURL, m.UpstreamID, m.Credential, prices[0], prices[1], prices[2], prices[3], m.Enabled, m.Default, m.Validated, m.TestedAt, actor}
		q := `INSERT INTO platform_proxy_models(id,name,protocol,base_url,upstream_model_id,credential_ciphertext,input_price_units,output_price_units,cache_read_price_units,cache_write_price_units,enabled,is_default,validated,tested_at,updated_by)
   VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`
		if !create {
			q = `UPDATE platform_proxy_models SET name=$2,protocol=$3,base_url=$4,upstream_model_id=$5,credential_ciphertext=$6,input_price_units=$7,output_price_units=$8,
    cache_read_price_units=$9,cache_write_price_units=$10,enabled=$11,is_default=$12,validated=$13,tested_at=$14,updated_by=$15,version=version+1,updated_at=now() WHERE id=$1`
		}
		var e error
		m, e = scanModel(tx.QueryRowContext(ctx, q+` RETURNING `+columns, args...))
		return e
	})
	var pg *pgconn.PgError
	if errors.As(e, &pg) && pg.Code == "23505" {
		e = platform.Fail("model_version_conflict", 409, "模型配置冲突，请刷新")
	}
	return m, e
}
