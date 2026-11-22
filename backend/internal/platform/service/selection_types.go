package service

// SelectionRef is a typed, source-scoped identity, never an authorization grant.
type SelectionRef struct {
	Kind           string `json:"kind"`
	Provider       string `json:"provider"`
	WorkspaceID    string `json:"workspace_id"`
	AgentID        string `json:"agent_id"`
	ID             string `json:"id,omitempty"`
	BindingID      string `json:"binding_id,omitempty"`
	TenantID       string `json:"tenant_id,omitempty"`
	Namespace      string `json:"identity_namespace,omitempty"`
	IdentityEpoch  int64  `json:"identity_epoch,omitempty"`
	Generation     int64  `json:"connection_generation,omitempty"`
	ConfigRevision int64  `json:"config_revision,omitempty"`
	ExternalID     string `json:"external_id"`
	GroupEpoch     int64  `json:"group_epoch,omitempty"`
	AgentEpoch     int64  `json:"agent_epoch,omitempty"`
}
