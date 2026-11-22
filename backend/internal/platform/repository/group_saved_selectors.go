package repository

import (
	"context"
	"database/sql"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"strings"
)

func (s *GroupSelectorStore) ResolveSavedGroups(ctx context.Context, w, actor, agent string, selectors []service.UsageSelector) ([]service.GroupSelection, error) {
	out := []service.GroupSelection{}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	if err = selectionAdmin(ctx, tx, w, actor, agent); err != nil {
		return out, err
	}
	for _, v := range selectors {
		g, e := resolveSavedGroup(ctx, tx, w, agent, v)
		if e != nil {
			return out, e
		}
		out = append(out, g)
	}
	return out, tx.Commit()
}
func resolveSavedGroup(ctx context.Context, tx *sql.Tx, w, agent string, v service.UsageSelector) (service.GroupSelection, error) {
	// Query exact source identity. Never match display name or provider ID alone.
	g, err := scanGroupSelection(tx.QueryRowContext(ctx, `SELECT * FROM (`+groupSelectionSourcesSQL+`) candidates WHERE provider=$3 AND external_id=$4 AND binding=$5 AND identity_epoch=$6 ORDER BY CASE WHEN evidence='observed' THEN 0 ELSE 1 END,id LIMIT 1`, w, agent, v.Channel, v.ID, v.BindingID, v.IdentityEpoch), w, agent)
	if err == nil {
		return g, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return g, err
	}
	g = unavailableSavedGroup(w, agent, v)
	var name string
	if v.Channel == "web" {
		// A previous employee membership proves metadata association; workspace admin
		// alone is not sufficient to resolve an unrelated private group's name.
		err = tx.QueryRowContext(ctx, `SELECT g.title FROM platform_web_groups g JOIN platform_web_group_agents ga ON ga.group_id=g.id WHERE g.workspace_id=$1 AND ga.agent_id=$2 AND g.id=$3`, w, agent, v.ID).Scan(&name)
	} else {
		err = tx.QueryRowContext(ctx, `SELECT source.display_name FROM platform_channel_bindings b JOIN LATERAL (
   SELECT x.display_name,0 AS priority FROM platform_channel_observed_chats x WHERE x.binding_id=b.id AND x.identity_epoch=b.identity_epoch AND x.external_tenant_id=b.external_tenant_id AND x.external_chat_id=$5
   UNION ALL
   SELECT x.display_name,1 FROM platform_channel_group_candidates x WHERE x.binding_id=b.id AND x.identity_epoch=b.identity_epoch AND x.external_tenant_id=b.external_tenant_id AND x.external_chat_id=$5
  ) source ON true WHERE b.workspace_id=$1 AND b.agent_id=$2 AND b.provider=$3 AND b.id=$4 AND b.identity_epoch=$6 ORDER BY source.priority LIMIT 1`, w, agent, v.Channel, v.BindingID, v.ID, v.IdentityEpoch).Scan(&name)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return g, err
	}
	if strings.TrimSpace(name) != "" {
		g.Name = name
	}
	return g, nil
}
func unavailableSavedGroup(w, agent string, v service.UsageSelector) service.GroupSelection {
	ref := service.SelectionRef{Kind: "group", Provider: v.Channel, WorkspaceID: w, AgentID: agent, BindingID: v.BindingID, IdentityEpoch: v.IdentityEpoch, ExternalID: v.ID, Namespace: "chat_id"}
	if v.Channel == "web" {
		ref.ID = v.ID
		ref.Namespace = "web_group_id"
	}
	return service.GroupSelection{Ref: ref, Provider: v.Channel, Name: v.ID, Evidence: "saved_reference", Coverage: "unknown", Status: "unavailable", Selectable: false, DisabledReason: "selection_unavailable"}
}
