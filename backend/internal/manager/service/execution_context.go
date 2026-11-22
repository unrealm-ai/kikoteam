package service

import (
	"context"
	platform "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func (v HarnessConfig) ExecutionContext(ctx context.Context) context.Context {
	if v.WorkAssignmentID != "" {
		return platform.WithWorkExecution(ctx, platform.WorkExecution{AssignmentID: v.WorkAssignmentID, SourceAgentID: v.WorkSourceAgentID, InputID: v.InputID})
	}
	if v.TriggerID != "" {
		return platform.WithTriggerExecution(ctx, platform.TriggerExecution{TriggerID: v.TriggerID, Version: v.TriggerVersion, EventID: v.TriggerEventID, InputID: v.InputID})
	}
	if v.TaskSpaceID != "" {
		return platform.WithTaskSpaceExecution(ctx, platform.TaskSpaceExecution{TaskSpaceID: v.TaskSpaceID, InputID: v.InputID})
	}
	if v.WebGroup {
		return platform.WithWebGroupExecution(ctx, platform.WebGroupExecution{ConversationID: v.ConversationID, InputID: v.InputID})
	}
	if v.ChannelPrincipalID == nil {
		return platform.WithWebEmployee(ctx, v.AgentID)
	}
	return platform.WithChannelExecution(ctx, platform.ChannelExecution{PrincipalID: *v.ChannelPrincipalID, Generation: v.ChannelGeneration, ConversationID: v.ConversationID, InputID: v.InputID, ConfirmationOnly: v.ConfirmationOnly})
}
