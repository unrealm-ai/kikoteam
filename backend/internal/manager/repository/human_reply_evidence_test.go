package repository

import (
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func TestHumanReplyEvidenceSupportsDistinctRequestsInOneInbound(t *testing.T) {
	a := service.HumanRequest{ID: service.NewID(), Version: 1}
	b := service.HumanRequest{ID: service.NewID(), Version: 1}
	a.Code = service.HumanShortCode(a.ID, a.Version)
	b.Code = service.HumanShortCode(b.ID, b.Version)
	original := a.Code + " 按原方案做；" + b.Code + " 填周五"
	reply := humanReplyContext{
		Text: original, Segments: humanSegments("current-input", original), QuotePresent: true,
		QuotedRequests: []humanReplyRef{{RequestID: a.ID, Version: 1}},
	}
	proposal := service.HumanAnswer{Association: "current_message", Decision: "information"}
	for i, request := range []service.HumanRequest{a, b} {
		selected, err := humanSelectedEvidence(reply, []string{reply.Segments[i].ID})
		if err != nil {
			t.Fatal(err)
		}
		if err := verifyHumanReplyAssociation(reply, request, proposal, selected); err != nil {
			t.Fatal(err)
		}
	}
	if err := verifyHumanReplyAssociation(reply, b, proposal, reply.Segments[0].Text); err == nil {
		t.Fatal("evidence naming another request was accepted")
	}
	if _, err := humanSelectedEvidence(reply, []string{"invented-segment"}); err == nil {
		t.Fatal("evidence outside the current inbound was accepted")
	}
}

func TestHumanClarificationRequiresOneMatchingVisibleFocus(t *testing.T) {
	request := service.HumanRequest{ID: service.NewID(), Version: 2, QuestionDigest: "current"}
	answer := service.HumanAnswer{Association: "clarification", Decision: "approve"}
	ref := humanReplyRef{RequestID: request.ID, Version: 2, Digest: request.QuestionDigest}
	for _, tc := range []struct {
		name  string
		focus []humanReplyRef
		valid bool
	}{
		{name: "missing"},
		{name: "unambiguous", focus: []humanReplyRef{ref}, valid: true},
		{name: "multiple", focus: []humanReplyRef{ref, {RequestID: service.NewID(), Version: 2}}},
		{name: "older version", focus: []humanReplyRef{{RequestID: request.ID, Version: 1}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := verifyHumanReplyAssociation(humanReplyContext{ClarificationFocus: tc.focus}, request, answer, "同意")
			if (err == nil) != tc.valid {
				t.Fatalf("clarification validation = %v, want valid %t", err, tc.valid)
			}
		})
	}
}
