package repository

import (
	"context"
	"encoding/json"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

// A steer is the head of its own input batch and occurs after the Turn's
// starter. Do not project other batch rows or queued/requeued inputs. The
// subquery uses a bounded lookahead so large reports do not grow without limit.
const turnSupplementJSON = `(SELECT COALESCE(jsonb_agg(jsonb_build_object('input_id',x.id,'input_seq',x.seq::text,'context_state',x.context_state) ORDER BY x.seq),'[]')
	FROM (SELECT a.id,a.seq,a.context_state FROM manager_inputs a WHERE a.workspace_id=i.workspace_id AND a.session_id=i.session_id
	AND a.turn_id=t.id AND a.seq>i.seq AND a.batch_input_id=a.id AND a.delivery_mode='steer'
	AND a.source_kind='user' AND a.trigger_agent_loop ORDER BY a.seq LIMIT 101) x)`

func parseTurnSupplements(raw []byte) (service.TurnSupplements, error) {
	out := service.TurnSupplements{Receipts: []service.SupplementReceipt{}}
	if err := json.Unmarshal(raw, &out.Receipts); err != nil {
		return service.TurnSupplements{}, err
	}
	if len(out.Receipts) > 100 {
		out.Receipts = out.Receipts[:100]
		out.Truncated = true
	}
	return out, nil
}

func (s *Store) DelegatedTurnSupplements(ctx context.Context, scope service.DelegationScope, session, turn string) (service.TurnSupplements, error) {
	var raw []byte
	if err := s.DB.QueryRowContext(ctx, `SELECT `+turnSupplementJSON+sessionFrom+`
	JOIN manager_tasks k ON k.id=s.task_id JOIN manager_turns t ON t.workspace_id=s.workspace_id AND t.session_id=s.id
	JOIN manager_inputs i ON i.workspace_id=t.workspace_id AND i.session_id=t.session_id AND i.id=t.input_id
	WHERE s.workspace_id=$1 AND `+sessionRead(ctx, scope.ActorID)+` AND s.id=$3 AND t.id=$4
	AND s.mode='task' AND s.conversation_id=$5 AND k.source_chat_session_id=$6`,
		scope.WorkspaceID, scope.ActorID, session, turn, scope.ConversationID, scope.Identity.SessionID).Scan(&raw); err != nil {
		return service.TurnSupplements{}, visibleError(err)
	}
	return parseTurnSupplements(raw)
}
