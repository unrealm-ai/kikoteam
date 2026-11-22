package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

const groupCandidatesSQL = `SELECT id,provider,name,evidence FROM (
 SELECT g.id,'web'::text AS provider,g.title AS name,'member'::text AS evidence FROM platform_web_groups g JOIN platform_web_group_agents ga ON ga.group_id=g.id WHERE g.workspace_id=$1 AND ga.agent_id=$3 AND ga.status='active' AND g.status='active' AND $2::uuid IS NOT NULL
 UNION ALL
 SELECT x.id,b.provider,x.display_name,'observed'::text FROM platform_channel_observed_chats x JOIN platform_channel_bindings b ON b.id=x.binding_id WHERE b.workspace_id=$1 AND b.agent_id=$3 AND b.enabled AND b.provider IN ('dingtalk','wecom') AND b.identity_epoch=x.identity_epoch AND b.external_tenant_id=x.external_tenant_id AND b.identity_verified_generation=b.connection_generation AND manager_channel_group_allowed(b.id,b.identity_epoch,b.external_tenant_id,x.external_chat_id,'')
 UNION ALL
 SELECT x.id,b.provider,x.display_name,'provider_verified'::text FROM platform_channel_group_candidates x JOIN platform_channel_bindings b ON b.id=x.binding_id WHERE b.workspace_id=$1 AND b.agent_id=$3 AND b.enabled AND b.provider='feishu' AND b.identity_epoch=x.identity_epoch AND b.external_tenant_id=x.external_tenant_id AND b.connection_generation=x.connection_generation AND b.config_revision=x.config_revision AND x.expires_at>now() AND manager_channel_group_allowed(b.id,b.identity_epoch,b.external_tenant_id,x.external_chat_id,'')
) available WHERE ($6::uuid IS NULL OR (provider,id)>($5,$6::uuid)) ORDER BY provider,id LIMIT $4`

