package repository

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Project a transient recovery reason on reads. Stop, archive and input
// reconciliation operate on the durable execution facts.
const publicBlockReason = `COALESCE(s.block_reason, CASE WHEN EXISTS (
	SELECT 1 FROM manager_engines e WHERE e.id=s.engine_id AND e.last_seen_at<=now()-interval '15 seconds'
) OR (s.mode='task' AND s.engine_id IS NULL AND EXISTS (
	SELECT 1 FROM manager_inputs i WHERE i.session_id=s.id AND i.status='queued' AND i.trigger_agent_loop
) AND EXISTS (
	SELECT 1 FROM manager_conversation_sandboxes b JOIN manager_engines e ON e.id=b.manager_engine_id
	WHERE (b.conversation_id=s.conversation_id OR b.task_id=s.task_id) AND
	(NOT e.accepting OR e.last_seen_at<=now()-interval '15 seconds')
)) THEN 'engine_recovery_pending' END)`

// Engine IDs are unique per boot. The file is created and locked before
// registration, inherited by runtime processes, and never replaced or unlinked.
// Opening a missing file would manufacture evidence for a pre-upgrade Engine.
func (s *Store) engineStopped(id string) (bool, error) {
	if s.RuntimeRoot == "" {
		return false, nil
	}
	f, err := os.OpenFile(filepath.Join(s.RuntimeRoot, "state", "engines", "instances", id+".lock"), os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	closeErr := f.Close()
	if errors.Is(err, unix.EWOULDBLOCK) {
		return false, closeErr
	}
	return err == nil, errors.Join(err, closeErr)
}

func (t *sessionTx) needsAssignment() (bool, error) {
	var current *string
	if err := t.tx.QueryRowContext(t.ctx, `SELECT engine_id FROM manager_sessions WHERE id=$1`, t.target.Session).Scan(&current); err != nil {
		return false, err
	}
	if current != nil {
		return t.store.engineStopped(*current)
	}
	session := t.state.Session
	if !t.state.AgentEnabled || t.state.OwnerLifecycle != "active" || session.Lifecycle != "active" || session.Execution.QueuedInputCount == 0 {
		return false, nil
	}
	var runnable bool
	err := t.tx.QueryRowContext(t.ctx, `SELECT EXISTS(SELECT 1 FROM manager_inputs WHERE session_id=$1 AND status='queued' AND trigger_agent_loop AND (work_submission_id IS NULL OR manager_work_review_ready(session_id)) AND ($2 OR confirmation_request_id IS NOT NULL OR human_request_id IS NOT NULL OR work_submission_id IS NOT NULL))`, session.ID, len(session.Execution.RequiredActions) == 0).Scan(&runnable)
	return runnable, err
}

// Lock the sandbox before selecting its next manager. A healthy manager keeps
// its placement even at capacity. A completed release also fences idle legacy
// Runners: no Pi/tool window remains and opening another requires a current
// assignment and a fresh scope from Backend.
func (t *sessionTx) dispatchSandbox() (string, *string, error) {
	if t.state.Session.Mode != "task" {
		return "", nil, nil
	}
	var box, manager string
	var available, handedOff bool
	err := t.tx.QueryRowContext(t.ctx, `SELECT b.id,b.manager_engine_id,
		e.accepting AND e.last_seen_at>now()-interval '15 seconds',
		NOT e.accepting AND b.state IN ('preparing','stopped') AND NOT EXISTS (
			SELECT 1 FROM manager_sessions s WHERE s.mode='task' AND `+sandboxMembers+` AND `+sandboxUnsettled+`)
		FROM manager_conversation_sandboxes b JOIN manager_engines e ON e.id=b.manager_engine_id
		WHERE b.conversation_id=$1 OR b.task_id=$2 FOR UPDATE OF b`,
		nullID(t.target.Conversation), standaloneTaskID(t.state.Session)).Scan(&box, &manager, &available, &handedOff)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, nil
	}
	if err != nil || available {
		return box, &manager, err
	}
	if handedOff {
		return box, nil, nil
	}
	stopped, err := t.store.engineStopped(manager)
	if stopped {
		return box, nil, err
	}
	return box, &manager, err
}
