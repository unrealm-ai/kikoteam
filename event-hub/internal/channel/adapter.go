package channel

import (
	"context"
	"errors"
)

// Provider capabilities are explicit: unsupported history/topics never fall back
// to another conversation or another provider.
func supportedProvider(provider string) bool {
	return provider == "feishu" || provider == "dingtalk" || provider == "wecom"
}
func RunProvider(ctx context.Context, b Binding, backend *Backend) error {
	switch b.Provider {
	case "feishu":
		return Feishu(ctx, b, backend)
	case "dingtalk":
		return DingTalk(ctx, b, backend)
	case "wecom":
		return WeCom(ctx, b, backend)
	default:
		return errors.New("unsupported channel provider")
	}
}
func sendProviderDelivery(ctx context.Context, b Binding, d Delivery) deliveryReceipt {
	switch b.Provider {
	case "feishu":
		return sendFeishuDelivery(ctx, b, d)
	case "dingtalk":
		return sendDingTalkDelivery(ctx, b, d)
	case "wecom":
		return sendWeComDelivery(ctx, b, d)
	default:
		return deliveryReceipt{Status: "failed", EvidenceType: "not_dispatched", ErrorCode: "unsupported_channel_provider"}
	}
}
