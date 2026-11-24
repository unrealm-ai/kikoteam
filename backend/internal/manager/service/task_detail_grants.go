package service

import "time"

// TaskDetailDestination is assembled server-side from already authorized chat
// routing, not from the model, bearer page, or request host.
type TaskDetailDestination struct {
	WorkspaceID, ConversationID, Channel, BindingID, ChatType, ChatID, TopicID string
	Generation                                                                 int64
}

type TaskDetailGrant struct {
	ID, WorkspaceID, TaskID, SessionID, ConversationID string
	SourceChatSessionID, SubjectID, SubjectInputID     string
	TriggerNotificationID                              string
	SubjectGeneration                                  int64
	Channel                                            *ChannelActor
	Scope                                              string
	Destination                                        TaskDetailDestination
	ExpiresAt                                          time.Time
}

func (g TaskDetailGrant) CanWrite() bool { return g.Scope == "write" }

// TaskDetailIssue names exactly one trusted issuance authority. The store
// checks it against the Session owner and the current destination.
type TaskDetailIssue struct {
	SessionID             string
	Destination           TaskDetailDestination
	Source                *DelegationScope
	TriggerNotificationID string
}

// IssuedTaskDetailGrant is internal-only. Neither credential nor URL may be
// returned to the model or written to logs.
type IssuedTaskDetailGrant struct {
	Grant TaskDetailGrant
	Token string
	URL   string
}
