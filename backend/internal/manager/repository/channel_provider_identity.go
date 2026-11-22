package repository

import (
	"context"
	"database/sql"
	"strings"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

// Only the Hub's authenticated provider connection may submit this internal
// envelope. Configuration and a connected transport alone never confirm tenant.
func verifyChannelInboundIdentity(ctx context.Context, tx *sql.Tx, v service.ChannelInbound) (string, int64, error) {
	var provider, tenant, bot, app string
	var epoch, generation, revision, rules int64
	var enabled bool
	err := tx.QueryRowContext(ctx, `SELECT provider,identity_epoch,connection_generation,config_revision,rules_version,enabled,external_tenant_id,bot_open_id,app_id FROM platform_channel_bindings WHERE id=$1 FOR UPDATE`, v.BindingID).Scan(&provider, &epoch, &generation, &revision, &rules, &enabled, &tenant, &bot, &app)
	if err != nil {
		return provider, epoch, visibleError(err)
	}
	if v.ConnectionGeneration != generation {
		return provider, epoch, service.Fail("binding_generation_stale", 409, "渠道连接代次已过期")
	}
	if provider == "feishu" {
		return provider, epoch, nil
	}
	if provider != "dingtalk" && provider != "wecom" {
		return provider, epoch, service.Fail("invalid_argument", 400, "渠道未接入")
	}
	if !enabled || v.ConfigRevision != revision || v.RulesVersion != rules {
		return provider, epoch, service.Fail("binding_generation_stale", 409, "渠道配置已变化")
	}
	if !validProviderIdentity(provider, app, v) || tenant != "" && tenant != v.ExternalTenantID || bot != "" && bot != v.BotID {
		return provider, epoch, service.Fail("channel_identity_conflict", 409, "渠道事件身份不匹配")
	}
	_, err = tx.ExecContext(ctx, `UPDATE platform_channel_bindings SET external_tenant_id=$2,bot_open_id=$3,identity_verified_generation=$4 WHERE id=$1`, v.BindingID, v.ExternalTenantID, v.BotID, generation)
	if err != nil {
		return provider, epoch, err
	}
	if provider == "dingtalk" && tenant == "" {
		// This directory already belongs to the same app and credential generation.
		// Attach the newly confirmed tenant without invalidating the snapshot or its lease.
		_, err = tx.ExecContext(ctx, `UPDATE platform_channel_directory_state SET external_tenant_id=$2
			WHERE binding_id=$1 AND identity_epoch=$3 AND connection_generation=$4 AND external_tenant_id=''`,
			v.BindingID, v.ExternalTenantID, epoch, generation)
	}
	return provider, epoch, err
}
func validProviderIdentity(provider, app string, v service.ChannelInbound) bool {
	if v.BotID == "" || len(v.BotID) > 200 || strings.ContainsAny(v.BotID, "\r\n\x00") || v.SenderIDType != "user_id" || v.ThreadID != "" || !supportedChannelInput(provider, v.MessageType) && !nativeChannelMedia(provider, v.MessageType, v.Media) {
		return false
	}
	if v.ChatType == "group" && !v.BotMentioned && (provider != "wecom" || !v.ProviderDirected) {
		return false
	}
	if provider == "wecom" {
		return v.BotID == app && v.ExternalTenantID == app
	}
	return v.SenderTenant == v.ExternalTenantID && !v.ProviderDirected
}
func nativeChannelMedia(provider, messageType string, media []service.MediaReference) bool {
	if len(media) != 1 {
		return false
	}
	switch messageType {
	case "picture":
		return provider == "dingtalk" && media[0].Kind == "image"
	case "image":
		return (provider == "dingtalk" || provider == "wecom") && media[0].Kind == "image"
	case "file":
		return (provider == "dingtalk" || provider == "wecom") && media[0].Kind == "file"
	}
	return false
}
func channelInboundTriggerMatches(provider string, trigger []byte, text string, mentioned, directed bool) (bool, error) {
	if provider != "feishu" {
		return mentioned || provider == "wecom" && directed, nil
	}
	return channelTriggerMatches(trigger, text, mentioned)
}
