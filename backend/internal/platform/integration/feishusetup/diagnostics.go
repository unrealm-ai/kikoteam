package feishusetup

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/integration/feishuapi"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/integration/feishudirectory"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func (c *Client) get(ctx context.Context, path, token string) (map[string]json.RawMessage, error) {
	m, err := c.request(ctx, "GET", api+path, "", token, nil)
	if err != nil {
		return nil, err
	}
	if !success(m) {
		return nil, failure("feishu_permission_or_identity_denied")
	}
	data := object(m, "data")
	if data == nil {
		return nil, failure("feishu_invalid_response")
	}
	return data, nil
}
func (c *Client) Diagnose(ctx context.Context, id, secret string) (service.FeishuDiagnostics, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	d := service.FeishuDiagnostics{CheckedAt: time.Now().UTC(), CredentialStatus: "unknown", BotStatus: "unknown", DirectoryStatus: "unknown", ScopeStatus: "unknown", Permissions: service.FeishuPermissions()}
	body, err := json.Marshal(map[string]string{"app_id": id, "app_secret": secret})
	if err != nil {
		return d, err
	}
	m, err := c.request(ctx, "POST", api+"/auth/v3/tenant_access_token/internal", "application/json", "", body)
	token := str(m, "tenant_access_token")
	if err != nil {
		d.ErrorCode = "feishu_auth_unavailable"
		d.ErrorDetail = feishuapi.ErrorDetail(number(m, "code"), str(m, "msg"), secret, token)
		if number(m, "code") == 21001 {
			d.CredentialStatus = "failed"
			d.ErrorCode = "feishu_authentication_failed"
		}
		return d, nil
	}
	if !success(m) || token == "" {
		d.CredentialStatus = "failed"
		d.ErrorCode = "feishu_authentication_failed"
		d.ErrorDetail = feishuapi.ErrorDetail(number(m, "code"), str(m, "msg"), secret, token)
		return d, nil
	}
	d.CredentialStatus = "pass"
	// bot/v3/info has a top-level bot object (unlike Contact v3 data envelopes).
	bot, e := c.request(ctx, "GET", api+"/bot/v3/info", "", token, nil)
	if e == nil {
		if success(bot) {
			d.BotID = str(object(bot, "bot"), "open_id")
		}
	}
	if d.BotID != "" {
		d.BotStatus = "pass"
	} else {
		d.BotStatus = "failed"
	}
	tenant, e := c.get(ctx, "/tenant/v2/tenant/query", token)
	if e == nil {
		d.TenantID = str(object(tenant, "tenant"), "tenant_key")
	} // Never substitute display_id.
	scopes, e := c.get(ctx, "/application/v6/scopes", token)
	if e == nil {
		inspectScopes(&d, scopes)
	}
	c.probeDirectory(ctx, token, &d)
	d.Ready = d.BotStatus == "pass" && d.DirectoryStatus == "pass" && d.ScopeStatus == "pass"
	for _, p := range d.Permissions {
		if p.Required && p.Status != "granted" {
			d.Ready = false
		}
	}
	if !d.Ready && d.ErrorCode == "" {
		d.ErrorCode = "feishu_permissions_or_configuration_required"
	}
	return d, nil
}
func (c *Client) probeDirectory(ctx context.Context, token string, d *service.FeishuDiagnostics) {
	client := feishudirectory.Client{HTTPClient: &http.Client{Transport: c.Transport, Timeout: 15 * time.Second}}
	err := client.ProbeScope(ctx, token)
	var provider *service.DirectoryFetchError
	if errors.As(err, &provider) {
		d.ErrorDetail = provider.Detail
		if d.ErrorDetail == "" {
			d.ErrorDetail = provider.Code
		}
	}
	switch {
	case err == nil:
		d.DirectoryStatus = "pass"
	case provider != nil && provider.Code == "directory_access_denied":
		d.DirectoryStatus = "failed"
	default:
		// An unavailable or malformed provider response says nothing about grants.
		d.ErrorCode = "feishu_directory_check_unavailable"
	}
}

func inspectScopes(d *service.FeishuDiagnostics, m map[string]json.RawMessage) {
	var more bool
	if json.Unmarshal(m["has_more"], &more) == nil && more {
		return
	}
	var rows []struct {
		Scope    string `json:"scope_name"`
		Identity string `json:"scope_type"`
		Granted  *int   `json:"grant_status"`
	}
	if json.Unmarshal(m["scopes"], &rows) != nil || rows == nil || len(rows) > 10000 {
		return
	}
	seen := map[string]string{}
	for _, r := range rows {
		if r.Identity != "tenant" {
			continue
		}
		state := "unknown"
		if r.Granted != nil {
			switch *r.Granted {
			case 1:
				state = "granted"
			case 2:
				state = "missing"
			}
		}
		if old, ok := seen[r.Scope]; ok && old != state {
			state = "unknown"
		}
		seen[r.Scope] = state
	}
	d.ScopeStatus = "pass"
	for n := range d.Permissions {
		if state, ok := seen[d.Permissions[n].Scope]; ok {
			d.Permissions[n].Status = state
		}
	}
}
