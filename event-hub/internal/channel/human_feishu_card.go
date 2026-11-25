package channel

import (
	"encoding/base64"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
)

const (
	feishuHumanCardMarker = "kiko_human_confirmation"
	feishuHumanNoteField  = "note"
)

type feishuHumanCardSpec struct {
	Token            string
	Summary          string
	DecisionRequired bool
	Questions        []feishuHumanQuestion
}

type feishuHumanQuestion struct {
	ID, Text, AnswerType string
	Required             bool
	Options              []feishuHumanOption
}

type feishuHumanOption struct {
	ID, Label string
}

type feishuHumanAnswer struct {
	QuestionID string   `json:"questionId"`
	Value      string   `json:"value,omitempty"`
	OptionIDs  []string `json:"optionIds,omitempty"`
}

type feishuHumanCardAction struct {
	Token, EventID, TenantID, ActorID, ChatID, MessageID, Decision, Text string
	Answers                                                              []feishuHumanAnswer
}

type feishuHumanElement = map[string]any

func feishuHumanPlainText(s string) feishuHumanElement {
	return feishuHumanElement{"tag": "plain_text", "content": s}
}

func feishuHumanParagraph(s string) feishuHumanElement {
	return feishuHumanElement{"tag": "div", "text": feishuHumanPlainText(s)}
}

func feishuHumanShell(elements []feishuHumanElement) map[string]any {
	return map[string]any{
		"schema": "2.0", "config": feishuHumanElement{"update_multi": true},
		"header": feishuHumanElement{"title": feishuHumanPlainText("人工确认")},
		"body":   feishuHumanElement{"elements": elements},
	}
}

func feishuHumanButton(token, decision, label, style string) feishuHumanElement {
	return feishuHumanElement{
		"tag": "button", "name": decision, "type": style, "form_action_type": "submit",
		"text": feishuHumanPlainText(label),
		"behaviors": []feishuHumanElement{{
			"type": "callback", "value": feishuHumanElement{feishuHumanCardMarker: token, "action": decision},
		}},
	}
}

func feishuHumanForm(name string, fields []feishuHumanElement) feishuHumanElement {
	return feishuHumanElement{"tag": "form", "name": name, "elements": fields}
}

func feishuHumanNote() feishuHumanElement {
	return feishuHumanElement{
		"tag": "input", "name": feishuHumanNoteField, "max_length": 2000, "required": false,
		"placeholder": feishuHumanPlainText("补充说明（选填）"),
	}
}

func buildFeishuHumanCard(spec feishuHumanCardSpec) (map[string]any, bool) {
	if !validFeishuHumanIdentifier(spec.Token, 256) || len(spec.Questions) > 12 ||
		(!spec.DecisionRequired && len(spec.Questions) == 0) || len(spec.Summary) > 16000 {
		return nil, false
	}
	body := []feishuHumanElement{feishuHumanParagraph(spec.Summary)}
	form, ok := feishuHumanQuestionFields(spec.Questions)
	if !ok {
		return nil, false
	}
	if !spec.DecisionRequired {
		form = append(form, feishuHumanButton(spec.Token, "submit", "提交", "primary"))
		return feishuHumanShell(append(body, feishuHumanForm("information", form))), true
	}
	approve := feishuHumanButton(spec.Token, "approve", "同意", "primary")
	reject := feishuHumanButton(spec.Token, "reject", "拒绝", "danger")
	if len(spec.Questions) == 0 {
		return feishuHumanShell(append(body, feishuHumanForm("approval", []feishuHumanElement{feishuHumanNote(), approve, reject}))), true
	}
	// Mandatory approval questions may never impede declining the request.
	form = append(form, feishuHumanNote(), approve)
	delete(reject, "form_action_type")
	return feishuHumanShell(append(body, feishuHumanForm("approval", form), reject)), true
}