func (s *Store) ListTriggerGroupCandidates(ctx context.Context, w, actor, agent string, n int, afterProvider, afterID string) ([]service.TriggerGroupCandidate, bool, error) {
	out := []service.TriggerGroupCandidate{}
	if n < 1 || n > 100 {
		return out, false, service.Fail("invalid_argument", 400, "limit无效")
	}
	err := s.readTriggerAdmin(ctx, w, actor, agent, func(tx *sql.Tx) error {
		rows, e := tx.QueryContext(ctx, groupCandidatesSQL, w, actor, agent, n+1, afterProvider, nullID(afterID))
		if e != nil {
			return e
		}
		defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit close below.
		for rows.Next() {
			var v service.TriggerGroupCandidate
			if e = rows.Scan(&v.ID, &v.Provider, &v.Name, &v.Evidence); e != nil {
				return errors.Join(e, rows.Close())
			}
			out = append(out, v)
		}
		return errors.Join(rows.Err(), rows.Close())
	})
	more := len(out) > n
	if more {
		out = out[:n]
	}
	return out, more, err
}
func (s *Store) ResolveTriggerGroupCandidate(ctx context.Context, w, actor, agent string, q service.TriggerGroupResolve) (service.TriggerNotificationTarget, error) {
	var out service.TriggerNotificationTarget
	if !service.ValidID(q.CandidateID) {
		return out, service.Fail("invalid_argument", 400, "群候选无效")
	}
	err := s.readTriggerAdmin(ctx, w, actor, agent, func(tx *sql.Tx) error {
		var id string
		var e error
		if q.Provider == "web" {
			id, e = resolveWebNoticeConversation(ctx, tx, w, actor, agent, q.CandidateID)
		} else {
			id, e = resolveChannelNoticeConversation(ctx, tx, w, agent, q)
		}
		if e != nil {
			return e
		}
		if e = s.ensureSystemGroupChat(ctx, tx, w, agent, id); e != nil {
			return e
		}
		out.ConversationID = id
		raw, e := json.Marshal([]service.TriggerNotificationTarget{out})
		if e != nil {
			return e
		}
		return ValidateTriggerTargetsTx(ctx, tx, w, actor, agent, raw)
	})
	return out, err
}
func resolveWebNoticeConversation(ctx context.Context, tx *sql.Tx, w, actor, agent, group string) (string, error) {
	var epoch, agentEpoch int64
	err := tx.QueryRowContext(ctx, `SELECT g.admission_epoch,ga.admission_epoch FROM platform_web_groups g JOIN platform_web_group_agents ga ON ga.group_id=g.id WHERE g.id=$1 AND g.workspace_id=$2 AND ga.agent_id=$3 AND g.status='active' AND ga.status='active' AND $4::uuid IS NOT NULL FOR SHARE OF g,ga`, group, w, agent, actor).Scan(&epoch, &agentEpoch)
	if err != nil {
		return "", visibleError(err)
	}
	var id string
	err = tx.QueryRowContext(ctx, `INSERT INTO manager_conversations(id,workspace_id,agent_id,system_owned,web_group_id,web_group_epoch,web_group_agent_epoch)VALUES($1,$2,$3,true,$4,$5,$6) ON CONFLICT(web_group_id,agent_id,web_group_epoch,web_group_agent_epoch) WHERE web_group_id IS NOT NULL DO UPDATE SET web_group_id=EXCLUDED.web_group_id RETURNING id`, service.NewID(), w, agent, group, epoch, agentEpoch).Scan(&id)
	return id, err
}
func resolveChannelNoticeConversation(ctx context.Context, tx *sql.Tx, w, agent string, q service.TriggerGroupResolve) (string, error) {
	if q.Provider != "feishu" && q.Provider != "dingtalk" && q.Provider != "wecom" {
		return "", service.Fail("invalid_argument", 400, "渠道无效")
	}
	table := "platform_channel_observed_chats"
	extra := " AND b.identity_verified_generation=b.connection_generation"
	if q.Provider == "feishu" {
		table = "platform_channel_group_candidates"
		extra = " AND x.connection_generation=b.connection_generation AND x.config_revision=b.config_revision AND x.expires_at>now()"
	}
	var binding, tenant, chat string
	var epoch int64
	err := tx.QueryRowContext(ctx, `SELECT b.id,b.identity_epoch,b.external_tenant_id,x.external_chat_id FROM `+table+` x JOIN platform_channel_bindings b ON b.id=x.binding_id WHERE x.id=$1 AND b.workspace_id=$2 AND b.agent_id=$3 AND b.provider=$4 AND b.enabled AND b.identity_epoch=x.identity_epoch AND b.external_tenant_id=x.external_tenant_id AND manager_channel_group_allowed(b.id,b.identity_epoch,b.external_tenant_id,x.external_chat_id,'')`+extra+` FOR SHARE OF b,x`, q.CandidateID, w, agent, q.Provider).Scan(&binding, &epoch, &tenant, &chat)
	if errors.Is(err, sql.ErrNoRows) && q.Provider == "wecom" {
		err = tx.QueryRowContext(ctx, `SELECT b.id,b.identity_epoch,b.external_tenant_id,x.external_chat_id FROM platform_channel_group_candidates x JOIN platform_channel_bindings b ON b.id=x.binding_id WHERE x.id=$1 AND b.workspace_id=$2 AND b.agent_id=$3 AND b.provider='wecom' AND b.enabled AND b.identity_epoch=x.identity_epoch AND b.external_tenant_id=x.external_tenant_id AND b.connection_generation=x.connection_generation AND b.identity_verified_generation=b.connection_generation AND b.config_revision=x.config_revision AND x.expires_at>now() AND manager_channel_group_allowed(b.id,b.identity_epoch,b.external_tenant_id,x.external_chat_id,'') FOR SHARE OF b,x`, q.CandidateID, w, agent).Scan(&binding, &epoch, &tenant, &chat)
	}
	if err != nil {
		return "", visibleError(err)
	}
	var id string
	err = tx.QueryRowContext(ctx, `INSERT INTO manager_conversations(id,workspace_id,agent_id,system_owned,channel_binding_id,channel_identity_epoch,channel_tenant_id,channel_chat_type,external_chat_id,channel_thread_id)VALUES($1,$2,$3,true,$4,$5,$6,'group',$7,'') ON CONFLICT(channel_binding_id,channel_identity_epoch,channel_tenant_id,channel_chat_type,external_chat_id,channel_thread_id) WHERE channel_binding_id IS NOT NULL DO UPDATE SET external_chat_id=EXCLUDED.external_chat_id RETURNING id`, service.NewID(), w, agent, binding, epoch, tenant, chat).Scan(&id)
	return id, err
}
func (s *Store) ensureSystemGroupChat(ctx context.Context, tx *sql.Tx, w, agent, conversation string) error {
	var lifecycle string
	if e := tx.QueryRowContext(ctx, `SELECT lifecycle FROM manager_conversations WHERE id=$1 FOR UPDATE`, conversation).Scan(&lifecycle); e != nil {
		return e
	}
	if lifecycle != "active" {
		return service.Fail("conversation_archived", 409, "群会话已归档")
	}
	var exists bool
	if e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM manager_sessions WHERE conversation_id=$1 AND mode='chat')`, conversation).Scan(&exists); e != nil {
		return e
	}
	if exists {
		return nil
	}
	t := &sessionTx{ctx: ctx, tx: tx, store: s, target: service.Target{Workspace: w, AgentID: agent, Conversation: conversation}}
	t.state.AgentID = agent
	cfg, e := t.Configuration("chat")
	if e != nil {
		return e
	}
	raw, e := json.Marshal(cfg)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO manager_sessions(id,workspace_id,agent_id,conversation_id,mode,system_owned,configuration)VALUES($1,$2,$3,$4,'chat',true,$5)`, service.NewID(), w, agent, conversation, raw)
	return e
}
