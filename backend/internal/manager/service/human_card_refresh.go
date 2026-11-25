package service

type HumanCardRefresh struct {
	ID             string                 `json:"id"`
	LeaseToken     string                 `json:"lease_token"`
	BindingID      string                 `json:"binding_id"`
	Provider       string                 `json:"provider"`
	TenantID       string                 `json:"tenant_id"`
	Generation     int64                  `json:"connection_generation"`
	Revision       int64                  `json:"config_revision"`
	ProviderCardID string                 `json:"provider_card_id"`
	State          string                 `json:"state"`
	RequestVersion int64                  `json:"request_version"`
	Card           *HumanConfirmationCard `json:"card"`
}
