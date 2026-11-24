package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

// optionalTriggerTaskDetailURL never replaces the ordinary result body. A
// failed optional grant must not abort the existing notification delivery.
func (s *Store) optionalTriggerTaskDetailURL(ctx context.Context, tx *sql.Tx, d service.ChannelDelivery) (string, error) {
	var workspace, session, conversation string
	err := tx.QueryRowContext(ctx, `SELECT k.workspace_id, s.id,n.conversation_id
 FROM manager_trigger_notifications n JOIN manager_tasks k ON k.id=n.task_id
 JOIN manager_sessions s ON s.task_id=k.id AND s.mode='task' AND s.trigger_id=n.trigger_id
 JOIN manager_turns t ON t.id=n.source_turn_id AND t.session_id=s.id
 WHERE n.id=$1 AND n.result_status='completed' AND t.status='completed' AND n.reminder_event_id IS NULL`, d.ID).Scan(&workspace, &session, &conversation)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, "SAVEPOINT optional_trigger_task_detail"); err != nil {
		return "", err
	}
	issue, err := s.IssueTaskDetailGrantTx(ctx, tx, service.TaskDetailIssue{SessionID: session, TriggerNotificationID: d.ID,
		Destination: service.TaskDetailDestination{WorkspaceID: workspace, ConversationID: conversation, Channel: d.Provider, BindingID: d.BindingID,
			ChatType: d.ChatType, ChatID: d.ChatID, TopicID: d.ThreadID, Generation: d.ConnectionGeneration}})
	if err != nil {
		if _, recovery := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT optional_trigger_task_detail"); recovery != nil {
			return "", recovery
		}
	}
	_, releaseErr := tx.ExecContext(ctx, "RELEASE SAVEPOINT optional_trigger_task_detail")
	if releaseErr != nil {
		return "", releaseErr
	}
	if err != nil {
		return "", nil
	}
	// No raw payload is persisted for triggers: an authorized lease transitions
	// directly to sending, and after that it is never eligible for another send.
	return issue.URL, nil
}
