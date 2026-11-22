package repository

import (
	"strings"
	"testing"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func TestDeliveryReceiptAcceptsFullProviderRequestKey(t *testing.T) {
	v := service.DeliveryCompletion{LeaseToken: service.NewID(), TargetDigest: strings.Repeat("a", 64), Status: "accepted", ProviderRequestID: strings.Repeat("q", 2048), EvidenceType: "send_response", EvidenceID: strings.Repeat("q", 2048)}
	if err := validateDeliveryReceipt(v); err != nil || !deliveryEvidence("dingtalk", v) {
		t.Fatal(err)
	}
	v.ProviderRequestID += "q"
	if validateDeliveryReceipt(v) == nil {
		t.Fatal("oversize key accepted")
	}
	v.ProviderRequestID = "request\nforged"
	if validateDeliveryReceipt(v) == nil {
		t.Fatal("invalid key accepted")
	}
}
