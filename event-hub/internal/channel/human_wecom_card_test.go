package channel

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func wecomHumanChoice(id, kind string) humanConfirmationQuestion {
	return humanConfirmationQuestion{ID: id, Text: "发布环境", Required: true, AnswerType: kind, Options: []humanConfirmationOption{
		{ID: "staging", Label: "测试环境"}, {ID: "production", Label: "正式环境"},
	}}
}

func TestRenderWecomHumanCard(t *testing.T) {
	decision := &humanConfirmationCard{Title: "批准变更？", Description: "同意后：应用变更\n影响范围：测试集群", DecisionRequired: true}
	single := &humanConfirmationCard{Title: "发布环境", Description: "请选择用于验收的环境", Questions: []humanConfirmationQuestion{wecomHumanChoice("where", "choice")}}
	multi := &humanConfirmationCard{Title: single.Title, Description: single.Description, Questions: []humanConfirmationQuestion{wecomHumanChoice("where", "multi_choice")}}
	selectors := &humanConfirmationCard{Title: "请选择", Questions: []humanConfirmationQuestion{single.Questions[0], wecomHumanChoice("reason", "choice")}}

	out, ok := renderWecomHumanCard(decision, "nonce_A-1")
	if !ok || out["card_type"] != wecomHumanButton || out["task_id"] != "nonce_A-1" || out["sub_title_text"] != decision.Description ||
		!reflect.DeepEqual(out["button_list"], []map[string]any{{"text": "同意", "style": 1, "key": "approve"}, {"text": "拒绝", "style": 2, "key": "reject"}}) {
		t.Fatalf("approval card = %#v", out)
	}
	for _, tc := range []struct {
		card *humanConfirmationCard
		mode int
		key  string
	}{{single, 0, wecomHumanSingleKey}, {multi, 1, wecomHumanMultiKey}} {
		body, ok := renderWecomHumanCard(tc.card, "nonce")
		if !ok || body["card_type"] != wecomHumanVote {
			t.Fatalf("vote card = %#v", body)
		}
		checkbox, ok := body["checkbox"].(map[string]any)
		if !ok || checkbox["question_key"] != "where" || checkbox["mode"] != tc.mode ||
			!reflect.DeepEqual(checkbox["option_list"], []map[string]string{{"id": "staging", "text": "测试环境"}, {"id": "production", "text": "正式环境"}}) ||
			!reflect.DeepEqual(body["submit_button"], map[string]string{"text": "提交答复", "key": tc.key}) {
			t.Fatalf("vote card = %#v", body)
		}
	}
	body, ok := renderWecomHumanCard(selectors, "nonce")
	if !ok || body["card_type"] != wecomHumanDropdown {
		t.Fatalf("dropdown card = %#v", body)
	}
	list, ok := body["select_list"].([]map[string]any)
	if !ok || len(list) != 2 || list[0]["question_key"] != "where" || list[1]["question_key"] != "reason" ||
		list[0]["selected_id"] != wecomHumanChoiceEmpty ||
		!reflect.DeepEqual(list[0]["option_list"], []map[string]string{
			{"id": wecomHumanChoiceEmpty, "text": "请选择"}, {"id": "staging", "text": "测试环境"}, {"id": "production", "text": "正式环境"},
		}) {
		t.Fatalf("dropdown card = %#v", body)
	}
}