func feishuHumanQuestionFields(questions []feishuHumanQuestion) ([]feishuHumanElement, bool) {
	form := make([]feishuHumanElement, 0, len(questions)*2+3)
	seen := make(map[string]bool, len(questions))
	for _, q := range questions {
		if !validFeishuQuestionID(q.ID) || seen[q.ID] || strings.TrimSpace(q.Text) == "" || utf8.RuneCountInString(q.Text) > 2000 {
			return nil, false
		}
		seen[q.ID] = true
		prefix := "q_"
		field := feishuHumanElement{"required": q.Required}
		switch q.AnswerType {
		case "choice", "multi_choice":
			opts, ok := feishuHumanOptions(q.Options)
			if !ok {
				return nil, false
			}
			prefix = "c_"
			field["tag"], field["options"], field["placeholder"] = "select_static", opts, feishuHumanPlainText("请选择")
			if q.AnswerType == "multi_choice" {
				prefix, field["tag"], field["placeholder"] = "m_", "multi_select_static", feishuHumanPlainText("可选多项")
			}
		case "text", "number":
			label := "请输入文字"
			if q.AnswerType == "number" {
				prefix, label = "n_", "请输入数字"
			}
			field["tag"], field["placeholder"], field["max_length"] = "input", feishuHumanPlainText(label), 2000
		default:
			return nil, false
		}
		field["name"] = prefix + base64.RawURLEncoding.EncodeToString([]byte(q.ID))
		form = append(form, feishuHumanParagraph(q.Text), field)
	}
	return form, true
}

func validFeishuQuestionID(id string) bool {
	return strings.TrimSpace(id) != "" && utf8.ValidString(id) && utf8.RuneCountInString(id) <= 80 && !strings.ContainsRune(id, 0)
}

func feishuHumanOptions(options []feishuHumanOption) ([]feishuHumanElement, bool) {
	if len(options) == 0 || len(options) > 20 {
		return nil, false
	}
	items := make([]feishuHumanElement, 0, len(options))
	seen := make(map[string]bool, len(options))
	for _, option := range options {
		if !validFeishuHumanIdentifier(option.ID, 64) || seen[option.ID] || strings.TrimSpace(option.Label) == "" {
			return nil, false
		}
		seen[option.ID] = true
		items = append(items, feishuHumanElement{"text": feishuHumanPlainText(option.Label), "value": option.ID})
	}
	return items, true
}

func validFeishuHumanIdentifier(s string, capBytes int) bool {
	return s != "" && len(s) <= capBytes && strings.IndexFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
	}) < 0
}

func completeFeishuHumanCard(spec feishuHumanCardSpec, decision, actorLabel string) *callback.Card {
	label := map[string]string{"approve": "已同意", "reject": "已拒绝", "information": "已提交", "submit": "已提交"}[decision]
	if label == "" {
		label = "已处理"
	}
	if actorLabel = strings.TrimSpace(actorLabel); actorLabel != "" {
		label += " · " + actorLabel
	}
	return &callback.Card{Type: "raw", Data: feishuHumanShell([]feishuHumanElement{feishuHumanParagraph(spec.Summary), feishuHumanParagraph(label)})}
}

func parseFeishuHumanCardEvent(b Binding, e *callback.CardActionTriggerEvent) (feishuHumanCardAction, bool) {
	var out feishuHumanCardAction
	if e == nil || e.EventV2Base == nil || e.EventV2Base.Header == nil || e.Event == nil ||
		e.Event.Action == nil || e.Event.Operator == nil || e.Event.Context == nil {
		return out, false
	}
	ev, h := e.Event, e.EventV2Base.Header
	if b.Provider != "feishu" || !b.Enabled || b.TenantID == "" || h.TenantKey != b.TenantID ||
		b.Credentials.AppID == "" || h.AppID != b.Credentials.AppID ||
		(h.EventType != "" && h.EventType != "card.action.trigger") ||
		(value(ev.Operator.TenantKey) != "" && value(ev.Operator.TenantKey) != h.TenantKey) ||
		(ev.Host != "" && ev.Host != "im_message") ||
		!validFeishuHumanIdentifier(h.EventID, 256) ||
		!validFeishuHumanIdentifier(ev.Operator.OpenID, 256) ||
		!validFeishuHumanIdentifier(ev.Context.OpenChatID, 256) ||
		!validFeishuHumanIdentifier(ev.Context.OpenMessageID, 256) {
		return out, false
	}
	token, _ := ev.Action.Value[feishuHumanCardMarker].(string)
	decision, _ := ev.Action.Value["action"].(string)
	if !validFeishuHumanIdentifier(token, 256) || ev.Action.Name != decision || (ev.Action.Tag != "" && ev.Action.Tag != "button") {
		return out, false
	}
	switch decision {
	case "approve", "reject":
	case "submit":
		decision = "information"
	default:
		return out, false
	}
	note, answers, ok := parseFeishuHumanAnswers(ev.Action.FormValue, decision)
	if !ok {
		return out, false
	}
	return feishuHumanCardAction{Token: token, EventID: h.EventID, TenantID: h.TenantKey, ActorID: ev.Operator.OpenID,
		ChatID: ev.Context.OpenChatID, MessageID: ev.Context.OpenMessageID, Decision: decision, Text: note, Answers: answers}, true
}

