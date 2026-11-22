package service

import "context"

func (s *Service) AnswerConfirmation(ctx context.Context, workspace, actor, session, key string, answer Confirmation) (Input, bool, error) {
	if !ValidID(answer.RequestID) || answer.Confirmed == nil {
		return Input{}, false, Fail("invalid_argument", 400, "请提供确认请求 ID 和是或否")
	}
	if !validKey(key) {
		return Input{}, false, Fail("idempotency_key_required", 400, "提交答复需要有效去重键")
	}
	fp, err := fingerprint(answer)
	if err != nil {
		return Input{}, false, err
	}
	d := Dedup{Kind: "session_input", Target: session, Key: key, Fingerprint: fp, SessionID: session}
	var out Input
	var replay bool
	err = s.Store.WithSession(ctx, Target{Workspace: workspace, Actor: actor, Session: session, DedupLock: d.Kind + ":" + session + ":" + key}, func(tx SessionTx) error {
		if err := admitted(tx.State()); err != nil {
			return err
		}
		found, err := tx.FindDedup(d)
		if err != nil {
			return err
		}
		if found != nil {
			if found.Fingerprint != fp {
				return Fail("idempotency_conflict", 409, "去重键已用于其他内容")
			}
			out, err = tx.Input(found.InputID)
			replay = true
			return err
		}
		out, replay, err = tx.AnswerConfirmation(answer)
		if err != nil {
			return err
		}
		d.InputID = out.ID
		if err = tx.RecordDedup(d); err != nil || replay {
			return err
		}
		if err = tx.Event("user.message", &out.ID, nil, InputFields(out)); err != nil {
			return err
		}
		if err = emitInput(tx, out); err != nil {
			return err
		}
		return tx.SnapshotEvent()
	})
	return out, replay, err
}
