package channel

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
)

const feishuHumanFixtureToken = "a0000000-0000-4000-8000-000000000001"

func feishuHumanCardElements(t *testing.T, c map[string]any) []feishuHumanElement {
	t.Helper()
	if c["schema"] != "2.0" || c["config"].(feishuHumanElement)["update_multi"] != true {
		t.Fatal("Feishu card is not the native schema or cannot replace original")
	}
	return c["body"].(feishuHumanElement)["elements"].([]feishuHumanElement)
}

func TestFeishuHumanCardBinaryButtonsOnlyAndRawReplacement(t *testing.T) {
	const summary = "[系统消息]目标\n背景\n操作范围；原样保留 文本与 **字符**"
	spec := feishuHumanCardSpec{Token: feishuHumanFixtureToken, Summary: summary, DecisionRequired: true}
	pending, ok := buildFeishuHumanCard(spec)
	if !ok {
		t.Fatal("pure binary approval must be a native candidate")
	}
	elements := feishuHumanCardElements(t, pending)
	if len(elements) != 4 || elements[0]["text"].(feishuHumanElement)["content"] != summary ||
		elements[1]["text"].(feishuHumanElement)["content"] != feishuHumanReplyHint {
		t.Fatal("expected the original summary and two standalone buttons")
	}
	for i, action := range []string{"approve", "reject"} {
		button := elements[i+2]
		behavior := button["behaviors"].([]feishuHumanElement)[0]
		payload := behavior["value"].(feishuHumanElement)
		if button["tag"] != "button" || button["form_action_type"] != nil || button["name"] != action ||
			behavior["type"] != "callback" || payload[feishuHumanCardMarker] != spec.Token || payload["action"] != action {
			t.Fatal("click not bound to exact approval decision")
		}
	}
	for _, tc := range []struct{ decision, status string }{{"approve", "已同意"}, {"reject", "已拒绝"}, {"information", "已提交"}, {"expired", "已处理"}} {
		done := completeFeishuHumanCard(spec, tc.decision, "王同学")
		if done.Type != "raw" {
			t.Fatal("completed card must replace original in place")
		}
		body := feishuHumanCardElements(t, done.Data.(map[string]any))
		if len(body) != 2 || body[0]["text"].(feishuHumanElement)["content"] != summary || body[1]["text"].(feishuHumanElement)["content"] != tc.status+" · 王同学" {
			t.Fatal("replacement lost original summary or status")
		}
		wire, err := json.Marshal(done)
		if err != nil || strings.Contains(string(wire), spec.Token) || strings.Contains(string(wire), `"tag":"button"`) || strings.Contains(string(wire), `"tag":"form"`) {
			t.Fatal("completed replacement retained sensitive marker or live controls", err)
		}
	}
}

func TestFeishuHumanCardFallbackRetainsProviderError(t *testing.T) {
	const original = "完整审批说明"
	b := Binding{Provider: "feishu"}
	d := Delivery{ID: feishuHumanFixtureToken, ChatType: "direct", Text: original,
		HumanCard: &humanConfirmationCard{Description: original, DecisionRequired: true}}
	var attempts []string
	got := sendHumanConfirmationCard(t.Context(), b, d, func(_ context.Context, _ Binding, sent Delivery) deliveryReceipt {
		attempts = append(attempts, sent.contentFormat())
		if sent.contentFormat() == "human_confirmation_card" {
			return feishuMessageRejected(true, "interactive", 230099, http.StatusBadRequest, false)
		}
		if sent.Text != original {
			t.Fatal("text fallback did not preserve the full approval notice")
		}
		return deliveryReceipt{Status: "sent"}
	})
	if !reflect.DeepEqual(attempts, []string{"human_confirmation_card", "text"}) || got.Status != "sent" ||
		got.HumanConfirmationCard || got.humanCardFallback != "provider_rejected" || got.providerErrorCode != 230099 {
		t.Fatalf("fallback lost original failure evidence: %+v, attempts %v", got, attempts)
	}
}

func TestFeishuHumanCardUnknownDeliveryDoesNotSendDuplicateText(t *testing.T) {
	b := Binding{Provider: "feishu"}
	d := Delivery{ID: feishuHumanFixtureToken, ChatType: "direct", Text: "完整审批说明",
		HumanCard: &humanConfirmationCard{Description: "完整审批说明", DecisionRequired: true}}
	var attempts int
	got := sendHumanConfirmationCard(t.Context(), b, d, func(_ context.Context, _ Binding, sent Delivery) deliveryReceipt {
		attempts++
		if sent.contentFormat() != "human_confirmation_card" {
			t.Fatal("unconfirmed card send may have reached Feishu; don't also send text")
		}
		return deliveryReceipt{Status: "unknown"}
	})
	if attempts != 1 || got.Status != "unknown" {
		t.Fatalf("unexpected attempts or outcome: %d %+v", attempts, got)
	}
}

