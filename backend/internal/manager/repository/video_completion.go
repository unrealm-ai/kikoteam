package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func taskVideoStatuses(ctx context.Context, tx *sql.Tx, id string) ([]service.TaskVideoStatus, error) {
	rows, e := tx.QueryContext(ctx, "SELECT id,status,seconds FROM platform_video_generations WHERE task_id=$1 ORDER BY started_at DESC,id DESC LIMIT 20", id)
	if e != nil {
		return nil, e
	}
	defer rows.Close() //nolint:errcheck // Iteration errors checked below.
	var out []service.TaskVideoStatus
	for rows.Next() {
		var v service.TaskVideoStatus
		if e = rows.Scan(&v.ID, &v.Status, &v.Seconds); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// NotifyVideo appends a single result to the original task, in the same transaction
// that marks the generation notified. Its authority follows the original input.
func NotifyVideo(ctx context.Context, tx *sql.Tx, id, status string) error {
	var workspace, session, conversation, source string
	err := tx.QueryRowContext(ctx, `SELECT s.workspace_id,s.id,coalesce(s.conversation_id::text,''),i.id
 FROM platform_video_generations g JOIN manager_inputs i ON i.id=g.input_id JOIN manager_sessions s ON s.id=g.session_id JOIN platform_agents a ON a.id=s.agent_id
 LEFT JOIN manager_conversations c ON c.id=s.conversation_id LEFT JOIN manager_task_spaces p ON p.id=s.task_space_id
 WHERE g.id=$1 AND s.lifecycle='active' AND coalesce(c.lifecycle,p.lifecycle)='active' AND s.stop_revision=g.stop_revision
 AND s.stop_revision=s.applied_stop_revision AND a.enabled AND a.admission_epoch=i.admission_epoch AND manager_input_actor_allowed(i.id)
 AND NOT EXISTS(SELECT FROM manager_human_requests h WHERE h.id=i.human_request_id AND h.state='revised')
 AND i.work_assignment_id IS NULL AND i.notification_id IS NULL FOR UPDATE OF s`, id).Scan(&workspace, &session, &conversation, &source)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	msg := "原请求 " + id + " 的视频生成失败，未扣积分。请回报结果。"
	if status == "ready" {
		msg = "视频生成已完成。已保存固定原片 outputs/videos/" + id + ".mp4 。请按原任务要求继续剪辑或调用 artifact_publish 发布该路径，并回报成片。"
	}
	var seq int64
	if err = tx.QueryRowContext(ctx, "UPDATE manager_sessions SET last_input_seq=last_input_seq+1 WHERE id=$1 RETURNING last_input_seq", session).Scan(&seq); err != nil {
		return err
	}
	in := service.NewID()
	_, err = tx.ExecContext(ctx, `INSERT INTO manager_inputs(id,workspace_id,session_id,submitted_by,channel_principal_id,channel_generation,trigger_event_id,admission_epoch,web_group_member_epoch,delegated_from_input_id,seq,text,source_kind,delivery_mode,trigger_agent_loop,context_state,video_generation_id)
 SELECT $2,workspace_id,session_id,submitted_by,channel_principal_id,channel_generation,trigger_event_id,admission_epoch,web_group_member_epoch,delegated_from_input_id,$3,$4,'system','queue',true,'pending',$5 FROM manager_inputs WHERE id=$1`, source, in, seq, msg, id)
	if err != nil {
		return err
	}
	st := sessionTx{ctx: ctx, tx: tx, target: service.Target{Workspace: workspace, Session: session, Conversation: conversation}}
	v, err := st.Input(in)
	if err != nil {
		return err
	}
	return st.Event("input.updated", &in, nil, service.InputFields(v))
}
