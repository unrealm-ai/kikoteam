package repository

import (
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func TestTriggerResultReceiptPresentation(t *testing.T) {
	card, link := service.TaskDetailCardPresentation, service.TaskDetailLinkPresentation
	for _, tc := range []struct {
		name, offered, stored, query, expected  string
		cardAuthorized, linkAuthorized, invalid bool
	}{
		{name: "confirmed_card", offered: card, expected: card, cardAuthorized: true, linkAuthorized: true},
		{name: "confirmed_fallback", offered: link, expected: link, cardAuthorized: true, linkAuthorized: true},
		{name: "legacy_confirmed_link", offered: link, expected: link, linkAuthorized: true},
		{name: "unlinked_reminder", expected: ""},
		{name: "missing_link_grant", offered: link, invalid: true},
		{name: "legacy_cannot_report_card", offered: card, linkAuthorized: true, invalid: true},
		{name: "old_query_inherits", stored: card, query: "query", expected: card, cardAuthorized: true, linkAuthorized: true},
		{name: "query_cannot_replace", offered: link, stored: card, query: "query", cardAuthorized: true, linkAuthorized: true, invalid: true},
		{name: "no_invented_delivery_type", cardAuthorized: true, linkAuthorized: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := service.DeliveryCompletion{Presentation: tc.offered, QueryToken: tc.query}
			err := normalizeTriggerPresentation(&v, tc.stored, tc.cardAuthorized, tc.linkAuthorized)
			if (err != nil) != tc.invalid || (!tc.invalid && v.Presentation != tc.expected) {
				t.Fatalf("presentation=%q error=%v", v.Presentation, err)
			}
		})
	}
}
