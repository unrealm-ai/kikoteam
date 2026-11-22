package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

// The caller holds the original Session lock and verified the addressed
// recipient and question revision. A clarification is not a final answer.
func recordHumanClarification(ctx context.Context, tx *sql.Tx, viewer humanViewer, request string, answer service.HumanAnswer, available bool) (any, error) {
	hash := sha256.Sum256([]byte(answer.Text))
	digest := hex.EncodeToString(hash[:])
	var same bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM manager_human_clarifications WHERE request_id=$1 AND question_version=$2 AND body_digest=$3 AND body=$4)`, request, answer.Version, digest, answer.Text).Scan(&same); err != nil {
		return nil, err
	}
	receipt := map[string]string{"id": request, "state": "needs_more"}
	if same {
		return receipt, nil
	}
	if !available {
		return nil, service.Fail("human_request_unavailable", 409, "该确认请求当前无法作答")
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO manager_human_clarifications(request_id,question_version,body_digest,body,source_input_id,source_inbox_id)
 VALUES($1,$2,$3,$4,NULLIF($5,'')::uuid,NULLIF($6,'')::uuid)`, request, answer.Version, digest, answer.Text, viewer.input, viewer.inbox)
	return receipt, err
}
