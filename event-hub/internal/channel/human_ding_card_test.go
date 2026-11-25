package channel

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func humanDingTestField(kind string) humanDingCardField {
	field := humanDingCardField{Name: "biz-" + kind, Label: "Test " + kind, Type: kind, Required: true}
	if kind == "choice" || kind == "multi_choice" {
		field.Options = []humanDingCardOption{{Value: "opaque-first", Label: "First"}, {Value: "opaque-second", Label: "Second"}}
	}
	return field
}

func humanDingTestPayload(t *testing.T, data, track, tenant, staff, space string) []byte {
	t.Helper()
	out, err := json.Marshal(humanDingCallback{Type: "actionCallback", Tenant: tenant, Actor: staff, ActorType: 1, Track: track, SpaceType: space, Content: data})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestHumanDingApprovalContract(t *testing.T) {
	s := humanDingCardSpec{Kind: "approval", Title: "Release confirmation", Description: "Confirm staging release only."}
	b, err := humanDingCardPayload("test-track", "fake-robot", "test-actor", s)
	if err != nil {
		t.Fatal(err)
	}
	if b["cardTemplateId"] != humanDingApprovalTemplate || b["openSpaceId"] != "dtv1.card//IM_ROBOT.test-actor" || b["callbackType"] != "STREAM" {
		t.Fatalf("wrong provider contract: %+v", b)
	}
	params := b["cardData"].(map[string]any)["cardParamMap"].(map[string]string)
	if params["title"] != s.Title || params["reason"] != s.Description || params["status"] != "待处理" || params["amount"] != "" {
		t.Fatalf("wrong approval params: %+v", params)
	}
	ex := humanDingCardExpectation{TrackID: "test-track", TenantID: "test-tenant", StaffID: "test-actor", Spec: s}
	for _, row := range []struct{ native, platform, status string }{{"accept", "approve", "已同意"}, {"reject", "reject", "被拒绝"}} {
		data := `{"cardPrivateData":{"actionIds":["0"],"params":{"action":"` + row.native + `"}}}`
		raw := humanDingTestPayload(t, data, ex.TrackID, ex.TenantID, ex.StaffID, "IM")
		answer, err := parseHumanDingCardAnswer(raw, ex)
		if err != nil || answer.Decision != row.platform {
			t.Fatalf("unexpected decision %+v, %v", answer, err)
		}
		terminal, err := humanDingCardTerminalParams(s, answer)
		if err != nil || terminal["status"] != row.status || terminal["reason"] != s.Description {
			t.Fatalf("bad terminal card: %+v, %v", terminal, err)
		}
		for _, bad := range [][]byte{
			humanDingTestPayload(t, data, "other-track", ex.TenantID, ex.StaffID, "IM"),
			humanDingTestPayload(t, data, ex.TrackID, "other-tenant", ex.StaffID, "IM"),
			humanDingTestPayload(t, data, ex.TrackID, ex.TenantID, "other-actor", "IM"),
			humanDingTestPayload(t, data, ex.TrackID, ex.TenantID, ex.StaffID, "IM_GROUP"),
		} {
			if _, err := parseHumanDingCardAnswer(bad, ex); !errors.Is(err, errHumanDingCardAnswer) {
				t.Fatal("callback from unauthorized origin accepted")
			}
		}
	}
	s.Fields = []humanDingCardField{humanDingTestField("text")}
	if _, err := humanDingCardPayload("test-track", "fake-robot", "test-actor", s); !errors.Is(err, errHumanDingCardUnsupported) {
		t.Fatal("approval template silently dropped a form field")
	}
}

func TestHumanDingFormTypesAloneAndMixed(t *testing.T) {
	forms := []struct {
		kind         string
		providerType string
		raw          json.RawMessage
		values       []string
	}{
		{"choice", "SELECT", json.RawMessage(`{"index":0,"value":"opaque-first"}`), []string{"opaque-first"}},
		{"multi_choice", "MULTI_SELECT", json.RawMessage(`{"index":[1,0],"value":["opaque-second","opaque-first"]}`), []string{"opaque-second", "opaque-first"}},
		{"text", "TEXT", json.RawMessage(`"a note"`), []string{"a note"}},
		{"number", "NUMBER", json.RawMessage(`1.25`), []string{"1.25"}},
	}
	mixed := humanDingCardSpec{Kind: "form", Title: "Test title", Description: "Test description"}
	mixedForm := map[string]json.RawMessage{}
	wantMixed := map[string][]string{}
	for i, c := range forms {
		field := humanDingTestField(c.kind)
		t.Run(c.kind, func(t *testing.T) {
			s := humanDingCardSpec{Kind: "form", Title: "Test title", Fields: []humanDingCardField{field}}
			b, err := humanDingCardPayload("test-track", "test-bot", "test-staff", s)
			if err != nil || b["cardTemplateId"] != humanDingFormTemplate {
				t.Fatalf("template failed: %+v, %v", b, err)
			}
			m := b["cardData"].(map[string]any)["cardParamMap"].(map[string]string)
			if m["question_id"] != "test-track" || !strings.Contains(m["form"], `"type":"`+c.providerType+`"`) || m["card_status"] != "pending" {
				t.Fatalf("wrong fields: %+v", m)
			}
			answer := parseHumanDingFormCallback(t, s, map[string]json.RawMessage{"answer_0": c.raw})
			if !reflect.DeepEqual(answer, map[string][]string{field.Name: c.values}) {
				t.Fatalf("bad answer: %+v", answer)
			}
		})
		mixed.Fields = append(mixed.Fields, field)
		mixedForm[humanDingFieldName(i)] = c.raw
		wantMixed[field.Name] = c.values
	}
	if got := parseHumanDingFormCallback(t, mixed, mixedForm); !reflect.DeepEqual(got, wantMixed) {
		t.Fatalf("bad mixed answer: %+v", got)
	}
	terminal, err := humanDingCardTerminalParams(mixed, humanDingCardAnswer{Decision: "submit", Values: wantMixed})
	if err != nil || terminal["card_status"] != "submitted" || terminal["question_desc"] != mixed.Description {
		t.Fatalf("wrong terminal params: %+v %v", terminal, err)
	}
	if printable, e := json.Marshal(terminal); e != nil || strings.Contains(string(printable), "a note") {
		t.Fatal("terminal params leaked a private answer")
	}
}

func parseHumanDingFormCallback(t *testing.T, spec humanDingCardSpec, form map[string]json.RawMessage) map[string][]string {
	t.Helper()
	p := map[string]any{"cardPrivateData": map[string]any{"params": map[string]any{"form": form}}}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	raw := humanDingTestPayload(t, string(data), "test-track", "test-tenant", "test-staff", "IM_ROBOT")
	answer, err := parseHumanDingCardAnswer(raw, humanDingCardExpectation{TrackID: "test-track", TenantID: "test-tenant", StaffID: "test-staff", Spec: spec})
	if err != nil || answer.Decision != "submit" {
		t.Fatalf("unexpected form result: %+v, %v", answer, err)
	}
	return answer.Values
}

func TestHumanDingFormRejectsInvalidOrUnknownValues(t *testing.T) {
	f := humanDingTestField("multi_choice")
	for _, raw := range []string{`["opaque-first","opaque-first"]`, `["First"]`, `{"index":[0]}`, `[1]`, `[]`} {
		data := map[string]json.RawMessage{"answer_0": json.RawMessage(raw)}
		if _, err := humanDingFormAnswer(data, []humanDingCardField{f}); !errors.Is(err, errHumanDingCardAnswer) {
			t.Fatalf("accepted invalid choice %q", raw)
		}
	}
	n := humanDingTestField("number")
	for _, raw := range []string{`"not a number"`, `1e9999`, `"NaN"`, `true`, `null`, `""`} {
		if _, err := humanDingFormAnswer(map[string]json.RawMessage{"answer_0": json.RawMessage(raw)}, []humanDingCardField{n}); !errors.Is(err, errHumanDingCardAnswer) {
			t.Fatalf("accepted invalid number %q", raw)
		}
	}
	n.Required = false
	if _, err := humanDingFormAnswer(map[string]json.RawMessage{"foreign": json.RawMessage(`1`)}, []humanDingCardField{n}); !errors.Is(err, errHumanDingCardAnswer) {
		t.Fatal("unknown optional form entry accepted")
	}
	if answer, err := humanDingFormAnswer(map[string]json.RawMessage{}, []humanDingCardField{n}); err != nil || len(answer.Values) != 0 {
		t.Fatalf("missing optional field should be valid: %+v, %v", answer, err)
	}
}
