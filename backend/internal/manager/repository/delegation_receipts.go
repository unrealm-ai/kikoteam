package repository

import (
	"context"
	"database/sql"
	"errors"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func (s *Store) DelegatedInputReceipt(ctx context.Context, scope service.DelegationScope, session, id string) (service.Input, error) {
	if _, err := s.DelegatedSession(ctx, scope, session); err != nil {
		return service.Input{}, err
	}
	if id != "" && !service.ValidID(id) {
		return service.Input{}, service.Fail("invalid_argument", 400, "输入标识无效")
	}
	var state *string
	row := s.DB.QueryRowContext(ctx, `SELECT `+inputColumns+`,context_state FROM manager_inputs WHERE session_id=$1 AND ($2='' OR id::text=$2) ORDER BY seq DESC LIMIT 1`, session, id)
	v, err := scanInput(appendedScanner{scanner: row, extra: []any{&state}})
	if err != nil {
		var e *service.Error
		if errors.As(err, &e) && e.Status == 404 && id == "" {
			return service.Input{}, nil
		}
		if errors.Is(err, sql.ErrNoRows) && id == "" {
			return service.Input{}, nil
		}
		return v, err
	}
	delivery := "pending"
	if state != nil {
		switch *state {
		case "committed":
			delivery = "received"
		case "unknown":
			delivery = "unknown"
		case "absent":
			delivery = "not_received"
		}
	}
	if (v.Status == "canceled" || v.Status == "rejected") && (state == nil || *state == "absent") {
		delivery = "not_delivered"
	}
	v.Receipt = &service.InputReceipt{Recorded: true, Delivery: delivery, Application: "unconfirmed", ContextState: state, InputRevision: v.Seq}
	return v, nil
}
