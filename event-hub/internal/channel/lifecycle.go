package channel

import (
	"context"
	"encoding/json"
	"errors"
	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"net/url"
	"slices"
	"strconv"
	"time"
)

type lifecycleEvent struct {
	BindingID    string   `json:"binding_id"`
	Generation   int64    `json:"connection_generation"`
	Revision     int64    `json:"config_revision"`
	TenantID     string   `json:"external_tenant_id"`
	ChatID       string   `json:"external_chat_id"`
	EventID      string   `json:"provider_event_id"`
	OccurredAtMS int64    `json:"occurred_at_ms"`
	EventType    string   `json:"event_type"`
	MemberIDs    []string `json:"member_ids"`
}

func normalizeLifecycle(b Binding, event any, kind string) (lifecycleEvent, error) {
	v := lifecycleEvent{BindingID: b.ID, Generation: b.Generation, Revision: b.Revision, EventType: kind, MemberIDs: []string{}}
	raw, err := json.Marshal(event)
	if err != nil {
		return v, err
	}
	var envelope struct {
		Header *larkevent.EventHeader `json:"header"`
		Event  *struct {
			ChatID string                   `json:"chat_id"`
			Users  []*larkim.ChatMemberUser `json:"users"`
		} `json:"event"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Header == nil || envelope.Event == nil {
		return v, errors.New("invalid lifecycle envelope")
	}
	h := envelope.Header
	if h.AppID != b.Credentials.AppID || h.TenantKey == "" || (b.TenantID != "" && h.TenantKey != b.TenantID) || h.EventID == "" || envelope.Event.ChatID == "" {
		return v, errors.New("lifecycle identity mismatch")
	}
	v.TenantID = h.TenantKey
	v.ChatID = envelope.Event.ChatID
	v.EventID = h.EventID
	v.OccurredAtMS, err = strconv.ParseInt(h.CreateTime, 10, 64)
	if v.OccurredAtMS > 100000000000000 {
		v.OccurredAtMS /= 1000
	} // Feishu event headers may use microseconds.
	if err != nil || v.OccurredAtMS <= 0 || v.OccurredAtMS > time.Now().Add(5*time.Minute).UnixMilli() {
		return v, errors.New("invalid lifecycle timestamp")
	}
	if kind == "members_added" || kind == "members_removed" {
		for _, u := range envelope.Event.Users {
			if u == nil || u.UserId == nil || value(u.UserId.OpenId) == "" {
				return v, errors.New("invalid member identity")
			}
			v.MemberIDs = append(v.MemberIDs, value(u.UserId.OpenId))
		}
		if len(v.MemberIDs) == 0 {
			return v, errors.New("missing lifecycle members")
		}
		slices.Sort(v.MemberIDs)
		v.MemberIDs = slices.Compact(v.MemberIDs)
	}
	return v, nil
}
func registerLifecycle(ctx context.Context, d *dispatcher.EventDispatcher, b Binding, backend *Backend) {
	send := func(call context.Context, e any, kind string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		v, err := normalizeLifecycle(b, e, kind)
		if err != nil {
			return err
		}
		pin := status(b, "connected", "")
		pin.BotOpenID = b.BotOpenID
		pin.TenantID = v.TenantID
		if err = backend.Applied(call, b.ID, pin); err != nil {
			return err
		}
		var receipt struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}
		if err = backend.request(call, "POST", "/api/internal/v1/channel-lifecycle", v, &receipt); err != nil {
			return err
		}
		if !inboxIDPattern.MatchString(receipt.ID) || (receipt.Status != "accepted" && receipt.Status != "duplicate") {
			return errors.New("invalid durable lifecycle receipt")
		}
		return nil
	}
	d.OnP2ChatMemberBotAddedV1(func(c context.Context, e *larkim.P2ChatMemberBotAddedV1) error { return send(c, e, "bot_added") })
	d.OnP2ChatMemberBotDeletedV1(func(c context.Context, e *larkim.P2ChatMemberBotDeletedV1) error { return send(c, e, "bot_removed") })
	d.OnP2ChatDisbandedV1(func(c context.Context, e *larkim.P2ChatDisbandedV1) error { return send(c, e, "chat_disbanded") })
	d.OnP2ChatMemberUserAddedV1(func(c context.Context, e *larkim.P2ChatMemberUserAddedV1) error { return send(c, e, "members_added") })
	d.OnP2ChatMemberUserDeletedV1(func(c context.Context, e *larkim.P2ChatMemberUserDeletedV1) error {
		return send(c, e, "members_removed")
	})
	d.OnP2ChatMemberUserWithdrawnV1(func(c context.Context, e *larkim.P2ChatMemberUserWithdrawnV1) error {
		return send(c, e, "members_removed")
	})
}

type groupCheck struct {
	ID         string `json:"id"`
	LeaseToken string `json:"lease_token"`
	BindingID  string `json:"binding_id"`
	Generation int64  `json:"connection_generation"`
	Revision   int64  `json:"config_revision"`
	TenantID   string `json:"external_tenant_id"`
	ChatID     string `json:"external_chat_id"`
}
type membershipCheck func(context.Context, Binding, string) (bool, error)

func checkFeishuMembership(ctx context.Context, b Binding, chat string) (bool, error) {
	api := lark.NewClient(b.Credentials.AppID, b.Credentials.AppSecret, lark.WithLogger(quietLogger{}), lark.WithReqTimeout(10*time.Second))
	return checkFeishuMembershipWithClient(ctx, api, chat)
}
func checkFeishuMembershipWithClient(ctx context.Context, api *lark.Client, chat string) (bool, error) {
	resp, err := api.Im.ChatMembers.IsInChat(ctx, larkim.NewIsInChatChatMembersReqBuilder().ChatId(chat).Build())
	if err != nil || resp == nil || !resp.Success() || resp.Data == nil || resp.Data.IsInChat == nil {
		return false, errors.New("membership_verification_unavailable")
	}
	return *resp.Data.IsInChat, nil
}
func groupBinding(ctx context.Context, lookup func(string) (Binding, bool), j groupCheck) (Binding, bool) {
	b, ok := lookup(j.BindingID)
	return b, ctx.Err() == nil && ok && b.Enabled && b.Provider == "feishu" && b.Generation == j.Generation && b.Revision == j.Revision && j.TenantID != "" && b.TenantID == j.TenantID
}
func processGroupCheck(ctx context.Context, backend *Backend, lookup func(string) (Binding, bool), check membershipCheck, j groupCheck) error {
	b, ok := groupBinding(ctx, lookup, j)
	if !ok {
		return ctx.Err()
	}
	present, err := check(ctx, b, j.ChatID)
	var result *bool
	if err == nil {
		result = &present
	}
	if _, ok = groupBinding(ctx, lookup, j); !ok {
		return ctx.Err()
	}
	err = backend.request(ctx, "POST", "/api/internal/v1/channel-groups/"+url.PathEscape(j.ID)+"/complete", map[string]any{"lease_token": j.LeaseToken, "present": result}, nil)
	var rejected *BackendError
	if errors.As(err, &rejected) && rejected.Status == 409 {
		return nil
	}
	return err
}
func RunGroupChecks(ctx context.Context, backend *Backend, lookup func(string) (Binding, bool)) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var batch struct {
			Items []groupCheck `json:"items"`
		}
		if err := backend.request(ctx, "POST", "/api/internal/v1/channel-groups/claim", nil, &batch); err != nil {
			return err
		}
		for _, j := range batch.Items {
			if err := processGroupCheck(ctx, backend, lookup, checkFeishuMembership, j); err != nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
