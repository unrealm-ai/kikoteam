package service

import (
	"context"
)

type WebAccess struct {
	Mode           string   `json:"mode"`
	AllowMemberIDs []string `json:"allow_member_ids"`
	DenyMemberIDs  []string `json:"deny_member_ids"`
}
type TemplateOrigin struct {
	TemplateID        string         `json:"template_id"`
	TemplateVersionID string         `json:"template_version_id"`
	Parameters        map[string]any `json:"parameters"`
}

func validateWebAccess(ctx context.Context, t AgentTx, v WebAccess) error {
	if v.AllowMemberIDs == nil || v.DenyMemberIDs == nil {
		return Fail("invalid_web_access", 400, "请提供允许和排除成员列表")
	}
	if (v.Mode != "workspace" && v.Mode != "allowlist") || len(v.AllowMemberIDs) > 100 || len(v.DenyMemberIDs) > 100 {
		return Fail("invalid_web_access", 400, "使用范围无效")
	}
	for _, ids := range [][]string{v.AllowMemberIDs, v.DenyMemberIDs} {
		seen := map[string]bool{}
		for _, id := range ids {
			if !agentUUID(id) || seen[id] {
				return Fail("invalid_web_access", 400, "成员标识无效")
			}
			seen[id] = true
			m, e := t.Member(ctx, id)
			if e != nil || m.Status != "active" {
				return Fail("invalid_web_access", 400, "成员不在当前工作区")
			}
		}
	}
	return nil
}
