package channel

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	// Official DingTalk card callback tutorial, which explicitly makes the demo template available for tests.
	// https://open-dingtalk.github.io/developerpedia/docs/explore/tutorials/stream/bot/go/card-callback/
	humanDingApprovalTemplate = "dac1dbec-55af-40c3-be91-6d6882ef3b66.schema"
	// https://github.com/soimy/openclaw-channel-dingtalk/blob/main/src/card/card-template.ts
	humanDingFormTemplate = "c2a6355b-9724-4f7e-9653-d33fcb3311bb.schema"
)

var (
	errHumanDingCardUnsupported = errors.New("dingtalk card cannot represent confirmation")
	errHumanDingCardAnswer      = errors.New("invalid dingtalk card answer")
)

type humanDingCardSpec struct {
	Kind, Title, Description string
	Fields                   []humanDingCardField
}

type humanDingCardField struct {
	Name, Label, Type string
	Required          bool
	Options           []humanDingCardOption
}

type humanDingCardOption struct {
	Value, Label string
}

type humanDingCardExpectation struct {
	TrackID, TenantID, StaffID string
	Spec                       humanDingCardSpec
}

type humanDingCardAnswer struct {
	Decision string
	Values   map[string][]string
}

func humanDingCardPayload(trackID, robotCode, staffID string, spec humanDingCardSpec) (map[string]any, error) {
	if !dingID(trackID) || !dingRobotID(robotCode) || !dingID(staffID) || !validHumanDingSpec(spec) {
		return nil, errHumanDingCardUnsupported
	}
	template := humanDingApprovalTemplate
	params := humanDingApprovalParams(spec, "待处理")
	if spec.Kind == "form" {
		template = humanDingFormTemplate
		fields := make([]humanDingProviderField, 0, len(spec.Fields))
		for i, f := range spec.Fields {
			field := humanDingProviderField{Name: humanDingFieldName(i), Label: f.Label, Type: humanDingProviderType(f.Type), Required: f.Required}
			for _, option := range f.Options {
				field.Options = append(field.Options, humanDingProviderOption{Value: option.Value, Text: option.Label})
			}
			fields = append(fields, field)
		}
		form, err := json.Marshal(map[string][]humanDingProviderField{"fields": fields})
		if err != nil {
			return nil, err
		}
		params = map[string]string{
			"question_id": trackID, "question_title": spec.Title, "question_desc": spec.Description,
			"card_status": "pending", "form_btn_text": "提交", "selected_text": "", "selected_values": "[]",
			"form": string(form),
		}
	}
	return map[string]any{
		"cardTemplateId":          template,
		"outTrackId":              trackID,
		"callbackType":            "STREAM",
		"userIdType":              1,
		"cardData":                map[string]any{"cardParamMap": params},
		"openSpaceId":             "dtv1.card//IM_ROBOT." + staffID,
		"imRobotOpenSpaceModel":   map[string]bool{"supportForward": false},
		"imRobotOpenDeliverModel": map[string]string{"spaceType": "IM_ROBOT", "robotCode": robotCode},
	}, nil
}

type humanDingProviderField struct {
	Name     string                    `json:"name"`
	Label    string                    `json:"label"`
	Type     string                    `json:"type"`
	Required bool                      `json:"required"`
	Options  []humanDingProviderOption `json:"options,omitempty"`
}

type humanDingProviderOption struct {
	Value string `json:"value"`
	Text  string `json:"text"`
}

func humanDingFieldName(i int) string { return "answer_" + strconv.Itoa(i) }

func humanDingProviderType(typ string) string {
	switch typ {
	case "choice":
		return "SELECT"
	case "multi_choice":
		return "MULTI_SELECT"
	case "text":
		return "TEXT"
	case "number":
		return "NUMBER"
	default:
		return ""
	}
}

func humanDingTextOK(v string, max int) bool {
	return utf8.ValidString(v) && len(v) <= max && !strings.ContainsRune(v, 0)
}

