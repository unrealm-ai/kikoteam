package repository

import (
	"context"
	"encoding/json"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

const candidates = `
 SELECT id,created_at,is_default AND enabled AND validated AS preferred,0 AS rank,
 jsonb_build_object('id',id,'name',name,'description',description,'protocol',protocol,'base_url',base_url,'model_id',model_id,
 'version',version,'validated',validated,'has_key',has_key,'vision_status',vision_status,'enabled',enabled,'tested_at',tested_at,'source','workspace') AS item
 FROM platform_model_profiles WHERE workspace_id=$1
 UNION ALL
 SELECT id,created_at,is_default,1,
 jsonb_build_object('id',id,'name',name,'description','','protocol',protocol,'base_url','','model_id',id,'version',version,
 'validated',validated,'has_key',false,'vision_status','unknown','enabled',enabled,'tested_at',tested_at,'source','platform',
 'prices',jsonb_build_object('input',(input_price_units::numeric/1000000)::numeric(25,6)::text,
 'output',(output_price_units::numeric/1000000)::numeric(25,6)::text,
 'cache_read',(cache_read_price_units::numeric/1000000)::numeric(25,6)::text,
 'cache_write',(cache_write_price_units::numeric/1000000)::numeric(25,6)::text))
 FROM platform_proxy_models WHERE enabled AND validated`

func (t *modelTx) Selection(ctx context.Context, p, n int) ([]service.ModelProfile, int, error) {
	items := []service.ModelProfile{}
	// One statement gives counts, defaults and pages the same database snapshot.
	var raw []byte
	var total int
	e := t.tx.QueryRowContext(ctx, `WITH candidates AS (`+candidates+`), chosen AS (
 SELECT id,rank FROM candidates WHERE preferred ORDER BY rank,id LIMIT 1
 ), page AS (
 SELECT c.*,c.item || jsonb_build_object('is_default',d.id IS NOT NULL) AS value FROM candidates c
 LEFT JOIN chosen d ON d.id=c.id AND d.rank=c.rank
 ORDER BY c.created_at DESC,c.id DESC,c.rank LIMIT $2 OFFSET $3
 )
 SELECT (SELECT count(*) FROM candidates),coalesce(jsonb_agg(value ORDER BY created_at DESC,id DESC,rank),'[]'::jsonb) FROM page`,
		t.workspace, n, (p-1)*n).Scan(&total, &raw)
	if e != nil {
		return items, 0, e
	}
	e = json.Unmarshal(raw, &items)
	return items, total, e
}