func TestRenderWecomHumanCardDeclinesWholeUnsupportedCard(t *testing.T) {
	choice := wecomHumanChoice("one", "choice")
	badOption := wecomHumanChoice("badopt", "choice")
	badOption.Options[0].ID = wecomHumanChoiceEmpty
	longOption := wecomHumanChoice("longopt", "choice")
	longOption.Options[0].Label = strings.Repeat("长", 12)
	longQuestion := wecomHumanChoice("longq", "choice")
	longQuestion.Text = strings.Repeat("长", 14)
	cases := []struct {
		name, title, description string
		decision                 bool
		q                        []humanConfirmationQuestion
	}{
		{name: "no content", title: "问题"},
		{name: "empty title", description: "说明", decision: true},
		{name: "approval with unanswered field", title: "审批", description: "说明", decision: true, q: []humanConfirmationQuestion{choice}},
		{name: "required text field", title: choice.Text, q: []humanConfirmationQuestion{choice, wecomHumanChoice("two", "text")}},
		{name: "optional number field", title: choice.Text, q: []humanConfirmationQuestion{choice, {ID: "two", Text: "请输入数字", AnswerType: "number"}}},
		{name: "unsupported boolean field", title: choice.Text, q: []humanConfirmationQuestion{choice, wecomHumanChoice("two", "boolean")}},
		{name: "mixed multi dropdown", title: choice.Text, q: []humanConfirmationQuestion{choice, wecomHumanChoice("two", "multi_choice")}},
		{name: "duplicate question ID", title: choice.Text, q: []humanConfirmationQuestion{choice, choice}},
		{name: "placeholder collision", title: badOption.Text, q: []humanConfirmationQuestion{badOption}},
		{name: "oversize option label", title: choice.Text, q: []humanConfirmationQuestion{longOption}},
		{name: "oversize dropdown title", title: choice.Text, q: []humanConfirmationQuestion{choice, longQuestion}},
		{name: "long approval details", title: "审批", decision: true, description: strings.Repeat("长", 113)},
		{name: "long choice details", title: choice.Text, description: strings.Repeat("长", 31), q: []humanConfirmationQuestion{choice}},
		{name: "not visible question", title: "请选择", description: "无法看到题目", q: []humanConfirmationQuestion{choice}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			card := &humanConfirmationCard{Title: tc.title, Description: tc.description, DecisionRequired: tc.decision, Questions: tc.q}
			if got, ok := renderWecomHumanCard(card, "nonce"); ok || got != nil {
				t.Fatalf("unsafe partial card rendered: %#v", got)
			}
		})
	}
	if got, ok := renderWecomHumanCard(&humanConfirmationCard{Title: "批准变更？", Description: "说明", DecisionRequired: true}, "nonce ."); ok || got != nil {
		t.Fatalf("unsafe task ID: %#v", got)
	}
}

func makeWecomHumanEvent(t *testing.T, scope string, chat string, bot string, action string) wecomFrame {
	t.Helper()
	f := wecomFrame{Cmd: "aibot_event_callback", Body: json.RawMessage(`{"msgid":"msg","aibotid":"` + bot + `","from":{"userid":"user"},"chatid":"` + chat + `","chattype":"` + scope + `","msgtype":"event","event":{"eventtype":"template_card_event",` + action + `}}`)}
	f.Headers.ReqID = "provider-request"
	if !json.Valid(f.Body) {
		t.Fatalf("invalid test callback: %s", f.Body)
	}
	return f
}