func validHumanDingSpec(spec humanDingCardSpec) bool {
	if strings.TrimSpace(spec.Title) == "" || !humanDingTextOK(spec.Title, 256) || !humanDingTextOK(spec.Description, 4096) {
		return false
	}
	switch spec.Kind {
	case "approval":
		return len(spec.Fields) == 0
	case "form":
		if len(spec.Fields) == 0 || len(spec.Fields) > 20 {
			return false
		}
	default:
		return false
	}
	seen := make(map[string]bool, len(spec.Fields))
	for _, f := range spec.Fields {
		if !validHumanDingField(f) || seen[f.Name] {
			return false
		}
		seen[f.Name] = true
	}
	return true
}

func validHumanDingField(f humanDingCardField) bool {
	if !dingID(f.Name) || strings.TrimSpace(f.Label) == "" || !humanDingTextOK(f.Label, 256) || humanDingProviderType(f.Type) == "" {
		return false
	}
	if f.Type == "text" || f.Type == "number" {
		return len(f.Options) == 0
	}
	if len(f.Options) == 0 || len(f.Options) > 20 {
		return false
	}
	seen := make(map[string]bool, len(f.Options))
	for _, o := range f.Options {
		if strings.TrimSpace(o.Value) == "" || !humanDingTextOK(o.Value, 256) || seen[o.Value] ||
			strings.TrimSpace(o.Label) == "" || !humanDingTextOK(o.Label, 256) {
			return false
		}
		seen[o.Value] = true
	}
	return true
}

func humanDingApprovalParams(spec humanDingCardSpec, status string) map[string]string {
	// The template variable names reflect its original finance demo, but this adapter only
	// supplies confirmation content. In particular, it must not invent a payment amount.
	return map[string]string{"title": spec.Title, "type": "", "amount": "", "reason": spec.Description, "status": status}
}

func humanDingCardTerminalParams(spec humanDingCardSpec, answer humanDingCardAnswer) (map[string]string, error) {
	if !validHumanDingSpec(spec) {
		return nil, errHumanDingCardUnsupported
	}
	switch {
	case spec.Kind == "approval" && answer.Decision == "approve":
		return humanDingApprovalParams(spec, "已同意"), nil
	case spec.Kind == "approval" && answer.Decision == "reject":
		return humanDingApprovalParams(spec, "被拒绝"), nil
	case spec.Kind == "form" && answer.Decision == "submit":
		return map[string]string{"card_status": "submitted", "form_btn_text": "已提交", "question_desc": spec.Description}, nil
	default:
		return nil, errHumanDingCardAnswer
	}
}

type humanDingCallback struct {
	Type      string `json:"type"`
	Tenant    string `json:"corpId"`
	Actor     string `json:"userId"`
	ActorType int    `json:"userIdType"`
	Track     string `json:"outTrackId"`
	SpaceType string `json:"spaceType"`
	Content   string `json:"content"`
}

type humanDingCallbackData struct {
	Private struct {
		Params struct {
			Action string                     `json:"action"`
			Form   map[string]json.RawMessage `json:"form"`
		} `json:"params"`
	} `json:"cardPrivateData"`
}

func parseHumanDingCardAnswer(raw []byte, expected humanDingCardExpectation) (humanDingCardAnswer, error) {
	var callback humanDingCallback
	if len(raw) > 128<<10 || json.Unmarshal(raw, &callback) != nil || callback.Type != "actionCallback" ||
		!dingID(expected.TrackID) || !dingID(expected.TenantID) || !dingID(expected.StaffID) ||
		callback.Track != expected.TrackID || callback.Actor != expected.StaffID || callback.Tenant != expected.TenantID ||
		(callback.ActorType != 0 && callback.ActorType != 1) || !humanDingDirectSpace(callback.SpaceType) ||
		!validHumanDingSpec(expected.Spec) {
		return humanDingCardAnswer{}, errHumanDingCardAnswer
	}
	var content humanDingCallbackData
	if json.Unmarshal([]byte(callback.Content), &content) != nil {
		return humanDingCardAnswer{}, errHumanDingCardAnswer
	}
	if expected.Spec.Kind == "approval" {
		switch content.Private.Params.Action {
		case "accept":
			return humanDingCardAnswer{Decision: "approve"}, nil
		case "reject":
			return humanDingCardAnswer{Decision: "reject"}, nil
		default:
			return humanDingCardAnswer{}, errHumanDingCardAnswer
		}
	}
	return humanDingFormAnswer(content.Private.Params.Form, expected.Spec.Fields)
}

