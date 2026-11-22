package service

import (
	"context"
	"errors"
	"sync"
	"time"
)

// WecomContactPermission describes only the bot's keyword search operation,
// never directory synchronization, contact identity, or message permissions.
type WecomContactPermission struct {
	BindingID  string     `json:"binding_id"`
	Status     string     `json:"status"`
	Generation int64      `json:"connection_generation"`
	Revision   int64      `json:"config_revision"`
	CheckedAt  *time.Time `json:"checked_at,omitempty"`
}

type wecomContactPermissionKey struct {
	id                          string
	epoch, generation, revision int64
}
type wecomContactPermissionEntry struct {
	done   chan struct{}
	expiry time.Time
	value  WecomContactPermission
}
type wecomContactPermissionCache struct {
	mu      sync.Mutex
	entries map[wecomContactPermissionKey]*wecomContactPermissionEntry
	running int
}

const wecomProbeKeyword = "KikoPermissionProbeNoSuchContact921763"

func wecomPermissionKey(b ChannelBinding) wecomContactPermissionKey {
	return wecomContactPermissionKey{b.ID, b.IdentityEpoch, b.Generation, b.Revision}
}

func (s *WecomContactsService) ContactPermission(ctx context.Context, workspace, actor, agent string) (WecomContactPermission, error) {
	b, err := s.Channels.GetProvider(ctx, workspace, actor, agent, "wecom")
	if err != nil {
		return WecomContactPermission{}, err
	}
	value := WecomContactPermission{Status: "not_checked", BindingID: b.ID, Generation: b.Generation, Revision: b.Revision}
	if !b.Enabled || !b.IdentityConfirmed || b.IdentityVerifiedGeneration != b.Generation {
		return value, nil
	}
	key := wecomPermissionKey(b)
	value, err = s.permissionCache.get(ctx, key, func() WecomContactPermission {
		value.Status = s.probeContactPermission(ctx, b)
		now := time.Now().UTC()
		value.CheckedAt = &now
		return value
	})
	if err != nil {
		return WecomContactPermission{}, err
	}
	// Re-read both caller authorization and credential identity after waiting
	// for Tencent. A concurrent edit/rebind must not publish a prior result.
	latest, err := s.binding(ctx, workspace, actor, agent)
	if err != nil {
		return WecomContactPermission{}, err
	}
	if key != wecomPermissionKey(latest) {
		return WecomContactPermission{}, Fail("wecom_permission_binding_changed", 409, "企微连接配置已更新，请重新读取后检测")
	}
	return value, nil
}

func (s *WecomContactsService) probeContactPermission(ctx context.Context, b ChannelBinding) string {
	if s.Provider == nil {
		return "unavailable"
	}
	secret, err := s.Channels.open(b)
	if err != nil {
		return "unavailable"
	}
	call, cancel := context.WithTimeout(ctx, 16*time.Second)
	defer cancel()
	// Do not retain or expose any matches. An empty valid response still
	// confirms the exact same provider operation as an ordinary search.
	_, err = s.Provider.Search(call, b.AppID, secret, wecomProbeKeyword)
	if err == nil {
		return "available"
	}
	var provider *Error
	if errors.As(err, &provider) {
		switch provider.Code {
		case "wecom_contacts_authorization_denied":
			return "denied"
		case "wecom_contacts_authentication_failed":
			return "authentication_failed"
		case "wecom_contacts_unsupported":
			return "unsupported"
		}
	}
	return "unavailable"
}

// Only an outcome (not contacts or credentials) is cached per active binding
// revision. The cache and concurrent provider calls are bounded per process.
func (c *wecomContactPermissionCache) get(ctx context.Context, key wecomContactPermissionKey, probe func() WecomContactPermission) (WecomContactPermission, error) {
	c.mu.Lock()
	now := time.Now()
	for k, entry := range c.entries {
		if entry.done == nil && !entry.expiry.After(now) {
			delete(c.entries, k)
		}
	}
	if entry := c.entries[key]; entry != nil {
		done := entry.done
		c.mu.Unlock()
		if done != nil {
			select {
			case <-ctx.Done():
				return WecomContactPermission{}, ctx.Err()
			case <-done:
			}
		}
		return entry.value, nil
	}
	if c.running >= 8 || len(c.entries) >= 256 {
		c.mu.Unlock()
		return WecomContactPermission{}, Fail("wecom_permission_probe_busy", 429, "联系人搜索检测繁忙，请稍后重试")
	}
	if c.entries == nil {
		c.entries = make(map[wecomContactPermissionKey]*wecomContactPermissionEntry)
	}
	entry := &wecomContactPermissionEntry{done: make(chan struct{})}
	c.entries[key] = entry
	c.running++
	c.mu.Unlock()

	result := probe()
	c.mu.Lock()
	c.running--
	entry.value = result
	ttl := 30 * time.Second
	if result.Status == "unavailable" {
		ttl = 5 * time.Second
	}
	entry.expiry = time.Now().Add(ttl)
	close(entry.done)
	entry.done = nil
	c.mu.Unlock()
	return result, nil
}