func TestParseWecomHumanCardEvent(t *testing.T) {
	b := Binding{Provider: "wecom"}
	b.Credentials.AppID = "bot"
	base := wecomHumanCardEvent{MessageID: "msg", ChatID: "user", ActorID: "user", TaskID: "nonce"}
	cases := []struct {
		name, wire, cardType, decision string
		want                           []humanCardAnswerItem
	}{
		{"approve", `"task_id":"nonce","event_key":"approve","card_type":"button_interaction"`, wecomHumanButton, "approve", nil},
		{"reject nested", `"template_card_event":{"task_id":"nonce","event_key":"reject"}`, wecomHumanButton, "reject", nil},
		{"single", `"task_id":"nonce","event_key":"kiko_submit_choice","selected_items":{"selected_item":[{"question_key":"env","option_ids":{"option_id":["production"]}}]}`, wecomHumanVote, "information", []humanCardAnswerItem{{QuestionID: "env", Value: "production"}}},
		{"multi nested", `"template_card_event":{"task_id":"nonce","event_key":"kiko_submit_multi_choice","selected_items":{"selected_item":[{"question_key":"env","option_ids":{"option_id":["staging","production"]}}]}}`, wecomHumanVote, "information", []humanCardAnswerItem{{QuestionID: "env", OptionIDs: []string{"production", "staging"}}}},
		{"dropdown unselected ignored", `"task_id":"nonce","event_key":"kiko_submit_dropdowns","selected_items":{"selected_item":[{"question_key":"env","option_ids":{"option_id":["kiko_unselected"]}},{"question_key":"target","option_ids":{"option_id":["staging"]}}]}`, wecomHumanDropdown, "information", []humanCardAnswerItem{{QuestionID: "target", Value: "staging"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseWecomHumanCardEvent(makeWecomHumanEvent(t, "single", "user", "bot", tc.wire), b)
			expected := base
			expected.CardType, expected.Decision, expected.Answers = tc.cardType, tc.decision, tc.want
			if !ok || !reflect.DeepEqual(got, expected) {
				t.Fatalf("callback = %#v, valid=%v, want %#v", got, ok, expected)
			}
		})
	}
	if got, ok := parseWecomHumanCardEvent(makeWecomHumanEvent(t, "", "", "bot", cases[0].wire), b); !ok || got.ChatID != "user" {
		t.Fatalf("provider private callback without chat fields = %#v", got)
	}
}

func TestParseWecomHumanCardEventRejectsInvalidScopeAndShapes(t *testing.T) {
	b := Binding{Provider: "wecom"}
	b.Credentials.AppID = "bot"
	approval := `"task_id":"nonce","event_key":"approve"`
	for _, spec := range [][3]string{{"group", "user", "bot"}, {"single", "wrong-actor", "bot"}, {"single", "user", "other-bot"}, {"", "user", "bot"}} {
		if _, ok := parseWecomHumanCardEvent(makeWecomHumanEvent(t, spec[0], spec[1], spec[2], approval), b); ok {
			t.Fatalf("accepted wrong scope/bot: %#v", spec)
		}
	}
	for _, wire := range []string{
		approval + `,"card_type":"vote_interaction"`,
		approval + `,"template_card_event":{"task_id":"other-nonce","event_key":"approve"}`,
		approval + `,"selected_items":{"selected_item":[{"question_key":"env","option_ids":{"option_id":["production"]}}]}`,
		`"task_id":"nonce","event_key":"unknown"`,
		`"task_id":"nonce","event_key":"kiko_submit_choice","selected_items":{"selected_item":[{"question_key":"env","option_ids":{"option_id":["staging","production"]}}]}`,
		`"task_id":"nonce","event_key":"kiko_submit_multi_choice","selected_items":{"selected_item":[{"question_key":"env","option_ids":{"option_id":["production","production"]}}]}`,
		`"task_id":"nonce","event_key":"kiko_submit_dropdowns","selected_items":{"selected_item":[{"question_key":"env","option_ids":{"option_id":["staging"]}},{"question_key":"env","option_ids":{"option_id":["production"]}}]}`,
		`"task_id":"nonce","event_key":"kiko_submit_dropdowns","selected_items":{"selected_item":[{"question_key":"env","option_ids":{"option_id":["kiko_unselected"]}},{"question_key":"target","option_ids":{"option_id":["kiko_unselected"]}}]}`,
	} {
		if got, ok := parseWecomHumanCardEvent(makeWecomHumanEvent(t, "single", "user", "bot", wire), b); ok {
			t.Fatalf("accepted invalid wire %s as %#v", wire, got)
		}
	}
	f := makeWecomHumanEvent(t, "single", "", "bot", approval)
	f.Headers.ReqID = ""
	if got, ok := parseWecomHumanCardEvent(f, b); ok {
		t.Fatalf("accepted event missing provider req_id as %#v", got)
	}
}

func TestRenderWecomHumanCardUpdate(t *testing.T) {
	for _, tc := range [][2]string{{"approve", "已同意"}, {"reject", "已拒绝"}, {"information", "已提交"}} {
		got, ok := renderWecomHumanCardUpdate("nonce", "actor", tc[0])
		exp := map[string]any{
			"response_type": "update_template_card",
			"userids":       []string{"actor"},
			"template_card": map[string]any{
				"card_type":   "text_notice",
				"task_id":     "nonce",
				"card_action": map[string]any{"type": 1, "url": "https://work.weixin.qq.com/"},
				"main_title":  map[string]string{"title": tc[1], "desc": "答复已记录，无需再次操作。"},
			},
		}
		if !ok || !reflect.DeepEqual(got, exp) {
			t.Fatalf("update = %#v, valid=%v", got, ok)
		}
	}
	if got, ok := renderWecomHumanCardUpdate("nonce", "actor", "needs_more"); ok || got != nil {
		t.Fatalf("nonterminal response rendered: %#v", got)
	}
}
