package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type packageConversationSource struct {
	channel, binding, person, group string
	epoch                           int64
	otherBindingEpochs              map[string]int64
}

// PackageConversationScopeAllows applies only the package restriction. Its
// caller must verify employee admission and the actual runtime Input first.
func (t *packageTx) PackageConversationScopeAllows(ctx context.Context, p service.CapabilityPackage, actor string) (bool, error) {
	if !p.Enabled || p.ConversationScope == nil || actor == "" {
		return false, nil
	}
	source, err := t.packageConversationSource(ctx, p.AgentID, actor)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	rule := p.ConversationScope.Private
	if source.group != "" {
		rule = p.ConversationScope.Group
		// Any still-current group/person entry determines whether its category
		// participates in the whitelist, even if it belongs to another channel.
		if rule.Enabled && rule.Mode == "allowlist" && slices.ContainsFunc(rule.Allow, func(ref service.UsageSelector) bool {
			return ref.BindingID != "" && ref.BindingID != source.binding
		}) {
			source.otherBindingEpochs, err = t.currentPackageBindingEpochs(ctx, p.AgentID)
			if err != nil {
				return false, err
			}
		}
	}
	return t.packageConversationRuleAllows(ctx, source, rule)
}

func (t *packageTx) currentPackageBindingEpochs(ctx context.Context, agent string) (map[string]int64, error) {
	rows, err := t.model.tx.QueryContext(ctx, `SELECT id,identity_epoch FROM platform_channel_bindings WHERE workspace_id=$1 AND agent_id=$2`, t.model.workspace, agent)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // Iteration errors are returned below.
	epochs := make(map[string]int64)
	for rows.Next() {
		var id string
		var epoch int64
		if err := rows.Scan(&id, &epoch); err != nil {
			return nil, err
		}
		epochs[id] = epoch
	}
	return epochs, rows.Err()
}

func (t *packageTx) packageConversationSource(ctx context.Context, employee, actor string) (packageConversationSource, error) {
	source := packageConversationSource{channel: "web", person: actor}
	if _, trigger := service.TriggerExecutionFrom(ctx); trigger {
		return source, sql.ErrNoRows
	}
	c, channel := service.ChannelExecutionFrom(ctx)
	g, webGroup := service.WebGroupExecutionFrom(ctx)
	if channel && webGroup {
		return source, sql.ErrNoRows
	}
	if channel {
		if actor != c.PrincipalID {
			return source, sql.ErrNoRows
		}
		err := t.model.tx.QueryRowContext(ctx, `SELECT b.provider,b.id,b.identity_epoch,p.external_user_id,
 CASE WHEN c.channel_chat_type='group' THEN c.external_chat_id ELSE '' END
 FROM manager_conversations c
 JOIN manager_channel_principals p ON p.id=$1 AND p.binding_id=c.channel_binding_id AND p.workspace_id=c.workspace_id
 JOIN platform_channel_bindings b ON b.id=p.binding_id AND b.workspace_id=c.workspace_id AND b.agent_id=c.agent_id
 WHERE c.id=$2 AND c.workspace_id=$3 AND c.agent_id=$4 AND c.lifecycle='active'
 AND c.channel_chat_type IN ('direct','group') AND c.external_chat_id<>''
 AND b.enabled AND b.connection_generation=$5 AND b.provider=p.provider
 AND b.identity_epoch=p.identity_epoch AND b.identity_epoch=c.channel_identity_epoch
 AND b.external_tenant_id<>'' AND p.external_tenant_id=b.external_tenant_id AND c.channel_tenant_id=b.external_tenant_id
 AND (b.provider='feishu' OR b.identity_verified_generation=b.connection_generation)`,
			actor, c.ConversationID, t.model.workspace, employee, c.Generation).Scan(&source.channel, &source.binding, &source.epoch, &source.person, &source.group)
		return source, err
	}
	if webGroup {
		err := t.model.tx.QueryRowContext(ctx, `SELECT c.web_group_id
 FROM manager_conversations c JOIN platform_web_groups g ON g.id=c.web_group_id AND g.workspace_id=c.workspace_id AND g.status='active'
 WHERE c.id=$1 AND c.workspace_id=$2 AND c.agent_id=$3 AND c.channel_binding_id IS NULL
 AND c.lifecycle='active' AND manager_web_group_actor_allowed(c.id,$4)`,
			g.ConversationID, t.model.workspace, employee, actor).Scan(&source.group)
		return source, err
	}
	return source, nil
}

type packageListMatch struct {
	personConfigured, groupConfigured bool
	personMatched, groupMatched       bool
}

func (m packageListMatch) allows() bool {
	return (m.personConfigured || m.groupConfigured) &&
		(!m.personConfigured || m.personMatched) && (!m.groupConfigured || m.groupMatched)
}

type packageDepartmentRule struct {
	ID                 string `json:"department_id"`
	IncludeDescendants bool   `json:"include_descendants"`
}

func packageListMatches(src packageConversationSource, refs []service.UsageSelector) (packageListMatch, []packageDepartmentRule) {
	var result packageListMatch
	var departments []packageDepartmentRule
	for _, ref := range refs {
		epoch, known := src.otherBindingEpochs[ref.BindingID]
		if ref.BindingID != "" && ref.BindingID == src.binding {
			epoch, known = src.epoch, true
		}
		if known && ref.IdentityEpoch < epoch {
			continue
		}
		if ref.Kind == "group" {
			result.groupConfigured = true
		} else {
			result.personConfigured = true
		}
		if ref.Channel != src.channel || ref.BindingID != src.binding {
			continue
		}
		if ref.IdentityEpoch != src.epoch {
			continue
		}
		switch ref.Kind {
		case "user":
			result.personMatched = result.personMatched || ref.ID == src.person
		case "group":
			result.groupMatched = result.groupMatched || (src.group != "" && ref.ID == src.group)
		case "department":
			departments = append(departments, packageDepartmentRule{ref.ID, ref.IncludeDescendants})
		}
	}
	return result, departments
}

func (t *packageTx) packageConversationRuleAllows(ctx context.Context, src packageConversationSource, rule service.PackageConversationRule) (bool, error) {
	if !rule.Enabled {
		return false, nil
	}
	switch rule.Mode {
	case "inherit_with_blacklist":
		hits, departments := packageListMatches(src, rule.Deny)
		if hits.personMatched || hits.groupMatched {
			return false, nil
		}
		match, err := t.packageDepartmentMatch(ctx, src, departments)
		return err == nil && match.Valid && !match.Bool, err
	case "allowlist":
		if len(rule.Deny) != 0 {
			return false, nil
		}
		hits, departments := packageListMatches(src, rule.Allow)
		if !hits.personMatched && (!hits.groupConfigured || hits.groupMatched) {
			match, err := t.packageDepartmentMatch(ctx, src, departments)
			if err != nil {
				return false, err
			}
			hits.personMatched = match.Valid && match.Bool
		}
		return hits.allows(), nil
	default:
		return false, nil
	}
}

func (t *packageTx) packageDepartmentMatch(ctx context.Context, src packageConversationSource, rules []packageDepartmentRule) (sql.NullBool, error) {
	if len(rules) == 0 {
		return sql.NullBool{Valid: true}, nil
	}
	if src.binding == "" {
		return sql.NullBool{}, nil
	}
	raw, err := json.Marshal(rules)
	if err != nil {
		return sql.NullBool{}, err
	}
	var match sql.NullBool
	err = t.model.tx.QueryRowContext(ctx, `SELECT platform_channel_directory_department_match($1,$2,$3::jsonb)`, src.binding, src.person, raw).Scan(&match)
	return match, err
}