func feishuHumanFixtureQuestions() []feishuHumanQuestion {
	opts := []feishuHumanOption{{ID: "a", Label: "甲"}, {ID: "b", Label: "乙"}}
	return []feishuHumanQuestion{
		{ID: "单选", Text: "请选择分类", AnswerType: "choice", Required: true, Options: opts},
		{ID: "multiple", Text: "请选择涉及范围", AnswerType: "multi_choice", Options: opts},
		{ID: "why", Text: "说明处理依据", AnswerType: "text", Required: true},
		{ID: "quantity", Text: "请输入预期数量", AnswerType: "number", Required: true},
	}
}

func TestFeishuHumanCardAcceptsIndividualAndMixedFormTypes(t *testing.T) {
	questions := feishuHumanFixtureQuestions()
	tags := []string{"select_static", "multi_select_static", "input", "input"}
	spec := feishuHumanCardSpec{Token: feishuHumanFixtureToken, Summary: "原确认内容"}
	if _, ok := buildFeishuHumanCard(spec); ok {
		t.Fatal("answer-only card with no fields should fall back to text")
	}
	for i, q := range questions {
		spec.Questions = []feishuHumanQuestion{q}
		feishuAssertQuestionFields(t, spec, tags[i:i+1])
	}
	spec.Questions = questions
	feishuAssertQuestionFields(t, spec, tags)

	spec.DecisionRequired = true
	pending, ok := buildFeishuHumanCard(spec)
	if !ok {
		t.Fatal("approval + questions should have valid form")
	}
	body := feishuHumanCardElements(t, pending)
	if len(body) != 4 || body[3]["tag"] != "button" || body[3]["name"] != "reject" || body[3]["form_action_type"] != nil {
		t.Fatal("reject is blocked behind mandatory approval questions")
	}
	spec.Questions = []feishuHumanQuestion{{ID: "choice", Text: "缺选项", AnswerType: "choice"}}
	if _, ok := buildFeishuHumanCard(spec); ok {
		t.Fatal("empty choices submitted as a native form")
	}
}

func feishuAssertQuestionFields(t *testing.T, spec feishuHumanCardSpec, tags []string) {
	t.Helper()
	c, ok := buildFeishuHumanCard(spec)
	if !ok {
		t.Fatal("supported fields unexpectedly refused")
	}
	body := feishuHumanCardElements(t, c)
	if body[1]["text"].(feishuHumanElement)["content"] != feishuHumanReplyHint {
		t.Fatal("form must say how to reply if callbacks are unavailable")
	}
	form := body[2]
	fields := form["elements"].([]feishuHumanElement)
	if form["tag"] != "form" || len(fields) != len(tags)*2+1 || fields[len(fields)-1]["name"] != "submit" {
		t.Fatal("expected one native form with submit")
	}
	for i, tag := range tags {
		field := fields[i*2+1]
		q := spec.Questions[i]
		name, _ := field["name"].(string)
		id, _, valid := feishuHumanFieldID(name)
		if !valid || id != q.ID || field["tag"] != tag || field["required"] != q.Required || fields[i*2]["text"].(feishuHumanElement)["content"] != q.Text {
			t.Fatal("question ID/required information/label lost")
		}
		if tag == "input" && field["max_length"] != 1000 {
			t.Fatal("Feishu card input exceeds documented limit")
		}
		if len(q.Options) > 0 {
			choices := field["options"].([]feishuHumanElement)
			if choices[1]["value"] != "b" || choices[1]["text"].(feishuHumanElement)["content"] != "乙" {
				t.Fatal("human option label and stable option ID confused")
			}
		}
	}
}

