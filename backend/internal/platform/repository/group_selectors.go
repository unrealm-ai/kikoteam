package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type GroupSelectorStore struct{ DB *sql.DB }

func NewGroupSelectorStore(db *sql.DB) *GroupSelectorStore { return &GroupSelectorStore{DB: db} }

// Each adapter contributes an employee-scoped source. No operator group membership
// is required and no conversation, session, input or outbound message is created.
const webGroupSelectionSQL = `SELECT g.id,'web'::text AS provider,g.title AS name,'member'::text AS evidence,'all_joined'::text AS coverage,''::text AS binding,''::text AS tenant,g.id::text AS external_id,0::bigint AS identity_epoch,0::bigint AS generation,0::bigint AS revision,g.admission_epoch AS group_epoch,ga.admission_epoch AS agent_epoch FROM platform_web_groups g JOIN platform_web_group_agents ga ON ga.group_id=g.id WHERE g.workspace_id=$1 AND ga.agent_id=$2 AND ga.status='active' AND g.status='active'`
const observedGroupSelectionSQL = `SELECT x.id,b.provider,x.display_name,'observed'::text,'observed_only'::text,b.id::text,b.external_tenant_id,x.external_chat_id,b.identity_epoch,b.connection_generation,b.config_revision,0::bigint,0::bigint FROM platform_channel_observed_chats x JOIN platform_channel_bindings b ON b.id=x.binding_id WHERE b.workspace_id=$1 AND b.agent_id=$2 AND b.enabled AND b.provider IN ('feishu','dingtalk','wecom') AND b.identity_epoch=x.identity_epoch AND b.external_tenant_id=x.external_tenant_id AND (b.provider='feishu' OR b.identity_verified_generation=b.connection_generation) AND manager_channel_group_allowed(b.id,b.identity_epoch,b.external_tenant_id,x.external_chat_id,'')`
const discoveredGroupSelectionSQL = `SELECT x.id,b.provider,x.display_name,'provider_verified'::text,CASE WHEN b.provider='wecom' THEN 'recent_window' ELSE 'all_joined' END,b.id::text,b.external_tenant_id,x.external_chat_id,b.identity_epoch,b.connection_generation,b.config_revision,0::bigint,0::bigint FROM platform_channel_group_candidates x JOIN platform_channel_bindings b ON b.id=x.binding_id WHERE b.workspace_id=$1 AND b.agent_id=$2 AND b.enabled AND b.provider IN ('feishu','wecom') AND b.identity_epoch=x.identity_epoch AND b.external_tenant_id=x.external_tenant_id AND b.connection_generation=x.connection_generation AND b.identity_verified_generation=b.connection_generation AND b.config_revision=x.config_revision AND x.expires_at>now() AND manager_channel_group_allowed(b.id,b.identity_epoch,b.external_tenant_id,x.external_chat_id,'')`

// Adapters expose metadata from their own source; provider networking/refresh is
// handled separately and never occurs during a selector read.
type groupSourceAdapter interface{ sourceSQL() string }
type webGroupAdapter struct{}
type feishuGroupAdapter struct{}
type dingtalkGroupAdapter struct{}
type wecomGroupAdapter struct{}

func (webGroupAdapter) sourceSQL() string { return webGroupSelectionSQL }
func (feishuGroupAdapter) sourceSQL() string {
	return `SELECT * FROM (` + observedGroupSelectionSQL + ` UNION ALL ` + discoveredGroupSelectionSQL + `) source WHERE provider='feishu'`
}
func (dingtalkGroupAdapter) sourceSQL() string {
	return `SELECT * FROM (` + observedGroupSelectionSQL + `) source WHERE provider='dingtalk'`
}
func (wecomGroupAdapter) sourceSQL() string {
	return `SELECT * FROM (` + observedGroupSelectionSQL + ` UNION ALL ` + discoveredGroupSelectionSQL + `) source WHERE provider='wecom'`
}
func groupSelectionSources() string {
	adapters := []groupSourceAdapter{webGroupAdapter{}, feishuGroupAdapter{}, dingtalkGroupAdapter{}, wecomGroupAdapter{}}
	sources := make([]string, 0, len(adapters))
	for _, adapter := range adapters {
		sources = append(sources, adapter.sourceSQL())
	}
	return strings.Join(sources, ` UNION ALL `)
}

