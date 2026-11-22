package repository

import (
	"context"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"strconv"
)

func sessionRead(ctx context.Context, actor string) string {
	return sessionReadWithPlanning(sessionReadBase(ctx, actor))
}

func sessionReadWithPlanning(base string) string {
	return " (" + base + ") AND (NOT EXISTS(SELECT 1 FROM manager_tasks pk WHERE pk.id=s.task_id AND pk.planning_state<>'started') OR COALESCE(s.created_by,s.channel_created_by)=$2::uuid) "
}
func sessionReadBase(ctx context.Context, actor string) string {
	if g, ok := service.WebGroupExecutionFrom(ctx); ok {
		return " c.web_group_id IS NOT NULL AND c.id='" + g.ConversationID + "'::uuid AND manager_web_group_actor_allowed(c.id,$2) "
	}
	a, ok := service.ChannelActorFrom(ctx)
	if !ok {
		return memberRead
	}
	if a.ID != actor || a.Generation < 1 {
		return " false "
	}
	// Generation is a server-owned integer, not interpolated request text.
	return " manager_channel_actor_allowed($2,c.id," + strconv.FormatInt(a.Generation, 10) + ") "
}
