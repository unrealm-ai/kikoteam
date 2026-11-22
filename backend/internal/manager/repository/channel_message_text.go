package repository

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

var channelMentionToken = regexp.MustCompile(`@_user_[A-Za-z0-9_]+`)
var channelLeadingMention = regexp.MustCompile(`^[ \t]*(@_user_[A-Za-z0-9_]+)[ \t]*`)

func validChannelMetadata(m service.ChannelMessageMetadata) bool {
	if len(m.SenderName) > 1024 || len(m.SenderTenant) > 256 || len(m.SenderIDType) > 64 || len(m.Mentions) > 100 {
		return false
	}
	for _, mention := range m.Mentions {
		if len(mention.ID) > 256 || len(mention.Key) > 256 || len(mention.Name) > 1024 || len(mention.IDType) > 64 || len(mention.TenantKey) > 256 || strings.ContainsRune(mention.ID+mention.Key+mention.Name+mention.IDType+mention.TenantKey, 0) {
			return false
		}
	}
	data, err := json.Marshal(m)
	return err == nil && len(data) <= 32768 && !strings.ContainsRune(m.SenderName+m.SenderTenant+m.SenderIDType, 0)
}

func channelLabel(value string) string {
	value = strings.Map(func(r rune) rune {
		if r == '[' || r == ']' || r < 32 || r == 127 {
			return ' '
		}
		return r
	}, value)
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) > 160 {
		value = string(runes[:160])
	}
	return strings.TrimSpace(value)
}

type mentionValue struct {
	identity, name string
	valid          bool
}

func groupMentionText(text string, mentions []service.ChannelMention) (string, string) {
	mapping := map[string]mentionValue{}
	for _, m := range mentions {
		identity := fmt.Sprintf("%q:%q:%q", m.TenantKey, m.IDType, m.ID)
		name := channelLabel(m.Name)
		next := mentionValue{identity: identity, name: name, valid: m.ID != "" && name != ""}
		if previous, ok := mapping[m.Key]; ok && (!previous.valid || !next.valid || previous.identity != next.identity || previous.name != next.name) {
			next.valid = false
		}
		mapping[m.Key] = next
	}
	names := []string{}
	seen := map[string]bool{}
	for _, token := range channelMentionToken.FindAllString(text, -1) {
		v := mapping[token]
		if v.valid && !seen[v.identity] {
			seen[v.identity] = true
			names = append(names, strings.ReplaceAll(v.name, ",", "，"))
		}
	}
	for {
		match := channelLeadingMention.FindStringSubmatchIndex(text)
		if match == nil || !mapping[text[match[2]:match[3]]].valid {
			break
		}
		text = strings.TrimLeftFunc(text[match[1]:], unicode.IsSpace)
	}
	text = channelMentionToken.ReplaceAllStringFunc(text, func(token string) string {
		if v := mapping[token]; v.valid {
			return "@" + v.name
		}
		return token
	})
	if len(names) == 0 {
		return "", text
	}
	return "[被@人=" + strings.Join(names, ",") + "]", text
}

func renderGroupMessage(speaker, text string, mentions []service.ChannelMention) string {
	header, body := groupMentionText(text, mentions)
	return "[说话人=" + channelLabel(speaker) + "]" + header + body
}

func renderSystemMessage(text string, mentions []service.ChannelMention) string {
	header, body := groupMentionText(strings.TrimPrefix(text, "[系统消息]"), mentions)
	return "[系统消息]" + header + body
}

// Names only affect presentation. Authorization always uses the original IDs.
func (t *sessionTx) channelPresentation(sender string, m service.ChannelMessageMetadata, includeSpeaker bool) (string, []service.ChannelMention, error) {
	var binding, tenant, provider string
	var epoch int64
	if err := t.tx.QueryRowContext(t.ctx, `SELECT c.channel_binding_id,c.channel_tenant_id,c.channel_identity_epoch,b.provider FROM manager_conversations c JOIN platform_channel_bindings b ON b.id=c.channel_binding_id WHERE c.id=$1`, t.target.Conversation).Scan(&binding, &tenant, &epoch, &provider); err != nil {
		return "", nil, err
	}
	resolve := func(id, idType, sourceTenant, fallback string) (string, error) {
		name := channelLabel(fallback)
		if provider == "wecom" && idType == "user_id" && (sourceTenant == "" || sourceTenant == tenant) {
			verified, err := t.wecomVerifiedSpeakerName(binding, epoch, tenant, id)
			if err != nil {
				return "", err
			}
			if verified != "" {
				name = verified
			}
		}
		if ((provider == "feishu" && idType == "open_id") || (provider == "dingtalk" && idType == "user_id")) && (sourceTenant == "" || sourceTenant == tenant) {
			var cached sql.NullString
			if err := t.tx.QueryRowContext(t.ctx, `SELECT platform_channel_directory_display_name($1,$2,$3,$4)`, binding, epoch, tenant, id).Scan(&cached); err != nil {
				return "", err
			}
			if v := channelLabel(cached.String); v != "" && v != id {
				name = v
			}
		}
		if name == id {
			name = ""
		}
		return name, nil
	}
	if m.SenderTenant == "" {
		m.SenderTenant = tenant
	}
	var speaker string
	var err error
	if includeSpeaker {
		speaker, err = resolve(sender, m.SenderIDType, m.SenderTenant, m.SenderName)
		if err != nil {
			return "", nil, err
		}
		if speaker == "" {
			speaker, err = t.channelSpeakerAlias(sender, m.SenderIDType, m.SenderTenant)
			if err != nil {
				return "", nil, err
			}
		}
	}
	mentions := append([]service.ChannelMention(nil), m.Mentions...)
	for i := range mentions {
		mentions[i].Name, err = resolve(mentions[i].ID, mentions[i].IDType, mentions[i].TenantKey, mentions[i].Name)
		if err != nil {
			return "", nil, err
		}
		// Normalize absent source tenant to this verified conversation for dedup only.
		if mentions[i].TenantKey == "" {
			mentions[i].TenantKey = tenant
		}
	}
	return speaker, mentions, nil
}

func (t *sessionTx) channelSpeakerAlias(id, idType, tenant string) (string, error) {
	if id == "" {
		return "未知用户", nil
	}
	key, err := json.Marshal([]string{tenant, idType, id})
	if err != nil {
		return "", err
	}
	// Session admission already serializes this conversation's unique chat Session.
	var ordinal int
	err = t.tx.QueryRowContext(t.ctx, `INSERT INTO manager_channel_speaker_aliases(conversation_id,identity_key,ordinal) SELECT $1,$2,COALESCE(max(ordinal),0)+1 FROM manager_channel_speaker_aliases WHERE conversation_id=$1 ON CONFLICT(conversation_id,identity_key) DO UPDATE SET identity_key=EXCLUDED.identity_key RETURNING ordinal`, t.target.Conversation, string(key)).Scan(&ordinal)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("成员%d", ordinal), nil
}
