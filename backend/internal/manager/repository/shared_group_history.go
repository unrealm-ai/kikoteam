package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	platform "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

// RequestSharedGroupHistory is an internal repository primitive, not a public
// actor API. It remains fail-closed until an approved execution mode provides
// manager_shared_history_allowed and durable pre-exposure lineage enforcement.
func (s *Store) RequestSharedGroupHistory(ctx context.Context, scope service.SharedHistoryScope, q service.SharedHistoryRequest) (service.SharedHistoryResult, error) {
	out := service.SharedHistoryResult{Items: []service.SharedHistoryItem{}}
	var source platform.SelectionRef
	if json.Unmarshal(scope.SourceRef, &source) != nil || source.Kind != "group" || source.WorkspaceID != scope.WorkspaceID || source.AgentID != scope.AgentID || source.ExternalID == "" || q.Limit < 1 || q.Limit > 50 || q.StartMS < 0 || q.EndMS <= q.StartMS || (source.Provider == "wecom" && q.EndMS-q.StartMS > 604800000) {
		return out, service.Fail("invalid_history_query", 400, "群历史范围无效")
	}
	if source.Provider == "dingtalk" {
		return out, service.Fail("group_history_unsupported", 422, "当前钉钉接入不支持群历史")
	}
	switch source.Provider {
	case "web", "feishu", "wecom":
	default:
		return out, service.Fail("group_history_unsupported", 422, "渠道不支持群历史")
	}
	for _, id := range []string{scope.WorkspaceID, scope.AgentID, scope.SessionID, scope.InputID} {
		if !service.ValidID(id) {
			return out, service.Fail("invalid_history_scope", 400, "运行身份无效")
		}
	}
	if scope.PolicyID != "" && !service.ValidID(scope.PolicyID) {
		return out, service.Fail("invalid_history_scope", 400, "策略身份无效")
	}
	if scope.PolicyID == "" && (scope.PolicyVersion != 0 || scope.EdgeOrdinal != -1) {
		return out, service.Fail("invalid_history_scope", 400, "来源群身份无效")
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	var now int64
	if e = tx.QueryRowContext(ctx, `SELECT (extract(epoch FROM clock_timestamp())*1000)::bigint`).Scan(&now); e != nil {
		return out, e
	}
	if q.EndMS > now || (source.Provider == "wecom" && q.StartMS < now-604800000) {
		return out, service.Fail("invalid_history_range", 400, "渠道历史时间范围无效")
	}
	var locked string
	if e = tx.QueryRowContext(ctx, `SELECT id FROM manager_inputs WHERE id=$1 FOR UPDATE`, scope.InputID).Scan(&locked); e != nil {
		return out, visibleError(e)
	}
	var budget bool
	if e = tx.QueryRowContext(ctx, `SELECT count(*)<20 FROM manager_group_history_queries WHERE input_id=$1`, scope.InputID).Scan(&budget); e != nil {
		return out, e
	}
	if !budget {
		return out, service.Fail("history_budget_exhausted", 429, "本次执行历史预算已用尽")
	}
	id := service.NewID()
	_, e = tx.ExecContext(ctx, `INSERT INTO manager_group_history_queries(id,workspace_id,agent_id,session_id,input_id,policy_id,policy_version,edge_ordinal,source_ref,target_ref,start_ms,end_ms,page_limit)VALUES($1,$2,$3,$4,$5,NULLIF($6,'')::uuid,$7,$8,$9,$10,$11,$12,$13)`, id, scope.WorkspaceID, scope.AgentID, scope.SessionID, scope.InputID, scope.PolicyID, scope.PolicyVersion, scope.EdgeOrdinal, scope.SourceRef, scope.TargetRef, q.StartMS, q.EndMS, q.Limit)
	if e != nil {
		return out, e
	}
	if e = sharedHistoryAllowed(ctx, tx, id); e != nil {
		return out, e
	}
	out.ID = id
	out.Status = "pending"
	out.SourceRef = scope.SourceRef
	out.StartMS = q.StartMS
	out.EndMS = q.EndMS
	return out, tx.Commit()
}
func sharedHistoryAllowed(ctx context.Context, tx *sql.Tx, id string) error {
	var allowed bool
	if e := tx.QueryRowContext(ctx, `SELECT COALESCE(manager_shared_history_allowed($1),false)`, id).Scan(&allowed); e != nil {
		return e
	}
	if !allowed {
		return service.Fail("shared_history_execution_unavailable", 403, "群共享正文读取尚未开放")
	}
	return nil
}

const sharedHistoryJobColumns = `id,lease_token,source_ref,start_ms,end_ms,page_limit,provider_cursor,pending_items`

func scanSharedHistoryJob(row interface{ Scan(...any) error }) (service.SharedHistoryJob, error) {
	var j service.SharedHistoryJob
	var pending []byte
	e := row.Scan(&j.ID, &j.LeaseToken, &j.SourceRef, &j.StartMS, &j.EndMS, &j.Limit, &j.ProviderCursor, &pending)
	if e == nil {
		e = json.Unmarshal(pending, &j.PendingItems)
	}
	return j, e
}
func (s *Store) ClaimSharedHistory(ctx context.Context) ([]service.SharedHistoryJob, error) {
	out := []service.SharedHistoryJob{}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	var id string
	e = tx.QueryRowContext(ctx, `SELECT id FROM manager_group_history_queries WHERE (status='pending' OR(status='leased' AND lease_until<now())) AND expires_at>now() AND source_ref->>'provider'<>'web' AND manager_shared_history_allowed(id) ORDER BY created_at,id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		return out, nil
	}
	if e != nil {
		return out, e
	}
	_, e = tx.ExecContext(ctx, `UPDATE manager_group_history_queries SET status='leased',lease_token=gen_random_uuid(),lease_until=now()+interval '30 seconds' WHERE id=$1`, id)
	if e != nil {
		return out, e
	}
	j, e := scanSharedHistoryJob(tx.QueryRowContext(ctx, `SELECT `+sharedHistoryJobColumns+` FROM manager_group_history_queries WHERE id=$1`, id))
	if e != nil {
		return out, e
	}
	out = append(out, j)
	return out, tx.Commit()
}
func (s *Store) AuthorizeSharedHistory(ctx context.Context, id, token string) (service.SharedHistoryJob, error) {
	if !service.ValidID(id) || !service.ValidID(token) {
		return service.SharedHistoryJob{}, service.Fail("invalid_argument", 400, "查询身份无效")
	}
	j, e := scanSharedHistoryJob(s.DB.QueryRowContext(ctx, `SELECT `+sharedHistoryJobColumns+` FROM manager_group_history_queries WHERE id=$1 AND lease_token=$2 AND status='leased' AND lease_until>now() AND expires_at>now() AND manager_shared_history_allowed(id)`, id, token))
	if errors.Is(e, sql.ErrNoRows) {
		e = service.Fail("shared_history_forbidden", 403, "查询授权无效")
	}
	return j, e
}
func (s *Store) CompleteSharedHistory(ctx context.Context, id string, v service.SharedHistoryCompletion) error {
	if !service.ValidID(id) || !service.ValidID(v.LeaseToken) || (v.Status != "completed" && v.Status != "failed") || len(v.Page.NextProviderCursor) > 8192 {
		return service.Fail("invalid_argument", 400, "历史结果无效")
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	var inputID string
	if e = tx.QueryRowContext(ctx, `SELECT input_id FROM manager_group_history_queries WHERE id=$1`, id).Scan(&inputID); e != nil {
		return visibleError(e)
	}
	if e = tx.QueryRowContext(ctx, `SELECT id FROM manager_inputs WHERE id=$1 FOR UPDATE`, inputID).Scan(&inputID); e != nil {
		return visibleError(e)
	}
	j, e := scanSharedHistoryJob(tx.QueryRowContext(ctx, `SELECT `+sharedHistoryJobColumns+` FROM manager_group_history_queries WHERE id=$1 AND lease_token=$2 AND status='leased' AND lease_until>now() AND expires_at>now() FOR UPDATE`, id, v.LeaseToken))
	if e != nil {
		return visibleError(e)
	}
	if e = sharedHistoryAllowed(ctx, tx, id); e != nil {
		return e
	}
	if len(v.Page.Items) > j.Limit {
		return service.Fail("invalid_argument", 400, "历史条数超限")
	}
	seen := map[string]bool{}
	if len(v.Page.PendingItems) > 500 {
		return service.Fail("invalid_argument", 400, "历史缓存条数超限")
	}
	for _, m := range append(append([]service.SharedHistoryItem{}, v.Page.Items...), v.Page.PendingItems...) {
		if m.ID == "" || seen[m.ID] || m.CreatedAtMS < j.StartMS || m.CreatedAtMS >= j.EndMS {
			return service.Fail("invalid_argument", 400, "历史来源范围无效")
		}
		seen[m.ID] = true
	}
	raw, e := json.Marshal(v.Page)
	if e != nil {
		return e
	}
	if len(raw) > 524288 {
		return service.Fail("history_page_too_large", 413, "历史页过大")
	}
	if v.Status == "failed" {
		raw = []byte(`{"items":[],"coverage":"unavailable"}`)
		v.Page.NextProviderCursor = ""
	}
	visible, e := json.Marshal(v.Page.Items)
	if e != nil {
		return e
	}
	var budget bool
	if e = tx.QueryRowContext(ctx, `SELECT bytes_before+$2<=64000 AND (SELECT COALESCE(sum(page_bytes),0) FROM manager_group_history_queries other WHERE other.input_id=q.input_id)+$2<=128000 FROM manager_group_history_queries q WHERE id=$1`, id, len(visible)).Scan(&budget); e != nil {
		return e
	}
	if !budget {
		return service.Fail("history_budget_exhausted", 429, "历史读取预算已用尽")
	}
	_, e = tx.ExecContext(ctx, `UPDATE manager_group_history_queries SET status=$2,snapshot=$3,next_provider_cursor=$4,page_bytes=$5,lease_until=NULL,error_code=CASE WHEN $2='failed' THEN 'provider_unavailable' ELSE NULL END WHERE id=$1`, id, v.Status, raw, v.Page.NextProviderCursor, len(visible))
	if e != nil {
		return e
	}
	return tx.Commit()
}

// ReadSharedGroupHistory checks the live Input and records destination-level
// lineage before any page body can leave this transaction, including retries.
func (s *Store) ReadSharedGroupHistory(ctx context.Context, id, inputID string) (service.SharedHistoryResult, error) {
	out := service.SharedHistoryResult{Items: []service.SharedHistoryItem{}}
	if !service.ValidID(id) || !service.ValidID(inputID) {
		return out, service.Fail("invalid_argument", 400, "查询身份无效")
	}
	if e := s.prepareSharedWebPage(ctx, id, inputID); e != nil {
		return out, e
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns lineage success.
	var raw []byte
	e = tx.QueryRowContext(ctx, `SELECT id,status,source_ref,start_ms,end_ms,COALESCE(snapshot,'{}'::jsonb),(next_provider_cursor<>'' OR jsonb_array_length(COALESCE(snapshot->'pending_items','[]'))>0),COALESCE(error_code,'') FROM manager_group_history_queries WHERE id=$1 AND input_id=$2 AND expires_at>now()`, id, inputID).Scan(&out.ID, &out.Status, &out.SourceRef, &out.StartMS, &out.EndMS, &raw, &out.HasMore, &out.ErrorCode)
	if e != nil {
		return out, visibleError(e)
	}
	if e = sharedHistoryAllowed(ctx, tx, id); e != nil {
		return service.SharedHistoryResult{}, e
	}
	if out.Status == "completed" {
		var recorded bool
		if e = tx.QueryRowContext(ctx, `SELECT manager_shared_history_record_read($1)`, id).Scan(&recorded); e != nil {
			return service.SharedHistoryResult{}, e
		}
		if !recorded {
			return service.SharedHistoryResult{}, service.Fail("shared_history_forbidden", 403, "来源派生权限已变化")
		}
		var page service.SharedHistoryPage
		if e = json.Unmarshal(raw, &page); e != nil {
			return service.SharedHistoryResult{}, e
		}
		out.Items = page.Items
		out.Coverage = page.Coverage
	}
	return out, tx.Commit()
}
func (s *Store) ContinueSharedGroupHistory(ctx context.Context, id, inputID string) (string, error) {
	if !service.ValidID(id) || !service.ValidID(inputID) {
		return "", service.Fail("invalid_argument", 400, "查询身份无效")
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return "", e
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	var locked string
	if e = tx.QueryRowContext(ctx, `SELECT id FROM manager_inputs WHERE id=$1 FOR UPDATE`, inputID).Scan(&locked); e != nil {
		return "", visibleError(e)
	}
	var next sql.NullString
	e = tx.QueryRowContext(ctx, `SELECT next_query_id FROM manager_group_history_queries WHERE id=$1 AND input_id=$2 AND status='completed' AND expires_at>now() AND (next_provider_cursor<>'' OR jsonb_array_length(COALESCE(snapshot->'pending_items','[]'))>0) FOR UPDATE`, id, inputID).Scan(&next)
	if e != nil {
		return "", visibleError(e)
	}
	if e = sharedHistoryAllowed(ctx, tx, id); e != nil {
		return "", e
	}
	if next.Valid {
		return next.String, nil
	}
	var budget bool
	if e = tx.QueryRowContext(ctx, `SELECT count(*)<20 FROM manager_group_history_queries WHERE input_id=$1`, inputID).Scan(&budget); e != nil {
		return "", e
	}
	if !budget {
		return "", service.Fail("history_budget_exhausted", 429, "本次执行历史预算已用尽")
	}
	nextID := service.NewID()
	r, e := tx.ExecContext(ctx, `INSERT INTO manager_group_history_queries(id,workspace_id,agent_id,session_id,input_id,policy_id,policy_version,edge_ordinal,source_ref,target_ref,start_ms,end_ms,page_limit,provider_cursor,parent_id,page_number,bytes_before,expires_at,pending_items) SELECT $2,workspace_id,agent_id,session_id,input_id,policy_id,policy_version,edge_ordinal,source_ref,target_ref,start_ms,end_ms,page_limit,next_provider_cursor,id,page_number+1,bytes_before+page_bytes,expires_at,COALESCE(snapshot->'pending_items','[]') FROM manager_group_history_queries WHERE id=$1 AND page_number<10 AND bytes_before+page_bytes<64000`, id, nextID)
	if e != nil {
		return "", e
	}
	count, e := r.RowsAffected()
	if e != nil {
		return "", e
	}
	if count != 1 {
		return "", service.Fail("history_budget_exhausted", 429, "查询历史预算已用尽")
	}
	if e = sharedHistoryAllowed(ctx, tx, nextID); e != nil {
		return "", e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE manager_group_history_queries SET next_query_id=$2 WHERE id=$1`, id, nextID); e != nil {
		return "", e
	}
	return nextID, tx.Commit()
}
