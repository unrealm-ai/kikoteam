package repository

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func taskDetailGrantFixture(t *testing.T, chatType string) (*sql.DB, *managerrepo.Store, manager.TaskDetailIssue) {
	t.Helper()
	db, sessions, store, w, _, _, binding := newTaskDetailGrantEnvironment(t)
	return db, store, makeTaskDetailGrantIssue(t, db, sessions, store, w, binding, chatType)
}

func newTaskDetailGrantEnvironment(t *testing.T) (*sql.DB, *manager.Service, *managerrepo.Store, string, string, string, string) {
	t.Helper()
	db, sessions, store, w, actor, agent, binding := channelManagerFixture(t)
	reviewExec(t, db, `UPDATE platform_accounts SET is_platform_admin=true WHERE id=$1`, actor)
	if _, err := (&TaskLinkSettingsStore{DB: db}).SaveAdmin(t.Context(), actor, 0, "https://tasks.example.test", true); err != nil {
		t.Fatal(err)
	}
	return db, sessions, store, w, actor, agent, binding
}

func makeTaskDetailGrantIssue(t *testing.T, db *sql.DB, sessions *manager.Service, store *managerrepo.Store, w, binding, chatType string) manager.TaskDetailIssue {
	t.Helper()
	ctx := t.Context()
	msg := inboundFixture(binding, "fixture-task-grant-"+chatType, "fixture-user")
	msg.ChatType = chatType
	if chatType == "group" {
		msg.BotMentioned = true
		msg.Text = "help"
		msg.ThreadID = "fixture-thread"
	}
	box, err := store.ReceiveChannel(ctx, msg)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.ProcessChannelInbox(ctx, sessions); err != nil {
		t.Fatal(err)
	}
	var chat, input string
	if err = db.QueryRowContext(ctx, `SELECT session_id,id FROM manager_inputs WHERE id=(SELECT input_id FROM manager_channel_inbox WHERE id=$1)`, box.ID).Scan(&chat, &input); err != nil {
		t.Fatal(err)
	}
	identity := manager.DelegationIdentity{SessionID: chat, EngineID: manager.NewID(), AssignmentID: manager.NewID(), Generation: 1}
	reviewExec(t, db, `UPDATE manager_sessions SET engine_id=$2,assignment_id=$3,generation=1 WHERE id=$1`, chat, identity.EngineID, identity.AssignmentID)
	reviewStartTurn(t, db, w, chat, input, identity.AssignmentID, 1)
	scope, err := store.DelegationSource(ctx, identity)
	if err != nil {
		t.Fatal(err)
	}
	delegate := manager.Delegation{Manager: sessions, Store: store}
	r, err := delegate.Call(ctx, scope, "create_session", manager.DelegationArgs{Instruction: "private fixture instruction"})
	if err != nil {
		t.Fatal(err)
	}
	sessionID, _ := r.(map[string]any)["session_id"].(string)
	return manager.TaskDetailIssue{
		SessionID: sessionID, Source: &scope, Destination: manager.TaskDetailDestination{
			WorkspaceID: w, ConversationID: scope.ConversationID, Channel: "feishu", BindingID: binding,
			ChatType: chatType, ChatID: msg.ExternalChatID, TopicID: msg.ThreadID, Generation: 1,
		},
	}
}

func issueTaskDetailFixture(t *testing.T, db *sql.DB, store *managerrepo.Store, issue manager.TaskDetailIssue) (manager.IssuedTaskDetailGrant, error) {
	t.Helper()
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit owns success.
	grant, err := store.IssueTaskDetailGrantTx(t.Context(), tx, issue)
	if err != nil {
		return grant, err
	}
	return grant, tx.Commit()
}

