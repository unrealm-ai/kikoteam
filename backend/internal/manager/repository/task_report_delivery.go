package repository

import (
	"context"
	"errors"
	"strings"
)

// A report-started chat Turn has no new inbound Inbox. Its original delegation
// supplies the immutable channel target. User-started Turns use normal replies.
func (s *Store) materializeTaskReplies(ctx context.Context) (retErr error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT n.id,n.binding_id,n.connection_generation,'task_reply:'||t.id::text,b.provider,t.status,COALESCE(t.result_summary,'')
 FROM manager_turns t JOIN manager_inputs root ON root.id=t.input_id JOIN manager_task_reports r ON r.id=root.report_id JOIN manager_sessions src ON src.id=(SELECT session_id FROM manager_inputs WHERE id=r.source_input_id) JOIN manager_tasks k ON k.id=src.task_id JOIN manager_channel_inbox n ON n.input_id=k.source_chat_input_id
 JOIN platform_channel_bindings b ON b.id=n.binding_id
 WHERE root.source_kind='task_report' AND t.status IN ('completed','failed','canceled','interrupted')
 AND NOT EXISTS(SELECT 1 FROM manager_channel_deliveries d WHERE d.inbox_id=n.id AND d.kind='task_reply:'||t.id::text)
 ORDER BY t.finished_at,t.id LIMIT 50`)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, rows.Close()) }()
	type reply struct {
		inbox, binding, kind, provider, status, summary string
		generation                                      int64
	}
	var pending []reply
	for rows.Next() {
		var item reply
		if err := rows.Scan(&item.inbox, &item.binding, &item.generation, &item.kind, &item.provider, &item.status, &item.summary); err != nil {
			return err
		}
		pending = append(pending, item)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range pending {
		text, format := taskReplyContent(item.provider, item.status, item.summary)
		_, err := s.DB.ExecContext(ctx, `INSERT INTO manager_channel_deliveries(id,inbox_id,binding_id,connection_generation,kind,text,content_format)
 VALUES(gen_random_uuid(),$1,$2,$3,$4,$5,$6) ON CONFLICT(inbox_id,kind) DO NOTHING`, item.inbox, item.binding, item.generation, item.kind, text, format)
		if err != nil {
			return err
		}
	}
	return nil
}

func taskReplyContent(provider, status, summary string) (string, string) {
	if status != "completed" {
		return "任务结果已到达，但主会话回复未成功完成，请查看任务详情。", "text"
	}
	if strings.TrimSpace(summary) == "" {
		return "任务本轮已结束，没有可发送的文字回复。", "text"
	}
	return summary, channelReplyFormat(provider, status, summary)
}
