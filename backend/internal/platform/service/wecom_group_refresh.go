package service

import "context"

type WecomGroupRefreshStatus struct {
	ID        string `json:"job_id"`
	Status    string `json:"status"`
	ErrorCode string `json:"error_code,omitempty"`
}
type WecomGroupRefreshJob struct {
	ID            string `json:"id"`
	LeaseToken    string `json:"lease_token"`
	BindingID     string `json:"binding_id"`
	Generation    int64  `json:"connection_generation"`
	Revision      int64  `json:"config_revision"`
	IdentityEpoch int64  `json:"identity_epoch"`
	TenantID      string `json:"external_tenant_id"`
}
type WecomGroupRefreshItem struct {
	ID   string `json:"chat_id"`
	Name string `json:"chat_name"`
}
type WecomGroupRefreshCompletion struct {
	LeaseToken string                  `json:"lease_token"`
	Status     string                  `json:"status"`
	Items      []WecomGroupRefreshItem `json:"items"`
}
type WecomGroupRefreshStore interface {
	RequestWecomGroups(context.Context, ChannelBinding, string) (WecomGroupRefreshStatus, error)
	ReadWecomGroups(context.Context, ChannelBinding, string, string) (WecomGroupRefreshStatus, error)
	ClaimWecomGroups(context.Context) ([]WecomGroupRefreshJob, error)
	AuthorizeWecomGroups(context.Context, string, string) (WecomGroupRefreshJob, error)
	CompleteWecomGroups(context.Context, string, WecomGroupRefreshCompletion) error
}
type WecomGroupRefresh struct {
	Channels *ChannelService
	Store    WecomGroupRefreshStore
}

func (s *WecomGroupRefresh) Run(ctx context.Context, w, actor, agent, id string) (WecomGroupRefreshStatus, error) {
	var out WecomGroupRefreshStatus
	b, e := s.Channels.GetProvider(ctx, w, actor, agent, "wecom")
	if e != nil {
		return out, e
	}
	if !b.Enabled || !b.IdentityConfirmed || b.TenantID == "" || b.IdentityVerifiedGeneration != b.Generation {
		return out, Fail("channel_identity_unconfirmed", 409, "企微连接身份尚未确认")
	}
	if id != "" {
		if !agentUUID(id) {
			return out, Fail("invalid_argument", 400, "刷新任务身份无效")
		}
		return s.Store.ReadWecomGroups(ctx, b, actor, id)
	}
	return s.Store.RequestWecomGroups(ctx, b, actor)
}

func ValidWecomRefreshID(id string) bool { return agentUUID(id) }
