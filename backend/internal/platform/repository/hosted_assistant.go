package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type HostedAssistantStore struct {
	DB *sql.DB
}

// ProvisionNext retries technical failures without leaving partially created employees or packages.
func (s *HostedAssistantStore) ProvisionNext(ctx context.Context) (bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback() //nolint:errcheck // Commit owns success.
	var w string
	err = tx.QueryRowContext(ctx, `SELECT workspace_id FROM platform_assistant_provisioning WHERE status='pending'
 AND next_attempt_at<=now() ORDER BY next_attempt_at,workspace_id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&w)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, "SAVEPOINT assistant_attempt"); err != nil {
		return false, err
	}
	status, err := s.provision(ctx, tx, w)
	if err != nil {
		if _, restoreErr := tx.ExecContext(ctx, "ROLLBACK TO assistant_attempt"); restoreErr != nil {
			return false, errors.Join(err, restoreErr)
		}
		if _, retryErr := tx.ExecContext(ctx, `UPDATE platform_assistant_provisioning SET attempts=attempts+1,
 status=CASE WHEN attempts+1>=5 THEN 'failed' ELSE 'pending' END,
 next_attempt_at=now()+(15*(attempts+1)*interval '1 second') WHERE workspace_id=$1`, w); retryErr != nil {
			return false, errors.Join(err, retryErr)
		}
		return false, errors.Join(err, tx.Commit())
	}
	if _, err = tx.ExecContext(ctx, "UPDATE platform_assistant_provisioning SET status=$2 WHERE workspace_id=$1", w, status); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (s *HostedAssistantStore) provision(ctx context.Context, tx *sql.Tx, w string) (string, error) {
	var existing, owner string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(assistant_agent_id::text,'') FROM platform_workspaces
 WHERE id=$1 FOR UPDATE`, w).Scan(&existing); err != nil || existing != "" {
		return "provisioned", err
	}
	err := tx.QueryRowContext(ctx, `SELECT user_id FROM platform_members
 WHERE workspace_id=$1 AND role='owner' AND status='active' FOR SHARE`, w).Scan(&owner)
	if err != nil {
		return "", err
	}
	m := &modelTx{tx: tx, workspace: w}
	at := &agentTx{model: m}
	a := service.Agent{ID: service.NewID(), Name: "KIKO 助手", Role: "体验 AI 员工工作方式，帮你配置自己的 AI 员工。",
		Instruction: "你是 KIKO 助手。根据用户需要推进任务；样例工具不代表真实业务操作。", IsAssistant: true,
		Enabled: true, Version: 1, ConfigVersion: 1, AdmissionEpoch: 1}
	if err = at.Create(ctx, a, owner); err != nil {
		return "", err
	}
	if err = (&packageTx{model: m}).Save(ctx, service.NewAssistantDemoPackage(a.ID), true); err != nil {
		return "", err
	}

	_, err = tx.ExecContext(ctx, "UPDATE platform_workspaces SET assistant_agent_id=$2 WHERE id=$1", w, a.ID)
	return "provisioned", err
}

func (s *HostedAssistantStore) Entry(ctx context.Context, workspace, actor string) (service.AssistantEntry, error) {
	out := service.AssistantEntry{Cards: []service.AssistantCard{}}
	err := s.DB.QueryRowContext(ctx, `SELECT a.id FROM platform_workspaces w
 JOIN platform_agents a ON a.workspace_id=w.id AND a.id=w.assistant_agent_id AND a.enabled
 WHERE w.id=$1 AND platform_agent_web_allowed(w.id,a.id,$2)`, workspace, actor).Scan(&out.AgentID)
	if errors.Is(err, sql.ErrNoRows) {
		err = s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM platform_assistant_provisioning p
 JOIN platform_members m ON m.workspace_id=p.workspace_id AND m.user_id=$2 AND m.status='active'
 WHERE p.workspace_id=$1 AND p.status='pending')`, workspace, actor).Scan(&out.Pending)
		return out, err
	}
	if err != nil {
		return out, err
	}
	ok, err := s.ToolAllowed(ctx, workspace, actor, out.AgentID, "demo", nil)
	if ok {
		out.Cards = service.AssistantCards()
		out.Experienced, err = s.experienced(ctx, actor)
	}
	return out, err
}

func (s *HostedAssistantStore) ToolAllowed(ctx context.Context, w, actor, agent, key string, snapshot *service.ExecutionCapabilities) (bool, error) {
	var allowed bool
	if key != "demo" && key != "platform" {
		return false, nil
	}
	err := (&ModelStore{DB: s.DB}).WithWorkspace(ctx, w, func(raw service.ModelTx) error {
		m := raw.(*modelTx)
		var id string
		err := m.tx.QueryRowContext(ctx, `SELECT p.id FROM platform_capability_packages p
 JOIN platform_workspaces w ON w.id=p.workspace_id AND w.assistant_agent_id=p.agent_id
 JOIN platform_agents a ON a.id=p.agent_id AND a.enabled
 WHERE p.workspace_id=$1 AND p.agent_id=$2 AND p.assistant_tool_set=$3 AND p.enabled`, w, agent, key).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err = authorizeSnapshotSource(ctx, m.tx, w, actor, agent); err != nil {
			return err
		}
		t := &packageTx{model: m}
		if snapshot != nil {
			found := false
			for _, old := range snapshot.Packages {
				if old.ID == id && old.AssistantToolSet == key {
					found = true
					break
				}
			}
			if !found {
				return nil
			}
		}
		p, err := t.Get(ctx, agent, id)
		if err != nil {
			return err
		}
		allowed, err = t.PackageScopeAllows(ctx, p, actor)
		return err
	})
	return allowed, err
}

// Read a successful experience from existing task/report facts, not from a mutable demo fixture.
func (s *HostedAssistantStore) experienced(ctx context.Context, actor string) (bool, error) {
	var done bool
	err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(
 SELECT 1 FROM manager_inputs i
 JOIN manager_sessions chat ON chat.id=i.session_id AND chat.mode='chat'
 JOIN manager_conversations c ON c.id=chat.conversation_id AND c.web_user_id=i.submitted_by
 JOIN platform_workspaces w ON w.id=chat.workspace_id AND w.assistant_agent_id=chat.agent_id
 JOIN manager_tasks t ON t.source_chat_input_id=i.id
 JOIN manager_sessions run ON run.task_id=t.id AND run.mode='task'
 JOIN manager_turns turn ON turn.session_id=run.id AND turn.status='completed'
 JOIN manager_task_reports r ON r.source_turn_id=turn.id AND r.target_session_id=chat.id AND r.status='admitted'
 WHERE i.submitted_by=$1 AND i.source_kind='user'
 AND (i.text LIKE '%MEET-301%' OR i.text LIKE '%INC-207%' OR (i.text LIKE '%ORD-1042%' AND i.text LIKE '%ORD-1087%'))
 AND EXISTS (SELECT 1 FROM manager_session_events e WHERE e.session_id=run.id
   AND e.event->>'type'='agent.tool_result' AND starts_with(e.event->>'title','mcp__assistant_demo__')
   AND e.event->'error'='null'::jsonb)
 GROUP BY i.id,i.text HAVING count(DISTINCT t.id)>=CASE WHEN i.text LIKE '%ORD-1042%' AND i.text LIKE '%ORD-1087%' THEN 2 ELSE 1 END
 )`, actor).Scan(&done)
	return done, err
}
