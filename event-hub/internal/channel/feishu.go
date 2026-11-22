package channel

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/unrealm-ai/kikoteam/event-hub/internal/feishucontent"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

// SDK logging is suppressed: raw protocol messages and bootstrap URLs may contain secrets.
type quietLogger struct{}

func (quietLogger) Debug(context.Context, ...interface{}) {}
func (quietLogger) Info(context.Context, ...interface{})  {}
func (quietLogger) Warn(context.Context, ...interface{})  {}
func (quietLogger) Error(context.Context, ...interface{}) {}

type Mention struct {
	ID        string `json:"id"`
	Key       string `json:"key"`
	Name      string `json:"name,omitempty"`
	IDType    string `json:"id_type,omitempty"`
	TenantKey string `json:"tenant_key,omitempty"`
}
type MediaReference struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Locator string `json:"locator"`
}
type MediaJob struct {
	MediaReference
	InboxID    string `json:"inbox_id"`
	Ordinal    int    `json:"ordinal"`
	Lease      string `json:"lease_token"`
	BindingID  string `json:"binding_id"`
	Generation int64  `json:"connection_generation"`
	Provider   string `json:"provider"`
	MessageID  string `json:"message_id"`
}
type Inbound struct {
	Media            []MediaReference `json:"media,omitempty"`
	LogProvider      string           `json:"-"`
	LogCallbackID    string           `json:"-"`
	SenderName       string           `json:"sender_name,omitempty"`
	ControlEligible  bool             `json:"control_eligible,omitempty"`
	ChatName         string           `json:"chat_name,omitempty"`
	BotID            string           `json:"bot_id,omitempty"`
	ProviderDirected bool             `json:"provider_directed,omitempty"`
	SenderTenant     string           `json:"sender_tenant,omitempty"`
	SenderIDType     string           `json:"sender_id_type,omitempty"`
	BindingID        string           `json:"binding_id"`
	Generation       int64            `json:"connection_generation"`
	Revision         int64            `json:"config_revision"`
	RulesVersion     int64            `json:"rules_version"`
	BotMentioned     bool             `json:"bot_mentioned"`
	EventID          string           `json:"provider_event_id"`
	MessageID        string           `json:"provider_message_id"`
	TenantID         string           `json:"external_tenant_id"`
	ChatID           string           `json:"external_chat_id"`
	ChatType         string           `json:"chat_type"`
	SenderID         string           `json:"sender_id"`
	Text             string           `json:"text"`
	ReplyMessageID   string           `json:"reply_message_id,omitempty"`
	QuotedText       string           `json:"quoted_text,omitempty"`
	MessageType      string           `json:"message_type"`
	ThreadID         string           `json:"thread_id,omitempty"`
	CreatedAtMS      int64            `json:"created_at_ms"`
	Mentions         []Mention        `json:"mentions"`
}

