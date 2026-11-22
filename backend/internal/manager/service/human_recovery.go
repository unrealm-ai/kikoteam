package service

type HumanRecoveryItem struct {
	ID          string `json:"id"`
	Version     int64  `json:"version,string"`
	Provider    string `json:"provider"`
	Status      string `json:"status"`
	ErrorCode   string `json:"error_code,omitempty"`
	CanRetry    bool   `json:"can_retry"`
	CanReassign bool   `json:"can_reassign"`
}

type HumanRecoveryListing struct {
	Items      []HumanRecoveryItem `json:"items"`
	Recipients []HumanRecipient    `json:"recipients"`
}

type HumanRecoveryAction struct {
	Version     int64  `json:"version,string"`
	Action      string `json:"action"`
	RecipientID string `json:"recipient_id,omitempty"`
	Reason      string `json:"reason"`
}
