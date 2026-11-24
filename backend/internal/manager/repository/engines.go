package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	platform "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func (s *Store) RegisterEngine(ctx context.Context, v service.Engine) error {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO manager_engines(id,host_id,endpoint,capacity,accepting) VALUES($1,$2,$3,$4,true) ON CONFLICT(id) DO UPDATE SET id=EXCLUDED.id WHERE manager_engines.host_id=EXCLUDED.host_id AND manager_engines.endpoint=EXCLUDED.endpoint AND manager_engines.capacity=EXCLUDED.capacity`, v.ID, v.HostID, v.Endpoint, v.Capacity)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n != 1 {
		return service.Fail("engine_identity_conflict", 409, "Engine 身份已用于其他实例")
	}
	return err
}
func (s *Store) HeartbeatEngine(ctx context.Context, id string, seq int64, accepting bool) error {
	var exists bool
	err := s.DB.QueryRowContext(ctx, `WITH updated AS (UPDATE manager_engines SET heartbeat_seq=$2,last_seen_at=now(),accepting=$3 WHERE id=$1 AND heartbeat_seq<$2 RETURNING id) SELECT EXISTS(SELECT 1 FROM manager_engines WHERE id=$1)`, id, seq, accepting).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return service.Fail("resource_not_found", 404, "Engine 尚未注册")
	}
	return nil
}

type dispatchPosition struct {
	created time.Time
	id      string
}

