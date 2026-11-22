package feishusetup

import (
	"context"
	"encoding/json"
	"net/url"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

// ExpandContactRange is setup-only. The guard runs after authentication and
// immediately before dispatching the single write to this newly-created app.
func (c *Client) ExpandContactRange(ctx context.Context, id, secret string, beforeWrite func() error) error {
	if !appID.MatchString(id) {
		return failure("feishu_invalid_registration")
	}
	body, err := json.Marshal(map[string]string{"app_id": id, "app_secret": secret})
	if err != nil {
		return err
	}
	m, err := c.request(ctx, "POST", api+"/auth/v3/tenant_access_token/internal", "application/json", "", body)
	if err != nil {
		return err
	}
	token := str(m, "tenant_access_token")
	if !success(m) || token == "" {
		return failure("feishu_authentication_failed")
	}
	if err = beforeWrite(); err != nil {
		return err
	}
	m, err = c.request(ctx, "PATCH", api+"/application/v6/applications/"+url.PathEscape(id)+"/contacts_range", "application/json", token, []byte(`{"contacts_range_type":"all"}`))
	if err != nil {
		return err
	}
	if !success(m) {
		switch number(m, "code") {
		case 10003, 10014, 20002, 20036, 20048, 20069, 20130, 99991661, 99991663, 99991664, 99991668:
			return failure("feishu_authentication_failed")
		case 40003, 40004, 41050, 41056, 1184001, 99991672, 99991676, 99991679, 99991680, 99992361, 99992362:
			return failure("feishu_authorization_denied")
		}
		return failure("feishu_contact_range_update_failed")
	}
	return nil
}

func (c *Client) InspectPermissions(ctx context.Context, id, secret string) (service.FeishuDiagnostics, error) {
	d := service.FeishuDiagnostics{Permissions: service.FeishuPermissions(), ScopeStatus: "unknown"}
	body, err := json.Marshal(map[string]string{"app_id": id, "app_secret": secret})
	if err != nil {
		return d, err
	}
	m, err := c.request(ctx, "POST", api+"/auth/v3/tenant_access_token/internal", "application/json", "", body)
	if err != nil {
		return d, err
	}
	token := str(m, "tenant_access_token")
	if !success(m) || token == "" {
		d.ErrorCode = "feishu_authentication_failed"
		return d, nil
	}
	scopes, err := c.get(ctx, "/application/v6/scopes", token)
	if err != nil {
		return d, err
	}
	inspectScopes(&d, scopes)
	return d, nil
}