var groupSelectionSourcesSQL = groupSelectionSources()

// Prefer observed identity for deduplication so refreshing discovery does not
// replace an already selectable observed reference.
var groupSelectionSearchSQL = `WITH available AS (` + groupSelectionSourcesSQL + `), canonical AS (SELECT DISTINCT ON(provider,binding,tenant,external_id) * FROM available ORDER BY provider,binding,tenant,external_id,CASE WHEN evidence='observed' THEN 0 ELSE 1 END,id) SELECT * FROM canonical WHERE ($3='' OR provider=$3) AND ($3<>'feishu' OR evidence='observed') AND ($4='' OR strpos(lower(name),lower($4))>0 OR strpos(lower(external_id),lower($4))>0) AND ($6='' OR (provider,id)>($5,NULLIF($6,'')::uuid)) ORDER BY provider,id LIMIT $7`

func selectionAdmin(ctx context.Context, tx *sql.Tx, w, actor, agent string) error {
	return AuthorizeEmployeeManagement(ctx, tx, w, agent, actor)
}
func scanGroupSelection(row scanner, w, agent string) (service.GroupSelection, error) {
	var g service.GroupSelection
	r := &g.Ref
	err := row.Scan(&r.ID, &g.Provider, &g.Name, &g.Evidence, &g.Coverage, &r.BindingID, &r.TenantID, &r.ExternalID, &r.IdentityEpoch, &r.Generation, &r.ConfigRevision, &r.GroupEpoch, &r.AgentEpoch)
	r.Kind = "group"
	r.Provider = g.Provider
	r.WorkspaceID = w
	r.AgentID = agent
	r.Namespace = "chat_id"
	if g.Provider == "web" {
		r.Namespace = "web_group_id"
	}
	g.Status = "available"
	g.Selectable = true
	if strings.TrimSpace(g.Name) == "" {
		g.Name = r.ExternalID
	}
	return g, err
}
func (s *GroupSelectorStore) SearchGroups(ctx context.Context, w, actor, agent string, q service.GroupSelectionQuery, b service.GroupSelectionBoundary) ([]service.GroupSelection, bool, error) {
	out := []service.GroupSelection{}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return out, false, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	if err = selectionAdmin(ctx, tx, w, actor, agent); err != nil {
		return out, false, err
	}
	rows, err := tx.QueryContext(ctx, groupSelectionSearchSQL, w, agent, q.Provider, q.Q, b.Provider, b.ID, q.Limit+1)
	if err != nil {
		return out, false, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit close below.
	for rows.Next() {
		g, e := scanGroupSelection(rows, w, agent)
		if e != nil {
			return out, false, errors.Join(e, rows.Close())
		}
		out = append(out, g)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return out, false, err
	}
	more := len(out) > q.Limit
	if more {
		out = out[:q.Limit]
	}
	return out, more, tx.Commit()
}
func (s *GroupSelectorStore) ResolveGroups(ctx context.Context, w, actor, agent string, refs []service.SelectionRef) ([]service.GroupSelection, error) {
	out := []service.GroupSelection{}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	if err = selectionAdmin(ctx, tx, w, actor, agent); err != nil {
		return out, err
	}
	for _, ref := range refs {
		g, e := scanGroupSelection(tx.QueryRowContext(ctx, `SELECT * FROM (`+groupSelectionSourcesSQL+`) candidates WHERE provider=$3 AND id=$4`, w, agent, ref.Provider, ref.ID), w, agent)
		if errors.Is(e, sql.ErrNoRows) {
			return out, service.Fail("selection_unavailable", 409, "群已不可用或候选已过期，请刷新")
		}
		if e != nil {
			return out, e
		}
		if g.Ref != ref {
			return out, service.Fail("selection_changed", 409, "群来源或绑定已变化，请重新选择")
		}
		out = append(out, g)
	}
	return out, tx.Commit()
}
