package repository

import (
	"context"
	"database/sql"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"strings"
	"time"
)

type webGroupJob struct {
	id, group, message, agent, actor, workspace string
	seq, groupEpoch, agentEpoch, memberEpoch    int64
}

func (s *Store) ProcessWebGroupInbox(ctx context.Context) error {
	var job webGroupJob
	err := s.DB.QueryRowContext(ctx, `SELECT n.id,n.group_id,m.workspace_id FROM manager_web_group_inbox n JOIN manager_web_group_messages m ON m.id=n.message_id WHERE n.status='pending' AND NOT EXISTS(SELECT 1 FROM manager_web_group_inbox p WHERE p.group_id=n.group_id AND p.agent_id=n.agent_id AND p.status='pending' AND p.message_seq<n.message_seq) ORDER BY n.message_seq,n.id LIMIT 1`).Scan(&job.id, &job.group, &job.workspace)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	// Even archived groups are locked so stale work can be durably rejected.
	var locked string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM platform_web_groups WHERE id=$1 FOR SHARE`, job.group).Scan(&locked); err != nil {
		return err
	}
	err = tx.QueryRowContext(ctx, `SELECT message_id,agent_id,sender_id,message_seq,group_epoch,agent_epoch,member_epoch FROM manager_web_group_inbox WHERE id=$1 AND status='pending' FOR UPDATE SKIP LOCKED`, job.id).Scan(&job.message, &job.agent, &job.actor, &job.seq, &job.groupEpoch, &job.agentEpoch, &job.memberEpoch)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var allowed bool
	var floor int64
	err = tx.QueryRowContext(ctx, `SELECT platform_web_group_agent_allowed(g.workspace_id,g.id,ga.agent_id,gm.user_id) AND g.admission_epoch=$4 AND ga.admission_epoch=$5 AND gm.admission_epoch=$6,0::bigint FROM platform_web_groups g JOIN platform_web_group_agents ga ON ga.group_id=g.id JOIN platform_web_group_members gm ON gm.group_id=g.id WHERE g.id=$1 AND ga.agent_id=$2 AND gm.user_id=$3`, job.group, job.agent, job.actor, job.groupEpoch, job.agentEpoch, job.memberEpoch).Scan(&allowed, &floor)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if !allowed {
		_, err = tx.ExecContext(ctx, `UPDATE manager_web_group_inbox SET status='rejected',error_code='authorization_changed',reply_done=true WHERE id=$1`, job.id)
		if err != nil {
			return err
		}
		return tx.Commit()
	}
	t, err := s.webGroupSession(ctx, tx, job)
	if err != nil {
		return err
	}
	if t.state.Session.Execution.QueuedInputCount >= 100 {
		return nil
	}
	if err = s.appendWebGroupJob(ctx, tx, t, job, floor); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) webGroupSession(ctx context.Context, tx *sql.Tx, j webGroupJob) (*sessionTx, error) {
	var conversation string
	err := tx.QueryRowContext(ctx, `INSERT INTO manager_conversations(id,workspace_id,agent_id,created_by,web_group_id,web_group_epoch,web_group_agent_epoch) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(web_group_id,agent_id,web_group_epoch,web_group_agent_epoch) WHERE web_group_id IS NOT NULL DO UPDATE SET web_group_id=EXCLUDED.web_group_id RETURNING id`, service.NewID(), j.workspace, j.agent, j.actor, j.group, j.groupEpoch, j.agentEpoch).Scan(&conversation)
	if err != nil {
		return nil, err
	}
	t := &sessionTx{ctx: service.WithWebGroupExecution(ctx, service.WebGroupExecution{ConversationID: conversation}), tx: tx, target: service.Target{Workspace: j.workspace, Actor: j.actor, Conversation: conversation}, store: s}
	t.state.AgentID = j.agent
	t.state.OwnerLifecycle = "active"
	t.state.Conversation, err = scanConversation(tx.QueryRowContext(ctx, `SELECT `+conversationColumns+` FROM manager_conversations c WHERE c.id=$1`, conversation))
	if err != nil {
		return nil, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT enabled,admission_epoch FROM platform_agents WHERE id=$1 FOR SHARE`, j.agent).Scan(&t.state.AgentEnabled, &t.state.AdmissionEpoch); err != nil {
		return nil, err
	}
	err = tx.QueryRowContext(ctx, `SELECT id FROM manager_sessions WHERE conversation_id=$1 AND mode='chat' FOR UPDATE`, conversation).Scan(&t.target.Session)
	if err == nil {
		return t, t.refresh()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	cfg, err := t.Configuration("chat")
	if err != nil {
		return nil, err
	}
	v := service.Session{ID: service.NewID(), WorkspaceID: j.workspace, AgentID: j.agent, ConversationID: conversation, CreatedBy: j.actor, Mode: "chat", Lifecycle: "active", Revision: 1, Configuration: cfg, CreatedAt: time.Now().UTC()}
	if err = t.InsertSession(v); err != nil {
		return nil, err
	}
	return t, nil
}
func (s *Store) appendWebGroupJob(ctx context.Context, tx *sql.Tx, t *sessionTx, j webGroupJob, floor int64) error {
	var boundary int64
	if err := tx.QueryRowContext(ctx, `SELECT web_group_boundary FROM manager_conversations WHERE id=$1`, t.target.Conversation).Scan(&boundary); err != nil {
		return err
	}
	if boundary > floor {
		floor = boundary
	}
	var enabled bool
	var limit int
	if err := tx.QueryRowContext(ctx, `SELECT (p.history->>'enabled')::boolean,(p.history->>'limit')::integer FROM platform_agents a CROSS JOIN LATERAL (SELECT platform_agent_usage_projection(a.usage_config,'web',NULL,0)->'history' AS history) p WHERE a.id=$1`, j.agent).Scan(&enabled, &limit); err != nil {
		return err
	}
	if !enabled || limit < 0 {
		limit = 0
	}
	if remaining := 99 - t.state.Session.Execution.QueuedInputCount; remaining < limit {
		limit = remaining
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+webMessageColumns+` FROM (SELECT * FROM manager_web_group_messages WHERE group_id=$1 AND seq>$2 AND seq<$3 ORDER BY seq DESC LIMIT $4) recent ORDER BY seq`, j.group, floor, j.seq, limit)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit closes report errors.
	items := []service.WebGroupMessage{}
	for rows.Next() {
		v, e := scanWebMessage(rows)
		if e != nil {
			return errors.Join(e, rows.Close())
		}
		items = append(items, v)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	current, err := scanWebMessage(tx.QueryRowContext(ctx, `SELECT `+webMessageColumns+` FROM manager_web_group_messages WHERE id=$1`, j.message))
	if err != nil {
		return err
	}
	items = append(items, current)
	var trigger service.Input
	for index, msg := range items {
		isTrigger := index == len(items)-1
		text, e := webGroupInputText(ctx, tx, msg)
		if e != nil {
			return e
		}
		kind := "user"
		if msg.Kind != "user" {
			kind = "system"
		}
		input, e := t.InsertInput(service.Input{ID: service.NewID(), SessionID: t.target.Session, SubmittedBy: j.actor, Text: text, DeliveryMode: "queue", SourceKind: kind, Status: "queued", TriggerAgentLoop: &isTrigger, CreatedAt: time.Now().UTC()})
		if e != nil {
			return e
		}
		if e = t.Event("input.updated", &input.ID, nil, service.InputFields(input)); e != nil {
			return e
		}
		if isTrigger {
			trigger = input
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE manager_conversations SET web_group_boundary=$2 WHERE id=$1`, t.target.Conversation, j.seq); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE manager_web_group_inbox SET status='accepted',conversation_id=$2,input_id=$3 WHERE id=$1`, j.id, t.target.Conversation, trigger.ID); err != nil {
		return err
	}
	return t.SnapshotEvent()
}
func webGroupInputText(ctx context.Context, tx *sql.Tx, m service.WebGroupMessage) (string, error) {
	name := channelLabel(m.DisplayName)
	if name == "" {
		name = "群成员"
	}
	if m.Kind != "user" {
		return "[系统消息]" + name + "：" + m.Text, nil
	}
	text := "[说话人=" + name + "]"
	names := []string{}
	for _, id := range m.MentionAgentIDs {
		var label string
		if err := tx.QueryRowContext(ctx, `SELECT name FROM platform_agents WHERE id=$1`, id).Scan(&label); err != nil {
			return "", err
		}
		label = strings.ReplaceAll(channelLabel(label), ",", "，")
		if label != "" {
			names = append(names, label)
		}
	}
	if len(names) > 0 {
		text += "[被@人=" + strings.Join(names, ",") + "]"
	}
	return text + m.Text, nil
}

// Every input (including delegated tasks/reports) records the member generation.
func (t *sessionTx) stampWebGroupInput(id string) error {
	if t.target.TaskSpaceID != "" {
		return nil
	}
	var group *string
	if err := t.tx.QueryRowContext(t.ctx, `SELECT web_group_id FROM manager_conversations WHERE id=$1`, t.target.Conversation).Scan(&group); err != nil {
		return err
	}
	if group == nil {
		return nil
	}
	var epoch int64
	if err := t.tx.QueryRowContext(t.ctx, `SELECT gm.admission_epoch FROM platform_web_group_members gm WHERE gm.group_id=$1 AND gm.user_id=$2 AND manager_web_group_actor_allowed($3,$2)`, *group, t.target.Actor, t.target.Conversation).Scan(&epoch); err != nil {
		return visibleError(err)
	}
	_, err := t.tx.ExecContext(t.ctx, `UPDATE manager_inputs SET web_group_member_epoch=$2 WHERE id=$1`, id, epoch)
	return err
}
