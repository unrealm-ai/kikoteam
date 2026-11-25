package channel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

const humanRefreshPath = "/api/internal/v1/human-notices/card-refreshes"

type humanCardRefresh struct {
	ID             string                 `json:"id"`
	LeaseToken     string                 `json:"lease_token"`
	BindingID      string                 `json:"binding_id"`
	Provider       string                 `json:"provider"`
	TenantID       string                 `json:"tenant_id"`
	Generation     int64                  `json:"connection_generation"`
	Revision       int64                  `json:"config_revision"`
	ProviderCardID string                 `json:"provider_card_id"`
	State          string                 `json:"state"`
	Version        int64                  `json:"request_version"`
	Card           *humanConfirmationCard `json:"card"`
}

type humanRefreshWriter func(context.Context, Binding, humanCardRefresh, func() bool) string

func refreshTerminalHumanCards(ctx context.Context, b *Backend, lookup func(string) (Binding, bool), writer humanRefreshWriter) error {
	var batch struct {
		Items []humanCardRefresh `json:"items"`
	}
	if err := b.request(ctx, "POST", humanRefreshPath+"/claim", nil, &batch); err != nil {
		var api *BackendError
		if errors.As(err, &api) && api.Status == http.StatusNotFound {
			return nil // Hub-first upgrade: older Managers did not have refresh.
		}
		return err
	}
	for _, j := range batch.Items {
		if err := runHumanCardRefresh(ctx, b, lookup, writer, j); err != nil {
			return err
		}
	}
	return nil
}

func runHumanCardRefresh(ctx context.Context, b *Backend, lookup func(string) (Binding, bool), writer humanRefreshWriter, j humanCardRefresh) error {
	if !humanCardNoticeID.MatchString(j.ID) || !humanCardNoticeID.MatchString(j.LeaseToken) || j.Version < 1 || j.Card == nil {
		return errors.New("invalid confirmation refresh lease")
	}
	path := humanRefreshPath + "/" + url.PathEscape(j.ID)
	lease := map[string]string{"lease_token": j.LeaseToken}
	binding, ok := lookup(j.BindingID)
	if !ok || !j.matches(binding) {
		return nil
	}
	check := func() bool {
		var authorized humanCardRefresh
		if b.request(ctx, "POST", path+"/authorize", lease, &authorized) != nil || !reflect.DeepEqual(j, authorized) {
			return false
		}
		latest, exists := lookup(j.BindingID)
		return exists && j.matches(latest) && latest.BotOpenID == binding.BotOpenID && latest.Credentials == binding.Credentials
	}
	result := writer(ctx, binding, j, check)
	return b.request(ctx, "POST", path+"/complete", map[string]string{"lease_token": j.LeaseToken, "result": result}, nil)
}

func (j humanCardRefresh) matches(b Binding) bool {
	return (j.Provider == "feishu" || j.Provider == "dingtalk") && b.Provider == j.Provider &&
		j.BindingID == b.ID && b.Enabled && b.TenantID != "" && j.TenantID == b.TenantID &&
		b.Generation == j.Generation && b.Revision == j.Revision && b.Credentials.AppID != "" && b.Credentials.AppSecret != ""
}

func refreshHumanCardProvider(ctx context.Context, b Binding, j humanCardRefresh, check func() bool) string {
	switch j.Provider {
	case "feishu":
		api := lark.NewClient(b.Credentials.AppID, b.Credentials.AppSecret, lark.WithLogger(quietLogger{}), lark.WithReqTimeout(8*time.Second), lark.WithHttpClient(noRedirectTaskDetailHTTP()))
		return refreshFeishuHumanCard(ctx, api, j, check)
	case "dingtalk":
		return refreshDingHumanCard(ctx, b, newDingHTTP(), j, check)
	default:
		return "failed"
	}
}

var feishuHumanRefreshMessage = regexp.MustCompile(`^om_[A-Za-z0-9_-]{1,240}$`)

func refreshFeishuHumanCard(ctx context.Context, api *lark.Client, j humanCardRefresh, check func() bool) string {
	if !feishuHumanRefreshMessage.MatchString(j.ProviderCardID) {
		return "failed"
	}
	decision, terminal := j.decision()
	if !terminal {
		return "failed"
	}
	card := completeFeishuHumanCard(feishuHumanCardSpec{Summary: j.Card.Description}, decision, "")
	payload, err := json.Marshal(card.Data)
	if err != nil || !check() {
		return "unknown"
	}
	resp, err := api.Im.Message.Patch(ctx, larkim.NewPatchMessageReqBuilder().MessageId(j.ProviderCardID).
		Body(larkim.NewPatchMessageReqBodyBuilder().Content(string(payload)).Build()).Build())
	if err != nil || resp == nil || resp.ApiResp == nil {
		return "unknown"
	}
	var evidence struct {
		Code *int `json:"code"`
	}
	known := json.Unmarshal(resp.RawBody, &evidence) == nil && evidence.Code != nil
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && known && *evidence.Code == 0 {
		return "updated"
	}
	return humanRefreshHTTPFailure(resp.StatusCode, known)
}

func refreshDingHumanCard(ctx context.Context, b Binding, h dingHTTP, j humanCardRefresh, check func() bool) string {
	if j.ProviderCardID != j.ID || !humanCardNoticeID.MatchString(j.ID) {
		return "failed"
	}
	decision, terminal := j.decision()
	if !terminal {
		return "failed"
	}
	if decision == "information" {
		decision = "submit"
	}
	params, err := humanDingCardTerminalParams(canonicalDingHumanCard(j.Card), humanDingCardAnswer{Decision: decision})
	if err != nil {
		return "failed"
	}
	token, err := h.token(ctx, b)
	if err != nil || !check() {
		return "unknown"
	}
	body := map[string]any{"outTrackId": j.ProviderCardID, "cardData": map[string]any{"cardParamMap": params},
		"cardUpdateOptions": map[string]bool{"updateCardDataByKey": true}}
	resp, err := h.do(ctx, http.MethodPut, "/v1.0/card/instances", token, body)
	if err != nil {
		return "unknown"
	}
	defer resp.Body.Close() //nolint:errcheck // Read-only cleanup.
	var out struct {
		Success *bool  `json:"success"`
		Result  *bool  `json:"result"`
		Code    string `json:"code"`
	}
	known := decodeDingResponse(resp.Body, &out) == nil
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && known && out.Success != nil && *out.Success && out.Result != nil && *out.Result {
		return "updated"
	}
	explicitFailure := known && (out.Code != "" || out.Success != nil && !*out.Success || out.Result != nil && !*out.Result)
	return humanRefreshHTTPFailure(resp.StatusCode, explicitFailure)
}

func humanRefreshHTTPFailure(status int, proved bool) string {
	if proved && (status >= 200 && status < 300 || status == 400 || status == 401 || status == 403 || status == 404 || status == 410) {
		return "failed"
	}
	return "unknown"
}

func (j humanCardRefresh) decision() (string, bool) {
	switch j.State {
	case "approved":
		if j.Card.DecisionRequired {
			return "approve", true
		}
		return "information", true
	case "rejected":
		return "reject", true
	case "revised", "cancelled":
		return "", true
	default:
		return "", false
	}
}