func humanDingDirectSpace(space string) bool {
	return space == "" || strings.EqualFold(space, "IM_ROBOT") || strings.EqualFold(space, "IM")
}

func humanDingFormAnswer(form map[string]json.RawMessage, fields []humanDingCardField) (humanDingCardAnswer, error) {
	if form == nil || len(form) > len(fields) {
		return humanDingCardAnswer{}, errHumanDingCardAnswer
	}
	answers := make(map[string][]string, len(form))
	found := 0
	for i, f := range fields {
		raw, exists := form[humanDingFieldName(i)]
		if exists {
			found++
		}
		values, ok := humanDingFieldAnswer(raw, f)
		if !ok || f.Required && len(values) == 0 {
			return humanDingCardAnswer{}, errHumanDingCardAnswer
		}
		if len(values) > 0 {
			answers[f.Name] = values
		}
	}
	if len(form) != found {
		return humanDingCardAnswer{}, errHumanDingCardAnswer
	}
	return humanDingCardAnswer{Decision: "submit", Values: answers}, nil
}

func humanDingFieldAnswer(raw json.RawMessage, field humanDingCardField) ([]string, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, true
	}
	if raw[0] == '{' {
		var wrapper struct {
			Value json.RawMessage `json:"value"`
		}
		if json.Unmarshal(raw, &wrapper) != nil || len(wrapper.Value) == 0 {
			return nil, false
		}
		raw = bytes.TrimSpace(wrapper.Value)
		if bytes.Equal(raw, []byte("null")) {
			return nil, true
		}
	}
	switch field.Type {
	case "text", "number":
		return humanDingScalarAnswer(raw, field.Type)
	case "choice", "multi_choice":
		return humanDingChoicesAnswer(raw, field)
	default:
		return nil, false
	}
}

func humanDingScalarAnswer(raw json.RawMessage, typ string) ([]string, bool) {
	var scalar string
	if json.Unmarshal(raw, &scalar) != nil {
		var number json.Number
		if typ != "number" || json.Unmarshal(raw, &number) != nil {
			return nil, false
		}
		scalar = number.String()
	}
	if !humanDingTextOK(scalar, 4096) {
		return nil, false
	}
	if strings.TrimSpace(scalar) == "" {
		return nil, true
	}
	if typ == "number" {
		scalar = strings.TrimSpace(scalar)
		var v json.Number
		if json.Unmarshal([]byte(scalar), &v) != nil || v.String() != scalar {
			return nil, false
		}
		n, err := v.Float64()
		if err != nil || math.IsInf(n, 0) || math.IsNaN(n) {
			return nil, false
		}
	}
	return []string{scalar}, true
}

func humanDingChoicesAnswer(raw json.RawMessage, field humanDingCardField) ([]string, bool) {
	var choices []string
	if len(raw) != 0 && raw[0] == '[' {
		if json.Unmarshal(raw, &choices) != nil {
			return nil, false
		}
	} else {
		var choice string
		if json.Unmarshal(raw, &choice) != nil {
			return nil, false
		}
		if choice != "" {
			choices = []string{choice}
		}
	}
	if len(choices) > len(field.Options) || field.Type == "choice" && len(choices) > 1 {
		return nil, false
	}
	valid := make(map[string]bool, len(field.Options))
	for _, o := range field.Options {
		valid[o.Value] = true
	}
	for _, v := range choices {
		if !valid[v] {
			return nil, false
		}
		delete(valid, v) // Unknown and duplicate option IDs are both invalid.
	}
	return choices, true
}