func feishuHumanCallbackFixture(t *testing.T, action string, form map[string]any) (Binding, *callback.CardActionTriggerEvent) {
	t.Helper()
	var e callback.CardActionTriggerEvent
	wire, err := json.Marshal(map[string]any{
		"schema": "2.0",
		"header": map[string]any{"event_id": "event", "event_type": "card.action.trigger", "app_id": "cli_app", "tenant_key": "tenant"},
		"event": map[string]any{
			"operator": map[string]any{"open_id": "ou_actor", "tenant_key": "tenant"},
			"context":  map[string]any{"open_message_id": "om_original", "open_chat_id": "oc_original"},
			"action":   map[string]any{"name": action, "tag": "button", "value": map[string]any{feishuHumanCardMarker: feishuHumanFixtureToken, "action": action}, "form_value": form},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(wire, &e); err != nil {
		t.Fatal(err)
	}
	b := Binding{Provider: "feishu", Enabled: true, TenantID: "tenant"}
	b.Credentials.AppID = "cli_app"
	return b, &e
}

func feishuHumanTestField(kind byte, id string) string {
	return string(kind) + "_" + base64.RawURLEncoding.EncodeToString([]byte(id))
}

func TestFeishuHumanCardCallbackMatchesFrozenQuestionContract(t *testing.T) {
	field := feishuHumanTestField
	form := map[string]any{field('c', "单选"): "b", field('m', "multiple"): []any{"a", "b"}, field('q', "why"): " 原文 ", field('n', "quantity"): "-1.25e2", field('q', "blank"): ""}
	b, e := feishuHumanCallbackFixture(t, "submit", form)
	out, ok := parseFeishuHumanCardEvent(b, e)
	want := []feishuHumanAnswer{
		{QuestionID: "multiple", OptionIDs: []string{"a", "b"}},
		{QuestionID: "quantity", Value: "-1.25e2"},
		{QuestionID: "why", Value: "原文"},
		{QuestionID: "单选", Value: "b"},
	}
	if !ok || out.Decision != "information" || out.EventID != "event" || out.ActorID != "ou_actor" || out.TenantID != "tenant" ||
		out.ChatID != "oc_original" || out.MessageID != "om_original" || out.Token != feishuHumanFixtureToken || !reflect.DeepEqual(out.Answers, want) {
		t.Fatal("did not preserve authoritative callback scope and typed answer shape")
	}
	for _, decision := range []string{"approve", "reject"} {
		b, e := feishuHumanCallbackFixture(t, decision, nil)
		r, ok := parseFeishuHumanCardEvent(b, e)
		if !ok || r.Decision != decision || r.Text != "" || len(r.Answers) != 0 {
			t.Fatal("standalone decision requires no form values")
		}
		for _, note := range []string{"", "仅这一句说明"} {
			b, e := feishuHumanCallbackFixture(t, decision, map[string]any{"note": note})
			r, ok := parseFeishuHumanCardEvent(b, e)
			if !ok || r.Decision != decision || r.Text != note || len(r.Answers) != 0 {
				t.Fatal("binary decision corrupted optional note")
			}
		}
	}
}

func TestFeishuHumanCardCallbackRejectsConflictingScopeAndMalformedValues(t *testing.T) {
	field := feishuHumanTestField
	for _, tc := range []struct {
		name string
		edit func(*callback.CardActionTriggerEvent)
	}{
		{"app", func(e *callback.CardActionTriggerEvent) { e.EventV2Base.Header.AppID = "other-app" }},
		{"tenant", func(e *callback.CardActionTriggerEvent) { e.EventV2Base.Header.TenantKey = "other-tenant" }},
		{"actor tenant", func(e *callback.CardActionTriggerEvent) { s := "other"; e.Event.Operator.TenantKey = &s }},
		{"wrong host", func(e *callback.CardActionTriggerEvent) { e.Event.Host = "im_top_notice" }},
		{"no message", func(e *callback.CardActionTriggerEvent) { e.Event.Context.OpenMessageID = "" }},
		{"no actor", func(e *callback.CardActionTriggerEvent) { e.Event.Operator.OpenID = "" }},
		{"no event id", func(e *callback.CardActionTriggerEvent) { e.EventV2Base.Header.EventID = "" }},
		{"wrong button", func(e *callback.CardActionTriggerEvent) { e.Event.Action.Name = "other" }},
		{"change event", func(e *callback.CardActionTriggerEvent) { e.Event.Action.Tag = "select_static" }},
		{"forged marker", func(e *callback.CardActionTriggerEvent) { delete(e.Event.Action.Value, feishuHumanCardMarker) }},
		{"multi duplicates", func(e *callback.CardActionTriggerEvent) { e.Event.Action.FormValue[field('m', "x")] = []any{"a", "a"} }},
		{"multi string", func(e *callback.CardActionTriggerEvent) { e.Event.Action.FormValue[field('m', "x")] = "a" }},
		{"numeric invalid", func(e *callback.CardActionTriggerEvent) { e.Event.Action.FormValue[field('n', "x")] = "NaN" }},
		{"same id twice", func(e *callback.CardActionTriggerEvent) {
			e.Event.Action.FormValue[field('q', "x")] = "a"
			e.Event.Action.FormValue[field('c', "x")] = "a"
		}},
		{"invalid field name", func(e *callback.CardActionTriggerEvent) { e.Event.Action.FormValue["q_%%%%"] = "a" }},
		{"information note", func(e *callback.CardActionTriggerEvent) { e.Event.Action.FormValue["note"] = "a" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, e := feishuHumanCallbackFixture(t, "submit", map[string]any{})
			tc.edit(e)
			if _, ok := parseFeishuHumanCardEvent(b, e); ok {
				t.Fatal("malformed payload parsed as valid")
			}
		})
	}
}
