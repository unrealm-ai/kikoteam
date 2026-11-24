package channel

import (
	"context"
	"errors"
	"net/url"
	"path"
	"time"
)

// Retry the exact completion fact, not the provider send. The short cleanup
// context lets a real late response survive cancellation of the worker scope.
func submitDeliveryReceipt(ctx context.Context, b *Backend, endpoint string, receipt deliveryReceipt) error {
	id := path.Base(endpoint)
	kind := b.deliveryKind()
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 12*time.Second)
	defer cancel()
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		last = b.request(finish, "POST", endpoint+"/complete", receipt, nil)
		outcome := "failed"
		if last == nil {
			outcome = "persisted"
		}
		b.trace(finish, "channel.delivery.receipt_persist", "delivery_kind", kind, "delivery_id", traceID(id), "attempt", attempt+1, "status", traceID(receipt.Status), "outcome", outcome)
		if last == nil {
			return nil
		}
		var rejected *BackendError
		if errors.As(last, &rejected) && rejected.Status >= 400 && rejected.Status < 500 {
			return last
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 200 * time.Millisecond)
		select {
		case <-finish.Done():
			timer.Stop()
			return last
		case <-timer.C:
		}
	}
	return last
}
func reconcileDeliveryQueries(ctx context.Context, b *Backend, lookup func(string) (Binding, bool), query deliverySender) error {
	var batch struct {
		Items []Delivery `json:"items"`
	}
	if err := b.request(ctx, "POST", b.deliveryQueryPath()+"/claim", map[string]int{"limit": 1}, &batch); err != nil {
		return err
	}
	for _, d := range batch.Items {
		b.traceDelivery(ctx, "channel.delivery.query", d, "claimed")
		if ctx.Err() != nil {
			return ctx.Err()
		}
		current, ok := lookup(d.BindingID)
		receipt := deliveryReceipt{Status: "unknown", ProviderRequestID: d.ProviderRequestID, ErrorCode: "confirmation_binding_unavailable"}
		// Reconciliation does not send content or require current user authorization.
		// Never query a different credential generation or a different provider.
		if ok && current.Generation == d.Generation && current.Provider == "dingtalk" && d.Provider == "dingtalk" && current.TenantID == d.TenantID && current.Credentials.AppID != "" && current.Credentials.AppSecret != "" {
			d.BeforeDispatch = func() error {
				if err := ctx.Err(); err != nil {
					return err
				}
				latest, exists := lookup(d.BindingID)
				if !exists || latest.Generation != current.Generation || latest.Provider != current.Provider || latest.TenantID != current.TenantID || latest.Credentials.AppID != current.Credentials.AppID || latest.Credentials.AppSecret != current.Credentials.AppSecret {
					return errors.New("confirmation binding changed")
				}
				return nil
			}
			b.traceDelivery(ctx, "channel.delivery.query", d, "started")
			receipt = query(ctx, current, d)
		}
		b.traceDelivery(ctx, "channel.delivery.query_result", d, receipt.Status)
		receipt.LeaseToken = d.LeaseToken
		receipt.TargetDigest = d.TargetDigest
		receipt.QueryToken = d.QueryToken
		if d.Presentation == taskDetailCardFormat || d.Presentation == taskDetailLinkFormat {
			receipt.Presentation = d.Presentation
		}
		if err := submitDeliveryReceipt(ctx, b, b.deliveryPath()+"/"+url.PathEscape(d.ID), receipt); err != nil {
			return err
		}
	}
	return nil
}
