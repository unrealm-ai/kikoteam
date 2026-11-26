package repository

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

// IssueTaskDetailGrantTx binds issuance to the same transaction as the
// outgoing Delivery. It never sends and accepts no caller-supplied scope.
func (s *Store) IssueTaskDetailGrantTx(ctx context.Context, tx *sql.Tx, issue service.TaskDetailIssue) (service.IssuedTaskDetailGrant, error) {
	var issued service.IssuedTaskDetailGrant
	origin, err := s.TaskDetailLinkOriginTx(ctx, tx)
	if err != nil {
		return issued, err
	}
	if origin == "" {
		return issued, service.Fail("task_detail_links_disabled", 409, "当前未启用任务详情链接")
	}
	if !service.ValidID(issue.SessionID) || !service.ValidID(issue.Destination.ConversationID) || (issue.Source == nil) == (issue.TriggerNotificationID == "") {
		return issued, invalidTaskDetailGrant()
	}
	g := service.TaskDetailGrant{ID: service.NewID(), SessionID: issue.SessionID, Scope: "read", TriggerNotificationID: issue.TriggerNotificationID}
	if issue.Source != nil {
		err = s.prepareOrdinaryTaskDetailGrant(ctx, tx, &g, *issue.Source)
	} else {
		err = prepareTriggerTaskDetailGrant(ctx, tx, &g)
	}
	if err != nil {
		return issued, err
	}
	if !matchingTaskDetailDestination(issue.Destination, g.Destination) {
		return issued, invalidTaskDetailGrant()
	}
	var random [32]byte
	if _, err = rand.Read(random[:]); err != nil {
		return issued, err
	}
	digest := sha256.Sum256(random[:])
	d := g.Destination
	err = tx.QueryRowContext(ctx, `WITH tick AS (SELECT clock_timestamp() t)
 INSERT INTO manager_task_detail_grants(id,token_digest,workspace_id,session_id,task_id,source_input_id,source_chat_session_id,trigger_notification_id,destination_conversation_id,destination_channel,destination_binding_id,destination_chat_type,destination_chat_id,destination_topic_id,destination_generation,scope,issued_at,expires_at,full_execution)
 SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,t,t+make_interval(secs=>$17),true FROM tick
 RETURNING expires_at`, g.ID, digest[:], g.WorkspaceID, g.SessionID, g.TaskID, nullID(g.SubjectInputID), nullID(g.SourceChatSessionID),
		nullID(g.TriggerNotificationID), d.ConversationID, d.Channel, nullID(d.BindingID), d.ChatType, d.ChatID, d.TopicID, d.Generation, g.Scope, taskDetailGrantTTL.Seconds()).Scan(&g.ExpiresAt)
	if err != nil {
		return issued, err
	}
	token := base64.RawURLEncoding.EncodeToString(random[:])
	return service.IssuedTaskDetailGrant{Grant: g, Token: token, URL: origin + "/tasks/" + token}, nil
}

func matchingTaskDetailDestination(request, trusted service.TaskDetailDestination) bool {
	if request.ChatType == "direct" {
		request.ChatType = "private"
	}
	return request == trusted
}

func (s *Store) prepareOrdinaryTaskDetailGrant(ctx context.Context, tx *sql.Tx, g *service.TaskDetailGrant, scope service.DelegationScope) error {
	if !service.ValidID(scope.InputID) || scope.ReportOnly && scope.ReportTaskID == "" {
		return service.Fail("user_authorization_required", 403, "只有普通主对话输入可发送任务详情链接")
	}
	target := service.Target{Source: &scope.Identity, SourceInputID: scope.InputID, Workspace: scope.WorkspaceID, Actor: scope.ActorID, Conversation: scope.ConversationID}
	if scope.ReportTaskID == "" {
		if err := s.lockDelegationSource(ctx, tx, target); err != nil {
			return err
		}
	} else if _, err := lockTaskDetailDispatchTarget(ctx, tx, scope, scope.ReportTaskID); err != nil {
		return err
	}
	// Bind the verified inbox to the effective user input or the reported
	// task’s original user input, never to a caller-selected chat or tenant.
	return loadOrdinaryTaskDetailAuthority(ctx, tx, g, scope)
}

