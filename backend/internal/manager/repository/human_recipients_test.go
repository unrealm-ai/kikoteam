package repository

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	platform "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestPublicHumanRecipientsFallback(t *testing.T) {
	for _, tc := range []struct {
		name      string
		scope     string
		isDefault bool
		wantItems int
	}{
		{name: "fallback only", isDefault: true},
		{name: "scoped fallback", scope: "财务", isDefault: true, wantItems: 1},
		{name: "no fallback", scope: "财务", wantItems: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ref := platform.SelectionRef{Provider: "web", ExternalID: "not-for-the-model"}
			rules := service.HumanRecipients{Version: 3, Recipients: []service.HumanRecipient{
				{ID: "candidate", Name: "确认人", Scope: tc.scope, IsDefault: tc.isDefault, Ref: ref},
			}}
			raw, err := json.Marshal(publicHumanRecipients(rules))
			if err != nil {
				t.Fatal(err)
			}
			var response struct {
				Version string              `json:"version"`
				Items   []map[string]string `json:"items"`
				Default map[string]string   `json:"default_recipient"`
			}
			if err := json.Unmarshal(raw, &response); err != nil {
				t.Fatal(err)
			}
			want := map[string]string{"candidateId": "candidate", "questionScope": tc.scope}
			if response.Version != "3" || len(response.Items) != tc.wantItems ||
				(tc.wantItems > 0 && !reflect.DeepEqual(response.Items[0], want)) {
				t.Fatalf("unexpected candidate list: %s", raw)
			}
			if tc.isDefault && !reflect.DeepEqual(response.Default, want) ||
				!tc.isDefault && response.Default != nil {
				t.Fatalf("unexpected fallback recipient: %s", raw)
			}
		})
	}
}
