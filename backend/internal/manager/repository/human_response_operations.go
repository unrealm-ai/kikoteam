package repository

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

var humanOperationID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// HumanChatSubmission records a proposal or, with no proposal, reads its
// receipt. Operation IDs are scoped to the authenticated inbound Input.
func (s *Store) HumanChatSubmission(ctx context.Context, scope service.HumanRuntimeScope, requestID, operationID string, answer *service.HumanAnswer) (any, error) {
	if !service.ValidID(requestID) || !humanOperationID.MatchString(operationID) {
		return nil, service.Fail("invalid_human_operation_id", 400, "请提供确认单 ID 和有效的提交标识")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck // Commit handles success.
	scope, viewer, err := lockHumanChat(ctx, tx, scope)
	if err != nil {
		return nil, err
	}
	stored, err := loadHumanResponseOperation(ctx, tx, viewer, requestID, operationID)
	if err != nil {
		return nil, err
	}
	if answer == nil {
		if stored.receipt == nil {
			return map[string]any{"id": requestID, "operationId": operationID, "state": "unknown",
				"message": "暂未查到本次提交的回执，请保留原提交标识核对"}, nil
		}
		return stored.result()
	}
	proposal, err := json.Marshal(answer)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(proposal)
	if stored.receipt != nil {
		if !bytes.Equal(stored.digest, digest[:]) {
			return nil, humanOperationConflict()
		}
		return stored.result()
	}
	receipt, err := runHumanResponseOperation(ctx, tx, viewer, scope, requestID, *answer)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO manager_human_response_operations(input_id,operation_id,request_id,proposal_digest,receipt)
 VALUES($1,$2,$3,$4,$5)`, viewer.input, operationID, requestID, digest[:], receipt); err != nil {
		return nil, err
	}
	stored, err = loadHumanResponseOperation(ctx, tx, viewer, requestID, operationID)
	if err != nil {
		return nil, err
	}
	result, err := stored.result()
	if err != nil {
		return nil, err
	}
	return result, tx.Commit()
}

type humanResponseOperation struct {
	id                   string
	digest, receipt      []byte
	resumeID, inputState string
	turnState, rejection string
}

func loadHumanResponseOperation(ctx context.Context, tx *sql.Tx, viewer humanViewer, requestID, operationID string) (humanResponseOperation, error) {
	out := humanResponseOperation{id: operationID}
	var sameRequest bool
	err := tx.QueryRowContext(ctx, `SELECT o.proposal_digest,o.receipt,COALESCE(o.request_id=r.id,true),
 COALESCE(i.id::text,''),COALESCE(i.status,''),COALESCE(t.status,''),COALESCE(i.rejection_reason,'')
 FROM manager_human_requests r
 LEFT JOIN manager_human_response_operations o ON o.input_id=$5::uuid AND o.operation_id=$8
 LEFT JOIN manager_inputs i ON i.id=r.resume_input_id LEFT JOIN manager_turns t ON t.id=i.turn_id
 WHERE r.id=$7 AND `+humanAnswerTarget,
		append(viewer.queryArgs(), requestID, operationID)...).
		Scan(&out.digest, &out.receipt, &sameRequest, &out.resumeID, &out.inputState, &out.turnState, &out.rejection)
	if err != nil {
		return out, visibleError(err)
	}
	if !sameRequest {
		return out, humanOperationConflict()
	}
	return out, nil
}

func humanOperationConflict() error {
	return service.Fail("human_operation_conflict", 409, "提交标识已用于其他答复；原样重试应沿用相同标识和内容")
}

func runHumanResponseOperation(ctx context.Context, tx *sql.Tx, viewer humanViewer, scope service.HumanRuntimeScope, requestID string, answer service.HumanAnswer) ([]byte, error) {
	if _, err := tx.ExecContext(ctx, "SAVEPOINT human_proposal"); err != nil {
		return nil, err
	}
	result, err := proposeHumanReply(ctx, tx, viewer, scope, requestID, answer)
	if err == nil {
		return json.Marshal(result)
	}
	var failure *service.Error
	if !errors.As(err, &failure) || (failure.Status != 400 && failure.Status != 403 && failure.Status != 404 && failure.Status != 409) {
		return nil, err
	}
	if _, rollbackErr := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT human_proposal"); rollbackErr != nil {
		return nil, rollbackErr
	}
	nextAction := "向用户说明答复未记录，并按原因处理"
	switch failure.Code {
	case "human_request_changed":
		nextAction = "重新查询确认单，再根据当前消息判断如何答复"
	case "human_task_scope_changed":
		nextAction = "告知用户原任务范围已变更，需要重新发起确认"
	case "human_request_ambiguous", "human_clarification_invalid":
		nextAction = "根据最新可信上下文向用户澄清"
	}
	return json.Marshal(map[string]string{
		"id": requestID, "state": "not_recorded", "reason": failure.Code, "message": failure.Message, "nextAction": nextAction,
	})
}

func (o humanResponseOperation) result() (map[string]any, error) {
	var result map[string]any
	if err := json.Unmarshal(o.receipt, &result); err != nil {
		return nil, err
	}
	result["operationId"] = o.id
	if o.resumeID != "" && (result["state"] == "approved" || result["state"] == "revised") {
		continuation := map[string]string{"inputId": o.resumeID, "state": o.inputState}
		if o.turnState != "" {
			continuation["turnState"] = o.turnState
		}
		if o.rejection != "" {
			continuation["rejectionReason"] = o.rejection
		}
		result["continuation"] = continuation
	}
	return result, nil
}