// Dispatch may run in every Backend: parent/Session and Engine row locks make
// each reservation atomic. At this stage the deployment has one local pool.
func (s *Store) Dispatch(ctx context.Context) error {
	// Allocation is a workspace control-plane action; the Input actor is
	// separately revalidated by Claim. A revoked Session creator must not
	// prevent dispatch/rejection of later accepted inputs from other members.
	cursor := s.dispatchCursor.Load()
	var afterTime, afterID any
	if cursor != nil {
		afterTime, afterID = cursor.created, cursor.id
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT s.workspace_id,
		CASE WHEN c.channel_binding_id IS NOT NULL THEN c.channel_created_by WHEN p.id IS NOT NULL THEN p.created_by ELSE m.user_id END,
		s.id,s.created_at FROM manager_sessions s LEFT JOIN manager_conversations c ON c.id=s.conversation_id
		LEFT JOIN manager_task_spaces p ON p.id=s.task_space_id LEFT JOIN platform_members m
		ON m.workspace_id=s.workspace_id AND m.role='owner' AND m.status='active'
		LEFT JOIN manager_engines old ON old.id=s.engine_id
		WHERE (c.channel_binding_id IS NOT NULL OR p.id IS NOT NULL OR m.user_id IS NOT NULL)
		AND (old.last_seen_at<=now()-interval '15 seconds' OR (s.lifecycle='active' AND COALESCE(c.lifecycle,p.lifecycle)='active'
		AND ((`+expiredConfirmation+`) OR (s.engine_id IS NULL AND EXISTS (
			SELECT 1 FROM manager_inputs i WHERE i.session_id=s.id AND i.status='queued' AND i.trigger_agent_loop AND
			(i.work_submission_id IS NULL OR manager_work_review_ready(i.session_id)) AND (s.required_actions='[]' OR i.confirmation_request_id IS NOT NULL OR i.human_request_id IS NOT NULL OR i.work_submission_id IS NOT NULL))))))
		ORDER BY (s.created_at,s.id)<=($1::timestamptz,$2::uuid),s.created_at,s.id LIMIT 100`, afterTime, afterID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Explicit close below releases rows before allocation transactions.
	type candidate struct {
		target service.Target
		order  dispatchPosition
	}
	var targets []candidate
	for rows.Next() {
		var t candidate
		if err = rows.Scan(&t.target.Workspace, &t.target.Actor, &t.target.Session, &t.order.created); err != nil {
			if e := rows.Close(); e != nil {
				return errors.Join(err, e)
			}
			return err
		}
		t.order.id = t.target.Session
		targets = append(targets, t)
	}
	if err = rows.Err(); err != nil {
		if e := rows.Close(); e != nil {
			return errors.Join(err, e)
		}
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, item := range targets {
		// A page of unresolved environments must not starve later inputs.
		s.dispatchCursor.Store(&item.order)
		target := item.target
		err = s.withDispatchSession(ctx, target, func(value service.SessionTx) error {
			t := value.(*sessionTx)
			if err := t.expireConfirmations(); err != nil {
				return err
			}
			if err := service.ReconcileQueuedInputs(value); err != nil {
				return err
			}
			needed, err := t.needsAssignment()
			if err != nil || !needed {
				return err
			}
			box, manager, err := t.dispatchSandbox()
			if err != nil {
				return err
			}
			var engine, engineHost string
			err = t.tx.QueryRowContext(ctx, `SELECT e.id,e.host_id FROM manager_engines e WHERE e.accepting AND e.last_seen_at>now()-interval '15 seconds' AND ($1::uuid IS NULL OR e.id=$1) AND (SELECT count(*) FROM manager_sessions s WHERE s.engine_id=e.id)<e.capacity ORDER BY (SELECT count(*) FROM manager_sessions s WHERE s.engine_id=e.id)::float/e.capacity,e.id FOR UPDATE OF e SKIP LOCKED LIMIT 1`, manager).Scan(&engine, &engineHost)
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			// The lock is held, but a scalar count in the selecting statement
			// may precede a competing commit. Recheck in a fresh statement.
			var used, capacity int
			if err = t.tx.QueryRowContext(ctx, `SELECT capacity,(SELECT count(*) FROM manager_sessions WHERE engine_id=$1) FROM manager_engines WHERE id=$1`, engine).Scan(&capacity, &used); err != nil {
				return err
			}
			if used >= capacity {
				return nil
			}
			if box != "" && manager == nil {
				_, err = t.tx.ExecContext(ctx, `UPDATE manager_conversation_sandboxes SET manager_engine_id=$2,host_id=$3,state='preparing' WHERE id=$1`, box, engine, engineHost)
				if err != nil {
					return err
				}
			} else if t.state.Session.Mode == "task" && box == "" {
				if _, err = t.tx.ExecContext(ctx, `INSERT INTO manager_conversation_sandboxes(id,workspace_id,conversation_id,manager_engine_id,host_id,task_id) VALUES($1,$2,$3,$4,$5,$6)`, service.NewID(), target.Workspace, nullID(t.target.Conversation), engine, engineHost, standaloneTaskID(t.state.Session)); err != nil {
					return err
				}
			}
			_, err = t.tx.ExecContext(ctx, `UPDATE manager_sessions SET engine_id=$2,assignment_id=$3,generation=generation+1,native_host_id=$4 WHERE id=$1`, target.Session, engine, service.NewID(), engineHost)
			return err
		})
		if err != nil {
			var e *service.Error
			if errors.As(err, &e) && e.Status == 404 {
				continue
			}
			return err
		}
	}
	return nil
}
func (s *Store) Assignments(ctx context.Context, engine string) ([]service.Assignment, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,assignment_id,generation FROM manager_sessions WHERE engine_id=$1 ORDER BY created_at`, engine)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Iteration errors are checked below.
	items := []service.Assignment{}
	for rows.Next() {
		var v service.Assignment
		if err = rows.Scan(&v.SessionID, &v.AssignmentID, &v.Generation); err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	return items, rows.Err()
}
func (s *Store) HarnessConfig(ctx context.Context, engine, session, assignment string, generation int64, input string) (service.HarnessConfig, string, error) {
	var cfg service.HarnessConfig
	var file service.WebhookFile
	var data []byte
	var actor string
	cfg.InputID = input
	err := s.DB.QueryRowContext(ctx, `SELECT s.id,s.workspace_id,s.agent_id,COALESCE(s.conversation_id::text,''),COALESCE(s.task_space_id::text,''),COALESCE(s.task_id::text,''),s.mode,s.configuration,COALESCE(i.submitted_by::text,i.channel_principal_id::text,''),COALESCE(s.trigger_id::text,''),COALESCE(s.trigger_version,0),COALESCE(i.trigger_event_id::text,''),i.channel_principal_id,COALESCE(i.channel_generation,0),c.web_group_id IS NOT NULL,i.source_kind='task_report',
 s.mode='chat' AND c.channel_binding_id IS NOT NULL,
 s.mode='chat' AND COALESCE(c.web_user_id IS NOT NULL OR (c.channel_chat_type='direct' AND c.channel_thread_id='' AND c.channel_created_by IS NOT NULL),false),
 s.mode='chat' AND `+chatIsGroup+`,
 CASE WHEN s.mode='task' THEN EXISTS(SELECT 1 FROM platform_agent_human_recipients h WHERE h.agent_id=s.agent_id AND h.recipients<>'[]')
 ELSE COALESCE(`+privateHumanInput+` AND (`+webHumanRecipientConfigured+` OR `+channelHumanRecipientConfigured+`),false) END,
 EXISTS(SELECT FROM manager_human_chat_inputs h WHERE h.input_id=i.id),
 CASE WHEN s.mode='chat' THEN a.name ELSE COALESCE(av.name,'') END,
 CASE WHEN s.mode='chat' THEN a.role ELSE COALESCE(av.role,'') END,
 CASE WHEN s.mode='chat' THEN a.instruction ELSE '' END,
 s.mode='task' AND EXISTS(SELECT 1 FROM manager_tasks k JOIN manager_task_plan_operations o
 ON o.workspace_id=k.workspace_id AND o.source_session_id=k.source_chat_session_id AND o.task_id=k.id AND o.action='start_todo'
 WHERE k.id=s.task_id AND k.planning_state='started' AND k.planned_instruction IS NOT NULL),
 COALESCE(wf.event_id::text,''),COALESCE(we.body_hash,''),wf.body
 FROM manager_sessions s JOIN manager_inputs i ON i.session_id=s.id LEFT JOIN manager_conversations c ON c.id=s.conversation_id
 LEFT JOIN manager_task_spaces p ON p.id=s.task_space_id JOIN platform_agents a ON a.id=s.agent_id
 LEFT JOIN manager_trigger_events we ON we.id=i.trigger_event_id AND we.input_id=i.id AND we.session_id=s.id
 LEFT JOIN manager_webhook_payloads wf ON wf.event_id=we.id AND wf.as_file
 LEFT JOIN platform_agent_versions av ON s.mode='task' AND av.workspace_id=s.workspace_id AND av.agent_id=s.agent_id AND av.version=(s.configuration->>'agent_version')::bigint
 WHERE (manager_input_actor_allowed(i.id) OR manager_human_chat_input_allowed(i.id)) AND s.engine_id=$1 AND s.id=$2 AND s.assignment_id=$3 AND s.generation=$4 AND i.id=$5 AND i.status='started' AND i.turn_id=s.active_turn_id AND s.stop_revision=s.applied_stop_revision AND COALESCE(c.lifecycle,p.lifecycle)='active' AND s.lifecycle='active' AND a.enabled AND i.admission_epoch=a.admission_epoch`, engine, session, assignment, generation, input).
		Scan(&cfg.SessionID, &cfg.WorkspaceID, &cfg.AgentID, &cfg.ConversationID, &cfg.TaskSpaceID, &cfg.TaskID, &cfg.Mode, &data, &actor, &cfg.TriggerID, &cfg.TriggerVersion, &cfg.TriggerEventID, &cfg.ChannelPrincipalID, &cfg.ChannelGeneration, &cfg.WebGroup, &cfg.ReportOnly, &cfg.ChatCatalogChannel, &cfg.ChatCatalogPrivate, &cfg.ChatCatalogGroup, &cfg.HumanTools, &cfg.ConfirmationOnly, &cfg.Employee.Name, &cfg.Employee.Role, &cfg.Employee.Instruction, &cfg.TodoStarted, &file.EventID, &file.SHA256, &file.Body)
	if err != nil {
		return cfg, "", visibleError(err)
	}
	err = json.Unmarshal(data, &cfg.Configuration)
	if file.EventID != "" {
		cfg.WebhookFile = &file
	}
	if err == nil && cfg.Mode == "task" && cfg.Configuration.ModelProfileID != "" {
		err = s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM platform_model_profile_versions WHERE workspace_id=$1 AND profile_id=$2 AND version=$3 AND vision_status='passed')`, cfg.WorkspaceID, cfg.Configuration.ModelProfileID, cfg.Configuration.ModelVersion).Scan(&cfg.Configuration.Vision)
	}
	if err == nil {
		err = s.workHarness(ctx, &cfg)
	}
	if err == nil && cfg.Mode == "task" && s.SnapshotCapabilities != nil {
		err = s.turnCapabilities(cfg.ExecutionContext(ctx), &cfg, actor)
	}
	cfg.Resources = platform.RuntimeResources{Skills: []platform.RuntimeSkill{}, Plugins: []platform.RuntimePlugin{}, Problems: []string{}}
	return cfg, actor, err
}
func (s *Store) Release(ctx context.Context, engine, session, assignment string, generation int64) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE manager_sessions SET engine_id=NULL,assignment_id=NULL WHERE id=$1 AND engine_id=$2 AND assignment_id=$3 AND generation=$4 AND active_turn_id IS NULL AND COALESCE(background_process_count,1)=0 AND block_reason IS NULL AND stop_revision=applied_stop_revision`, session, engine, assignment, generation)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return service.Fail("assignment_unresolved", 409, "执行仍需收尾，不能释放")
	}
	return err
}

