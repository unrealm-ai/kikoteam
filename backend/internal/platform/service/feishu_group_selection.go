package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

type feishuGroupProof struct {
	Workspace, Agent, Actor, Binding, Tenant string
	Epoch, Generation, Revision, Expires     int64
	Item                                     ChannelGroupItem
}

func feishuSelection(b ChannelBinding, id, name, group string) GroupSelection {
	if name == "" {
		name = group
	}
	return GroupSelection{
		Ref: SelectionRef{Kind: "group", Provider: "feishu", WorkspaceID: b.WorkspaceID, AgentID: b.AgentID, ID: id,
			BindingID: b.ID, TenantID: b.TenantID, Namespace: "chat_id", IdentityEpoch: b.IdentityEpoch,
			Generation: b.Generation, ConfigRevision: b.Revision, ExternalID: group},
		Provider: "feishu", Name: name, Evidence: "provider_verified", Coverage: "all_joined", Status: "available", Selectable: true,
	}
}

func (s *ChannelGroupDiscovery) groupProofMAC(payload string) []byte {
	mac := hmac.New(sha256.New, s.Secret)
	_, _ = mac.Write([]byte("feishu-group-selection:" + payload))
	return mac.Sum(nil)
}

func (s *ChannelGroupDiscovery) signGroupProof(b ChannelBinding, actor string, item ChannelGroupItem) (string, error) {
	raw, err := json.Marshal(feishuGroupProof{b.WorkspaceID, b.AgentID, actor, b.ID, b.TenantID, b.IdentityEpoch, b.Generation, b.Revision, time.Now().Add(5 * time.Minute).Unix(), item})
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	return payload + "." + base64.RawURLEncoding.EncodeToString(s.groupProofMAC(payload)), nil
}

// Select materializes only the explicitly chosen group for existing save paths.
// The live list does not publish a database snapshot.
func (s *ChannelGroupDiscovery) Select(ctx context.Context, w, actor, agent, token string) (DiscoveredChannelGroupPage, error) {
	out := DiscoveredChannelGroupPage{Items: []DiscoveredChannelGroup{}, Source: "provider"}
	bad := Fail("selection_unavailable", 409, "飞书群选择已过期或来源无效，请刷新")
	if s == nil || s.Channels == nil || s.Store == nil || len(s.Secret) < 32 || len(token) > 4096 {
		return out, bad
	}
	payload, sig, ok := strings.Cut(token, ".")
	signed, e := base64.RawURLEncoding.DecodeString(sig)
	if !ok || e != nil || !hmac.Equal(signed, s.groupProofMAC(payload)) {
		return out, bad
	}
	raw, e := base64.RawURLEncoding.DecodeString(payload)
	var p feishuGroupProof
	if e != nil || json.Unmarshal(raw, &p) != nil || p.Workspace != w || p.Agent != agent || p.Actor != actor || p.Expires <= time.Now().Unix() {
		return out, bad
	}
	b, e := s.Channels.GetProvider(ctx, w, actor, agent, "feishu")
	if e != nil {
		return out, e
	}
	if !b.Enabled || p.Binding != b.ID || p.Tenant != b.TenantID || p.Epoch != b.IdentityEpoch || p.Generation != b.Generation || p.Revision != b.Revision {
		return out, bad
	}
	published, e := s.Store.PublishGroups(ctx, b, actor, ChannelGroupPage{Items: []ChannelGroupItem{p.Item}})
	if e != nil {
		return out, e
	}
	if len(published.Items) != 1 {
		return out, bad
	}
	g := published.Items[0]
	pick := feishuSelection(b, g.ID, g.Name, p.Item.ID)
	g.Selection = &pick
	out.Items = append(out.Items, g)
	return out, nil
}