const ordinaryTaskDetailAuthority = `SELECT s.workspace_id,s.task_id,c.id,k.source_chat_session_id,orig.id,
 COALESCE(orig.channel_principal_id::text,orig.submitted_by::text,''),COALESCE(orig.channel_generation,0),
 b.provider,b.id,b.connection_generation,c.channel_chat_type,c.external_chat_id,c.channel_thread_id
 FROM manager_sessions s JOIN manager_tasks k ON k.id=s.task_id AND k.workspace_id=s.workspace_id
 JOIN manager_sessions chat ON chat.id=k.source_chat_session_id AND chat.workspace_id=s.workspace_id
 JOIN manager_conversations c ON c.id=chat.conversation_id AND c.workspace_id=s.workspace_id
 JOIN manager_inputs orig ON orig.id=k.source_chat_input_id AND orig.session_id=chat.id AND orig.workspace_id=s.workspace_id
 JOIN manager_turns turn ON turn.id=chat.active_turn_id JOIN manager_inputs root ON root.id=turn.input_id
 JOIN manager_inputs input ON input.id=CASE WHEN root.source_kind='task_report' THEN orig.id ELSE $4::uuid END AND input.session_id=chat.id AND input.workspace_id=s.workspace_id
 JOIN manager_channel_inbox n ON n.input_id=input.id AND n.conversation_id=c.id
 JOIN platform_channel_bindings b ON b.id=c.channel_binding_id AND b.id=n.binding_id AND b.workspace_id=s.workspace_id
 WHERE s.id=$1 AND s.mode='task' AND s.workspace_id=$2 AND chat.id=$3 AND s.conversation_id=c.id
 AND k.planning_state='started' AND s.lifecycle='active' AND c.lifecycle='active' AND chat.lifecycle='active'
 AND root.trigger_agent_loop AND input.source_kind='user' AND input.trigger_agent_loop
 AND ((root.source_kind='user' AND $6='' AND input.channel_principal_id::text=$5)
 OR (root.source_kind='task_report' AND k.id::text=$6 AND root.id=$4 AND input.id=orig.id))
 AND input.channel_principal_id IS NOT NULL AND orig.source_kind='user'
 AND n.status='accepted' AND n.connection_generation=b.connection_generation AND n.principal_id=input.channel_principal_id
 AND n.external_chat_id=c.external_chat_id AND n.thread_id=c.channel_thread_id AND n.chat_type=c.channel_chat_type
 AND b.enabled AND b.identity_epoch=c.channel_identity_epoch AND b.external_tenant_id=c.channel_tenant_id
 AND b.external_tenant_id<>'' AND b.provider IN ('feishu','dingtalk','wecom')
 AND manager_input_actor_allowed(input.id) AND manager_input_actor_allowed(orig.id)
 FOR SHARE OF b`

func loadOrdinaryTaskDetailAuthority(ctx context.Context, tx *sql.Tx, g *service.TaskDetailGrant, scope service.DelegationScope) error {
	d := &g.Destination
	err := tx.QueryRowContext(ctx, ordinaryTaskDetailAuthority, g.SessionID, scope.WorkspaceID, scope.Identity.SessionID, scope.InputID, scope.ActorID, scope.ReportTaskID).
		Scan(&g.WorkspaceID, &g.TaskID, &g.ConversationID, &g.SourceChatSessionID, &g.SubjectInputID, &g.SubjectID,
			&g.SubjectGeneration, &d.Channel, &d.BindingID, &d.Generation, &d.ChatType, &d.ChatID, &d.TopicID)
	if errors.Is(err, sql.ErrNoRows) {
		return invalidTaskDetailGrant()
	}
	if err != nil {
		return err
	}
	d.WorkspaceID, d.ConversationID = g.WorkspaceID, g.ConversationID
	if d.ChatType == "direct" {
		d.ChatType = "private"
		if g.SubjectID == scope.ActorID && g.SubjectGeneration == d.Generation {
			g.Scope = "write"
		}
	}
	if g.SubjectGeneration > 0 {
		g.Channel = &service.ChannelActor{ID: g.SubjectID, Generation: g.SubjectGeneration}
	}
	return nil
}

func prepareTriggerTaskDetailGrant(ctx context.Context, tx *sql.Tx, g *service.TaskDetailGrant) error {
	if !service.ValidID(g.TriggerNotificationID) {
		return invalidTaskDetailGrant()
	}
	d := &g.Destination
	err := tx.QueryRowContext(ctx, `SELECT s.workspace_id,s.task_id,n.conversation_id,COALESCE(n.target->>'provider',''),
 COALESCE(n.target->>'binding_id',''),COALESCE((n.target->>'generation')::bigint,0),
 COALESCE(n.target->>'chat_id',''),COALESCE(n.target->>'thread_id','')
 FROM manager_trigger_notifications n JOIN manager_tasks k ON k.id=n.task_id AND k.trigger_id=n.trigger_id AND k.trigger_version=n.trigger_version
 JOIN manager_sessions s ON s.task_id=n.task_id AND s.workspace_id=k.workspace_id AND s.trigger_id=n.trigger_id
 JOIN manager_conversations c ON c.id=n.conversation_id AND c.workspace_id=s.workspace_id
 JOIN manager_trigger_notification_targets nt ON nt.task_id=k.id AND nt.conversation_id=c.id AND nt.target=n.target
 WHERE n.id=$1 AND s.id=$2 AND s.mode='task' AND n.source_input_id IS NOT NULL AND n.reminder_event_id IS NULL
 AND n.status IN ('pending','leased','sending','sent') AND (c.channel_chat_type='group' OR c.web_group_id IS NOT NULL)
 AND EXISTS(SELECT 1 FROM manager_inputs i WHERE i.id=n.source_input_id AND i.session_id=s.id AND i.workspace_id=s.workspace_id)`,
		g.TriggerNotificationID, g.SessionID).Scan(&g.WorkspaceID, &g.TaskID, &d.ConversationID, &d.Channel, &d.BindingID, &d.Generation, &d.ChatID, &d.TopicID)
	if errors.Is(err, sql.ErrNoRows) {
		return invalidTaskDetailGrant()
	}
	if err != nil {
		return err
	}
	allowed, err := noticeAllowed(ctx, tx, g.TriggerNotificationID)
	if err != nil {
		return err
	}
	if !allowed {
		return invalidTaskDetailGrant()
	}
	d.WorkspaceID, d.ChatType = g.WorkspaceID, "group"
	return nil
}