// withDispatchSession is a repository-private control-plane path, never exposed
// through caller-controlled Target fields. Allocation does not grant the
// workspace owner read/control access to private chats; Input actors are
// revalidated before assignment and by Engine Claim.
func (s *Store) withDispatchSession(ctx context.Context, target service.Target, fn func(service.SessionTx) error) error {
	return retrySessionTransaction(ctx, func() error { return s.withSessionAttempt(ctx, target, fn, true) })
}

// A Task ID alone is not an owner discriminator: Conversation tasks still share
// their Conversation sandbox. Only a real TaskSpace task owns its environment.
func standaloneTaskID(s service.Session) *string {
	if s.TaskSpaceID == nil {
		return nil
	}
	return s.TaskID
}

func (s *Store) turnCapabilities(ctx context.Context, cfg *service.HarnessConfig, actor string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // Best effort after commit or the original error.
	var raw []byte
	var turn string
	err = tx.QueryRowContext(ctx, `SELECT t.id,t.capability_snapshot FROM manager_turns t JOIN manager_inputs i ON i.id=t.input_id
WHERE i.id=$1 AND i.session_id=$2 AND t.status='running' FOR UPDATE OF t`, cfg.InputID, cfg.SessionID).Scan(&turn, &raw)
	if err != nil {
		return visibleError(err)
	}
	var caps platform.ExecutionCapabilities
	if len(raw) == 0 {
		// Serialize the initial read of all package pins with employee upgrades.
		if _, err = tx.ExecContext(ctx, `SELECT id FROM platform_workspaces WHERE id=$1 FOR SHARE`, cfg.WorkspaceID); err != nil {
			return err
		}
		caps, err = s.SnapshotCapabilities(ctx, tx, cfg.WorkspaceID, actor, cfg.AgentID)
		if err != nil {
			return err
		}
		raw, err = json.Marshal(caps)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE manager_turns SET capability_snapshot=$2 WHERE id=$1`, turn, raw); err != nil {
			return err
		}
	} else if err = json.Unmarshal(raw, &caps); err != nil {
		return err
	}
	cfg.Configuration.Capabilities = &caps
	cfg.Configuration.Skills, cfg.Configuration.Plugins = nil, nil
	for _, p := range caps.Packages {
		for _, r := range p.Skills {
			if !slices.Contains(cfg.Configuration.Skills, r.SkillID) {
				cfg.Configuration.Skills = append(cfg.Configuration.Skills, r.SkillID)
			}
		}
		for _, r := range p.Plugins {
			if !slices.Contains(cfg.Configuration.Plugins, r.PluginID) {
				cfg.Configuration.Plugins = append(cfg.Configuration.Plugins, r.PluginID)
			}
		}
	}
	return tx.Commit()
}