func TestTaskDetailGrantScopeLifetimeAndProvenance(t *testing.T) {
	db, sessions, store, w, _, _, binding := newTaskDetailGrantEnvironment(t)
	for _, chatType := range []string{"direct", "group"} {
		t.Run(chatType, func(t *testing.T) {
			reviewExec(t, db, `UPDATE platform_task_link_settings SET enabled=true WHERE singleton`)
			issue := makeTaskDetailGrantIssue(t, db, sessions, store, w, binding, chatType)
			ctx := t.Context()
			cross := issue
			cross.Destination.ChatID = "another-chat"
			_, err := issueTaskDetailFixture(t, db, store, cross)
			assertChannelError(t, err, "task_detail_link_unavailable")
			cross = issue
			cross.Destination.ConversationID = manager.NewID()
			_, err = issueTaskDetailFixture(t, db, store, cross)
			assertChannelError(t, err, "task_detail_link_unavailable")

			first, err := issueTaskDetailFixture(t, db, store, issue)
			if err != nil {
				t.Fatal(err)
			}
			second, err := issueTaskDetailFixture(t, db, store, issue)
			if err != nil {
				t.Fatal(err)
			}
			if first.Grant.ID == second.Grant.ID || first.Token == second.Token || len(first.Token) != 43 ||
				!strings.HasPrefix(first.URL, "https://tasks.example.test/tasks/") || strings.Contains(first.URL, issue.SessionID) {
				t.Fatal("grant was not independent and opaque")
			}
			var exact bool
			if err = db.QueryRow(`SELECT expires_at-issued_at=interval '72 hours' AND octet_length(token_digest)=32
 FROM manager_task_detail_grants WHERE id=$1`, first.Grant.ID).Scan(&exact); err != nil || !exact {
				t.Fatalf("token storage or TTL: %t %v", exact, err)
			}
			g, err := store.ResolveTaskDetailGrant(ctx, first.Token)
			if err != nil {
				t.Fatal(err)
			}
			if g.SessionID != issue.SessionID || g.Destination.ChatID != issue.Destination.ChatID || g.SubjectID != issue.Source.ActorID ||
				g.SubjectInputID != issue.Source.InputID || g.CanWrite() != (chatType == "direct") {
				t.Fatalf("unexpected grant fields: %+v", g)
			}
			_, err = store.RecheckTaskDetailGrant(ctx, g.ID, true)
			if chatType == "group" {
				assertChannelError(t, err, "task_detail_link_read_only")
			} else if err != nil {
				t.Fatal(err)
			}

			reviewExec(t, db, `UPDATE platform_task_link_settings SET enabled=false WHERE singleton`)
			_, err = issueTaskDetailFixture(t, db, store, issue)
			assertChannelError(t, err, "task_detail_links_disabled")
			if _, err = store.ResolveTaskDetailGrant(ctx, first.Token); err != nil {
				t.Fatalf("settings disable revoked an existing capability: %v", err)
			}
			if err = store.RevokeTaskDetailGrant(ctx, manager.NewID(), g.ID); err != nil {
				t.Fatal(err)
			}
			if _, err = store.ResolveTaskDetailGrant(ctx, first.Token); err != nil {
				t.Fatalf("another workspace revoked the link: %v", err)
			}
			if err = store.RevokeTaskDetailGrant(ctx, issue.Destination.WorkspaceID, g.ID); err != nil {
				t.Fatal(err)
			}
			_, err = store.ResolveTaskDetailGrant(ctx, first.Token)
			assertChannelError(t, err, "task_detail_link_unavailable")
			if _, err = store.ResolveTaskDetailGrant(ctx, second.Token); err != nil {
				t.Fatalf("unrelated capability was revoked: %v", err)
			}
			reviewExec(t, db, `UPDATE manager_task_detail_grants SET issued_at=$2,expires_at=$3 WHERE id=$1`,
				second.Grant.ID, time.Now().Add(-96*time.Hour), time.Now().Add(-24*time.Hour))
			_, err = store.ResolveTaskDetailGrant(ctx, second.Token)
			assertChannelError(t, err, "task_detail_link_unavailable")
			reviewExec(t, db, `UPDATE platform_task_link_settings SET enabled=true WHERE singleton`)
			third, err := issueTaskDetailFixture(t, db, store, issue)
			if err != nil {
				t.Fatal(err)
			}
			reviewExec(t, db, `UPDATE platform_channel_bindings SET connection_generation=2 WHERE id=$1`, binding)
			_, err = store.ResolveTaskDetailGrant(ctx, third.Token)
			assertChannelError(t, err, "task_detail_link_unavailable")
			reviewExec(t, db, `UPDATE platform_channel_bindings SET connection_generation=1 WHERE id=$1`, binding)
		})
	}
}

func TestTaskDetailTriggerGrantRequiresExactPublishedTarget(t *testing.T) {
	db, store, w, actor, _, conv, input := triggerNoticeFixture(t)
	ctx := t.Context()
	reviewExec(t, db, `UPDATE platform_accounts SET is_platform_admin=true WHERE id=$1`, actor)
	if _, err := (&TaskLinkSettingsStore{DB: db}).SaveAdmin(ctx, actor, 0, "https://tasks.example.test", true); err != nil {
		t.Fatal(err)
	}
	endNoticeExecution(t, db, input)
	if err := store.MaterializeTriggerNotifications(ctx); err != nil {
		t.Fatal(err)
	}
	issue := manager.TaskDetailIssue{Destination: manager.TaskDetailDestination{
		WorkspaceID: w, ConversationID: conv, Channel: "web", ChatType: "group",
	}}
	if err := db.QueryRow(`SELECT s.id,n.id FROM manager_inputs i JOIN manager_sessions s ON s.id=i.session_id
 JOIN manager_trigger_notifications n ON n.source_input_id=i.id WHERE i.id=$1`, input).
		Scan(&issue.SessionID, &issue.TriggerNotificationID); err != nil {
		t.Fatal(err)
	}
	wrong := issue
	wrong.Destination.ConversationID = manager.NewID()
	_, err := issueTaskDetailFixture(t, db, store, wrong)
	assertChannelError(t, err, "task_detail_link_unavailable")
	created, err := issueTaskDetailFixture(t, db, store, issue)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := store.ResolveTaskDetailGrant(ctx, created.Token)
	if err != nil {
		t.Fatal(err)
	}
	if grant.CanWrite() || grant.TriggerNotificationID != issue.TriggerNotificationID || grant.Destination.ConversationID != conv || grant.SubjectID != "" {
		t.Fatalf("unexpected notification grant: %+v", grant)
	}
	_, err = store.RecheckTaskDetailGrant(ctx, grant.ID, true)
	assertChannelError(t, err, "task_detail_link_read_only")
	reviewExec(t, db, `UPDATE manager_trigger_notifications SET status='unknown' WHERE id=$1`, issue.TriggerNotificationID)
	_, err = store.ResolveTaskDetailGrant(ctx, created.Token)
	assertChannelError(t, err, "task_detail_link_unavailable")
}