func parseFeishuHumanAnswers(form map[string]any, decision string) (string, []feishuHumanAnswer, bool) {
	if len(form) > 13 {
		return "", nil, false
	}
	var note string
	var answers []feishuHumanAnswer
	seen := map[string]bool{}
	for k, raw := range form {
		if k == feishuHumanNoteField && decision != "information" {
			var ok bool
			note, ok = feishuHumanValue(raw)
			if !ok {
				return "", nil, false
			}
			continue
		}
		id, kind, ok := feishuHumanFieldID(k)
		if !ok || seen[id] {
			return "", nil, false
		}
		seen[id] = true
		answer := feishuHumanAnswer{QuestionID: id}
		switch kind {
		case 'm':
			answer.OptionIDs, ok = feishuHumanMulti(raw)
		default:
			answer.Value, ok = feishuHumanValue(raw)
			if ok && answer.Value != "" {
				switch kind {
				case 'c':
					ok = validFeishuHumanIdentifier(answer.Value, 64)
				case 'n':
					ok = finiteFeishuHumanNumber(answer.Value)
				}
			}
		}
		if !ok || decision == "reject" {
			return "", nil, false
		}
		if answer.Value != "" || len(answer.OptionIDs) != 0 {
			answers = append(answers, answer)
		}
	}
	sort.Slice(answers, func(i, j int) bool { return answers[i].QuestionID < answers[j].QuestionID })
	return note, answers, true
}

func feishuHumanFieldID(k string) (string, byte, bool) {
	if len(k) <= 2 || len(k) > 430 || k[1] != '_' || !strings.ContainsRune("qcmn", rune(k[0])) {
		return "", 0, false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(k[2:])
	id := string(raw)
	return id, k[0], err == nil && validFeishuQuestionID(id)
}

func feishuHumanValue(raw any) (string, bool) {
	if raw == nil {
		return "", true
	}
	v, ok := raw.(string)
	return strings.TrimSpace(v), ok && utf8.ValidString(v) && utf8.RuneCountInString(v) <= 2000 && !strings.ContainsRune(v, 0)
}

func feishuHumanMulti(raw any) ([]string, bool) {
	if raw == nil {
		return nil, true
	}
	list, ok := raw.([]any)
	if !ok || len(list) > 20 {
		return nil, false
	}
	out := make([]string, 0, len(list))
	seen := map[string]bool{}
	for _, v := range list {
		option, ok := v.(string)
		if !ok || !validFeishuHumanIdentifier(option, 64) || seen[option] {
			return nil, false
		}
		seen[option] = true
		out = append(out, option)
	}
	return out, true
}

// finiteFeishuHumanNumber also accepts decimal/exponent notation without
// silently converting finite user-supplied strings to approximate float values.
func finiteFeishuHumanNumber(s string) bool {
	n, err := strconv.ParseFloat(s, 64)
	return err == nil && !math.IsInf(n, 0) && !math.IsNaN(n) && !strings.ContainsAny(s, "xXpP_")
}
