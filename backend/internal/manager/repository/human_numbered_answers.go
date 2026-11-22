package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func numberedHumanAnswer(ctx context.Context, tx *sql.Tx, viewer humanViewer, id, kind string, count int, answer service.HumanAnswer, open bool) (service.HumanAnswer, map[string]any, error) {
	if answer.Decision != "approve" && answer.Decision != "information" {
		return answer, nil, nil
	}
	if answer.Decision == "approve" && (kind != "decision" || answer.Text != "") {
		return answer, nil, service.Fail("invalid_human_answer", 400, "请先按小题编号补齐答案，再单独发送「同意 编号」。")
	}
	if answer.Decision == "information" {
		values, ok := service.ParseNumberedHumanAnswers(answer.Text, count)
		if !ok {
			return answer, nil, service.Fail("human_numbered_answers_required", 400, "请标明具体小题，例如「答复 编号 第一个 500 元；第二个 周五」。")
		}
		if err := saveNumberedHumanAnswers(ctx, tx, viewer, id, answer.Version, values, open); err != nil {
			return answer, nil, err
		}
	}
	body, remaining, err := readNumberedHumanAnswers(ctx, tx, id, answer.Version, count)
	if err != nil {
		return answer, nil, err
	}
	if len(remaining) != 0 && answer.Decision == "approve" {
		return answer, nil, service.Fail("human_numbered_answers_required", 409, "仍有必答小题未回答，未记录同意。")
	}
	if answer.Decision == "information" && (kind == "decision" || len(remaining) != 0) {
		if !open {
			return answer, nil, service.Fail("human_request_unavailable", 409, "该确认已经结束。")
		}
		if len(remaining) == 0 {
			return answer, explicitHumanDecisionRequired(id), nil
		}
		return answer, map[string]any{"id": id, "state": "pending", "remaining": remaining, "requires_approval": kind == "decision"}, nil
	}
	answer.Text = body
	return answer, nil, nil
}

func saveNumberedHumanAnswers(ctx context.Context, tx *sql.Tx, viewer humanViewer, id string, version int64, values map[int]string, open bool) error {
	for ordinal, value := range values {
		if open {
			if _, err := tx.ExecContext(ctx, `INSERT INTO manager_human_numbered_answers(request_id,question_version,ordinal,body,source_input_id,source_inbox_id)
 VALUES($1,$2,$3,$4,NULLIF($5,'')::uuid,NULLIF($6,'')::uuid) ON CONFLICT DO NOTHING`, id, version, ordinal, value, viewer.input, viewer.inbox); err != nil {
				return err
			}
		}
		var prior string
		err := tx.QueryRowContext(ctx, `SELECT body FROM manager_human_numbered_answers WHERE request_id=$1 AND question_version=$2 AND ordinal=$3`, id, version, ordinal).Scan(&prior)
		if errors.Is(err, sql.ErrNoRows) || err == nil && prior != value {
			return service.Fail("human_numbered_answer_conflict", 409, "该小题已有不同答复，或请求已结束；没有覆盖已有答复。")
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func readNumberedHumanAnswers(ctx context.Context, tx *sql.Tx, id string, version int64, count int) (string, []int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT ordinal,body FROM manager_human_numbered_answers WHERE request_id=$1 AND question_version=$2 ORDER BY ordinal`, id, version)
	if err != nil {
		return "", nil, err
	}
	defer rows.Close() //nolint:errcheck // Read failures are reported by Scan and Err.
	values := make(map[int]string, count)
	for rows.Next() {
		var ordinal int
		var value string
		if err = rows.Scan(&ordinal, &value); err != nil {
			return "", nil, err
		}
		values[ordinal] = value
	}
	if err = rows.Err(); err != nil {
		return "", nil, err
	}
	remaining := []int{}
	var body strings.Builder
	for n := 1; n <= count; n++ {
		if values[n] == "" {
			remaining = append(remaining, n)
			continue
		}
		fmt.Fprintf(&body, "%d. %s\n", n, values[n])
	}
	return strings.TrimSpace(body.String()), remaining, nil
}
