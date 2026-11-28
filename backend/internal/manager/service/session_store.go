package service

import "time"

type Target struct {
	Control                                            bool
	Workspace, Actor, Conversation, Session, DedupLock string
	TaskSpaceID, AgentID                               string
	Source                                             *DelegationIdentity
	SourceInputID                                      string // Pins an immediate delegation to its authorized source Input.
}
type SessionState struct {
	OwnerLifecycle                                                string
	AgentID                                                       string
	TaskSpaceID                                                   *string
	Session                                                       Session
	Conversation                                                  Conversation
	AgentEnabled                                                  bool
	AdmissionEpoch                                                int64
	LastInputSeq, StopInputSeq, StopRevision, AppliedStopRevision int64
}
type Dedup struct{ Kind, Target, Key, Fingerprint, SessionID, InputID string }

// SessionTx is the persistence boundary for one short admission/control
// transaction. Callbacks may be replayed after a PostgreSQL deadlock; they must
// reset captured outputs and perform only transaction-local work, with no
// network, process, file or other non-transactional side effects.
type SessionTx interface {
	State() SessionState
	Configuration(string) (Configuration, error)
	InsertTask(string, *string) error
	InsertSession(Session) error
	AnswerConfirmation(Confirmation) (Input, bool, error)
	InsertInput(Input) (Input, error)
	FindDedup(Dedup) (*Dedup, error)
	RecordDedup(Dedup) error
	SetInitialInput(string) error
	Input(string) (Input, error)
	CancelInput(string, string) (Input, error)
	CancelQueued(string) ([]Input, error)
	RejectUnavailableInputs() ([]Input, error)
	UpdateTitle(*string, int64) error
	Stop(bool) error
	ArchiveConversation() ([]string, error)
	Event(string, *string, *string, map[string]any) error
	SnapshotEvent() error
}

type SessionFilter struct {
	PlanningState, ExecutionState, LatestTurnStatus string
	SourceChat                                      string
	TaskSpace                                       string
	Conversation, Agent, Mode, Lifecycle, BeforeID  string
	BeforeTime                                      time.Time
	Limit                                           int
}
type EventFilter struct {
	SharedDetail bool
	Turn, Type   string
	After, High  int64
	Before       int64
	Backward     bool
	Limit        int
}

func PublicSession(v Session) Session {
	v.Configuration.Instruction = ""
	v.Configuration.BaseURL = ""
	v.Configuration.Protocol = ""
	v.Configuration.ModelID = ""
	v.Configuration.Capabilities = nil
	return v
}
