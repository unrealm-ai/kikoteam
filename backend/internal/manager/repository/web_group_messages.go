package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

const webMessageColumns = `id,group_id,seq,author_kind,COALESCE(sender_id::text,''),COALESCE(agent_id::text,''),display_name,text,mention_agent_ids,created_at`

func scanWebMessage(row scanner) (service.WebGroupMessage, error) {
	var v service.WebGroupMessage
	var raw []byte
	err := row.Scan(&v.ID, &v.GroupID, &v.Seq, &v.Kind, &v.SenderID, &v.AgentID, &v.DisplayName, &v.Text, &raw, &v.CreatedAt)
	if err == nil {
		err = json.Unmarshal(raw, &v.MentionAgentIDs)
	}
	return v, err
}
func lockWebGroup(ctx context.Context, tx *sql.Tx, w, g string) error {
	var id string
	return visibleError(tx.QueryRowContext(ctx, `SELECT id FROM platform_web_groups WHERE id=$1 AND workspace_id=$2 AND status='active' FOR SHARE`, g, w).Scan(&id))
}
func requireWebGroupMember(ctx context.Context, tx *sql.Tx, w, g, a string) error {
	var allowed bool
	if err := tx.QueryRowContext(ctx, `SELECT platform_web_group_member_allowed($1,$2,$3)`, w, g, a).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return service.Fail("resource_not_found", 404, "群不存在或不可访问")
	}
	return nil
}
func nextWebGroupSeq(ctx context.Context, tx *sql.Tx, g string) (int64, error) {
	var seq int64
	err := tx.QueryRowContext(ctx, `INSERT INTO manager_web_group_streams(group_id,last_seq) VALUES($1,1) ON CONFLICT(group_id) DO UPDATE SET last_seq=manager_web_group_streams.last_seq+1 RETURNING last_seq`, g).Scan(&seq)
	return seq, err
}
func (s *Store) PostWebGroupMessage(ctx context.Context, w, a, g, key string, r service.WebGroupPost) (service.WebGroupMessage, bool, error) {
	var out service.WebGroupMessage
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, false, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	if err = lockWebGroup(ctx, tx, w, g); err != nil {
		return out, false, err
	}
	if err = requireWebGroupMember(ctx, tx, w, g, a); err != nil {
		return out, false, err
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return out, false, err
	}
	sum := sha256.Sum256(raw)
	fp := hex.EncodeToString(sum[:])
	// All messages have a total order. Membership changes own the group lock first.
	if _, err = tx.ExecContext(ctx, `INSERT INTO manager_web_group_streams(group_id) VALUES($1) ON CONFLICT DO NOTHING`, g); err != nil {
		return out, false, err
	}
	var lock int64
	if err = tx.QueryRowContext(ctx, `SELECT last_seq FROM manager_web_group_streams WHERE group_id=$1 FOR UPDATE`, g).Scan(&lock); err != nil {
		return out, false, err
	}
	var prior string
	err = tx.QueryRowContext(ctx, `SELECT fingerprint FROM manager_web_group_messages WHERE group_id=$1 AND sender_id=$2 AND request_key=$3`, g, a, key).Scan(&prior)
	if err == nil {
		if prior != fp {
			return out, false, service.Fail("idempotency_conflict", 409, "去重键已用于其他消息")
		}
		out, err = scanWebMessage(tx.QueryRowContext(ctx, `SELECT `+webMessageColumns+` FROM manager_web_group_messages WHERE group_id=$1 AND sender_id=$2 AND request_key=$3`, g, a, key))
		return out, true, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return out, false, err
	}
	targets, err := webGroupTargets(ctx, tx, w, g, a, r)
	if err != nil {
		return out, false, err
	}
	seq, err := nextWebGroupSeq(ctx, tx, g)
	if err != nil {
		return out, false, err
	}
	var name string
	if err = tx.QueryRowContext(ctx, `SELECT name FROM platform_accounts WHERE id=$1`, a).Scan(&name); err != nil {
		return out, false, err
	}
	name = channelLabel(name)
	if name == "" {
		name = "群成员"
	}
	mentions := r.MentionAgentIDs
	if mentions == nil {
		mentions = []string{}
	}
	raw, err = json.Marshal(mentions)
	if err != nil {
		return out, false, err
	}
	out, err = scanWebMessage(tx.QueryRowContext(ctx, `INSERT INTO manager_web_group_messages(id,workspace_id,group_id,seq,author_kind,sender_id,display_name,text,mention_agent_ids,request_key,fingerprint) VALUES($1,$2,$3,$4,'user',$5,$6,$7,$8,$9,$10) RETURNING `+webMessageColumns, service.NewID(), w, g, seq, a, name, r.Text, raw, key, fp))
	if err != nil {
		return out, false, err
	}
	for _, target := range targets {
		if _, err = tx.ExecContext(ctx, `INSERT INTO manager_web_group_inbox(id,group_id,message_id,agent_id,sender_id,message_seq,group_epoch,agent_epoch,member_epoch) SELECT $1,g.id,$2,ga.agent_id,gm.user_id,$3,g.admission_epoch,ga.admission_epoch,gm.admission_epoch FROM platform_web_groups g JOIN platform_web_group_agents ga ON ga.group_id=g.id JOIN platform_web_group_members gm ON gm.group_id=g.id WHERE g.id=$4 AND ga.agent_id=$5 AND gm.user_id=$6`, service.NewID(), out.ID, seq, g, target, a); err != nil {
			return out, false, err
		}
	}
	return out, false, tx.Commit()
}
func webGroupTargets(ctx context.Context, tx *sql.Tx, w, g, a string, r service.WebGroupPost) ([]string, error) {
	mentioned := map[string]bool{}
	for _, id := range r.MentionAgentIDs {
		mentioned[id] = true
	}
	rows, err := tx.QueryContext(ctx, `SELECT ga.agent_id,platform_agent_usage_projection(a.usage_config,'web',NULL,0)->'trigger',platform_web_group_agent_allowed($1,$2,ga.agent_id,$3) FROM platform_web_group_agents ga JOIN platform_agents a ON a.id=ga.agent_id AND a.workspace_id=ga.workspace_id WHERE ga.group_id=$2 AND ga.workspace_id=$1 AND ga.status='active'`, w, g, a)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit close below.
	out := []string{}
	for rows.Next() {
		var id string
		var trigger []byte
		var allowed bool
		if err = rows.Scan(&id, &trigger, &allowed); err != nil {
			return nil, err
		}
		match, e := channelTriggerMatches(trigger, r.Text, mentioned[id])
		if e != nil {
			return nil, e
		}
		delete(mentioned, id)
		if allowed && match {
			out = append(out, id)
		}
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	if len(mentioned) > 0 {
		return nil, service.Fail("invalid_argument", 400, "被提及员工未加入本群")
	}
	return out, nil
}
func (s *Store) WebGroupMessages(ctx context.Context, w, a, g string, after int64, limit int) ([]service.WebGroupMessage, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Read transaction cleanup.
	if err = lockWebGroup(ctx, tx, w, g); err != nil {
		return nil, err
	}
	if err = requireWebGroupMember(ctx, tx, w, g, a); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+webMessageColumns+` FROM manager_web_group_messages WHERE group_id=$1 AND workspace_id=$2 AND seq>$4 AND $3::uuid IS NOT NULL ORDER BY seq LIMIT $5`, g, w, a, after, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit close below.
	out := []service.WebGroupMessage{}
	for rows.Next() {
		v, e := scanWebMessage(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}