func value(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
func normalize(b Binding, f *Filter, e *larkim.P2MessageReceiveV1) (Inbound, bool, error) {
	v := Inbound{LogProvider: "feishu", BindingID: b.ID, Generation: b.Generation, Revision: b.Revision, RulesVersion: b.RulesVersion, Mentions: []Mention{}}
	if e == nil || e.EventV2Base == nil || e.EventV2Base.Header == nil || e.Event == nil || e.Event.Message == nil || e.Event.Sender == nil || e.Event.Sender.SenderId == nil {
		return v, false, errors.New("invalid feishu event")
	}
	if e.EventV2Base.Header.AppID != b.Credentials.AppID || e.EventV2Base.Header.TenantKey == "" || (b.TenantID != "" && e.EventV2Base.Header.TenantKey != b.TenantID) {
		return v, false, errors.New("feishu identity mismatch")
	}
	m, s := e.Event.Message, e.Event.Sender
	v.EventID = e.EventV2Base.Header.EventID
	v.TenantID = e.EventV2Base.Header.TenantKey
	v.MessageID = value(m.MessageId)
	v.ChatID = value(m.ChatId)
	v.ChatType = value(m.ChatType)
	v.SenderID = value(s.SenderId.OpenId)
	v.SenderTenant = value(s.TenantKey)
	v.SenderIDType = "open_id"
	v.MessageType = value(m.MessageType)
	v.ThreadID = value(m.ThreadId)
	if v.ChatType == "p2p" && v.ThreadID == "" {
		v.ReplyMessageID = value(m.ParentId)
	}
	if value(m.CreateTime) != "" {
		var err error
		v.CreatedAtMS, err = strconv.ParseInt(value(m.CreateTime), 10, 64)
		if err != nil || v.CreatedAtMS < 0 {
			return v, false, errors.New("invalid feishu message timestamp")
		}
	}
	if v.EventID == "" || v.MessageID == "" || v.ChatID == "" || v.SenderID == "" {
		return v, false, errors.New("missing feishu identity")
	}
	if value(s.SenderType) != "user" {
		return v, false, nil
	}

	mentioned := false
	for _, m := range m.Mentions {
		if m != nil && m.Id != nil {
			id := value(m.Id.OpenId)
			v.Mentions = append(v.Mentions, Mention{ID: id, Key: value(m.Key), Name: value(m.Name), IDType: "open_id", TenantKey: value(m.TenantKey)})
			if b.BotOpenID != "" && id == b.BotOpenID {
				mentioned = true
			}
		}
	}
	if v.MessageType == "text" || v.MessageType == "post" {
		var err error
		v.Text, err = feishucontent.RenderWithMentions(v.MessageType, value(m.Content), feishuMentionTokens(v.Mentions, v.ChatType == "group"))
		if err != nil {
			return v, false, err
		}
	}
	v.Media = feishuMedia(v.MessageType, value(m.Content))
	v.BotMentioned = mentioned
	match, err := f.Match(v.ChatType, v.Text, mentioned)
	return v, match, err
}

// Feishu runs one binding generation. Start joins SDK workers before returning.
func Feishu(ctx context.Context, b Binding, backend *Backend) error {
	filter, err := Compile(b.Trigger)
	if err != nil {
		return err
	}
	api := lark.NewClient(b.Credentials.AppID, b.Credentials.AppSecret, lark.WithLogger(quietLogger{}), lark.WithReqTimeout(10*time.Second))
	resp, err := api.Get(ctx, "/open-apis/bot/v3/info", nil, larkcore.AccessTokenTypeTenant)
	if err != nil {
		return errors.New("feishu identity lookup failed")
	}
	var info struct {
		Code int `json:"code"`
		Bot  struct {
			OpenID string `json:"open_id"`
		} `json:"bot"`
	}
	if json.Unmarshal(resp.RawBody, &info) != nil || info.Code != 0 || info.Bot.OpenID == "" {
		return errors.New("feishu bot identity unavailable")
	}
	if b.BotOpenID != "" && b.BotOpenID != info.Bot.OpenID {
		return errors.New("feishu bot identity mismatch")
	}
	b.BotOpenID = info.Bot.OpenID
	applied := status(b, "applied", "")
	applied.BotOpenID = b.BotOpenID
	if err = backend.Applied(ctx, b.ID, applied); err != nil {
		return err
	}
	handler := dispatcher.NewEventDispatcher("", "").OnP2MessageReceiveV1(func(call context.Context, e *larkim.P2MessageReceiveV1) error {
		incoming := Inbound{BindingID: b.ID, Generation: b.Generation}
		if e != nil {
			if e.EventV2Base != nil && e.EventV2Base.Header != nil {
				incoming.EventID = e.EventV2Base.Header.EventID
			}
			if e.Event != nil && e.Event.Message != nil {
				incoming.MessageID = value(e.Event.Message.MessageId)
				if created, parseErr := strconv.ParseInt(value(e.Event.Message.CreateTime), 10, 64); parseErr == nil {
					incoming.CreatedAtMS = created
				}
			}
		}
		backend.traceInbound(call, "channel.callback.received", "feishu", incoming, "received")
		outcome := "failed"
		defer func() { backend.traceInbound(call, "channel.callback.result", "feishu", incoming, outcome) }()

		if err := ctx.Err(); err != nil {
			return err
		}
		v, match, err := normalize(b, filter, e)
		if err != nil {
			return err
		}
		if !match {
			outcome = "filtered"
			return nil
		}
		if v.ChatType == "p2p" {
			v.ChatType = "direct"
		}
		pin := status(b, "connected", "")
		pin.BotOpenID = b.BotOpenID
		pin.TenantID = v.TenantID
		backend.traceInbound(call, "channel.callback.identity_check", "feishu", incoming, "started")
		if err = backend.Applied(call, b.ID, pin); err != nil {
			outcome = "identity_check_failed"
			return err
		}
		err = backend.Inbound(call, v)
		if err == nil {
			outcome = "durably_received"
		}
		return err
	})
	registerLifecycle(ctx, handler, b, backend)
	var client *larkws.Client
	reportConnected := func() {
		if err := backend.Applied(ctx, b.ID, status(b, "connected", "")); err != nil {
			client.Close()
		}
	}
	client = larkws.NewClient(b.Credentials.AppID, b.Credentials.AppSecret,
		larkws.WithEventHandler(handler), larkws.WithLogger(quietLogger{}),
		larkws.WithOnReady(reportConnected), larkws.WithOnReconnected(reportConnected),
		larkws.WithOnDisconnected(func() {
			if ctx.Err() == nil {
				if err := backend.Applied(ctx, b.ID, status(b, "disconnected", "")); err != nil {
					client.Close()
				}
			}
		}))
	return client.Start(ctx)
}

func feishuMentionTokens(mentions []Mention, tokens bool) map[string]string {
	out := map[string]string{}
	conflicts := map[string]bool{}
	for _, m := range mentions {
		if m.ID == "" {
			continue
		}
		display := m.Key
		if !tokens {
			display = "@" + m.Name
			if m.Name == "" {
				display = "@" + m.ID
			}
		}
		if old, ok := out[m.ID]; ok && old != display {
			conflicts[m.ID] = true
		}
		out[m.ID] = display
	}
	for id := range conflicts {
		delete(out, id)
	}
	return out
}
