package repository

import (
	"context"
	"errors"
	"sync"
	"testing"

	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

type dingTalkDirectoryFixtureFetcher struct{ fixtureDirectoryFetcher }

func (dingTalkDirectoryFixtureFetcher) FetchProvider(_ context.Context, provider, app, secret, tenant string) (service.DirectorySnapshot, error) {
	if provider != "dingtalk" || app != "provider_app" || secret != "synthetic-provider-secret" || tenant != "" {
		return service.DirectorySnapshot{}, errors.New("expected the DingTalk application credentials before tenant confirmation")
	}
	return directorySnapshot(), nil
}

func TestChannelProvidersFirstMessageConfirmsAndProcesses(t *testing.T) {
	for _, provider := range []string{"dingtalk", "wecom"} {
		t.Run(provider, func(t *testing.T) {
			db, m, _, w, actor, agent, _ := managerFixture(t)
			store := NewChannelStore(db)
			api := &service.ChannelService{Store: store, Secret: []byte("provider-test-secret-32-bytes-long!")}
			secret := "synthetic-provider-secret"
			b, err := api.PutProvider(context.Background(), w, actor, agent, provider, service.ChannelPut{AppID: "provider_app", AppSecret: &secret, Enabled: true, History: service.ChannelHistory{Limit: 20}, Access: service.ChannelAccess{Mode: "all"}})
			if err != nil {
				t.Fatal(err)
			}
			if b.IdentityConfirmed || b.TenantID != "" || b.BotID != "" {
				t.Fatal("configuration forged identity", b)
			}
			if err = api.Apply(context.Background(), b.ID, service.ChannelApplied{Generation: b.Generation, Revision: b.Revision, RulesVersion: b.RulesVersion, Status: "connected", ObservedTenantID: "forged", ObservedBotID: "forged"}); err == nil {
				t.Fatal("transport status forged identity")
			}
			var beforeMessage service.DirectoryStatus
			var refreshClaim *service.DirectoryClaim
			if provider == "dingtalk" {
				worked, e := api.SyncNextDirectory(context.Background(), dingTalkDirectoryFixtureFetcher{})
				if e != nil || !worked {
					t.Fatal("directory did not sync automatically before inbound", worked, e)
				}
				b, e = api.GetProvider(context.Background(), w, actor, agent, provider)
				if e != nil || b.IdentityConfirmed || b.TenantID != "" || b.BotID != "" {
					t.Fatal("directory sync confirmed robot identity", b, e)
				}
				beforeMessage, e = api.DirectoryStatusProvider(context.Background(), w, actor, agent, provider)
				if e != nil || !beforeMessage.Complete || beforeMessage.UserCount != 3 {
					t.Fatal("directory snapshot unavailable before inbound", beforeMessage, e)
				}
				if _, e = api.RequestDirectorySyncProvider(context.Background(), w, actor, agent, provider); e != nil {
					t.Fatal("manual sync blocked before first inbound", e)
				}
				refreshClaim, e = store.ClaimDirectorySync(context.Background())
				if e != nil || refreshClaim == nil || refreshClaim.Binding.TenantID != "" {
					t.Fatal("directory refresh not claimed before first inbound", refreshClaim, e)
				}
			} else if _, err = api.RequestDirectorySyncProvider(context.Background(), w, actor, agent, provider); err == nil {
				t.Fatal("WeCom directory accepted")
			}
			ms := m.Store.(*managerrepo.Store)
			v := inboundFixture(b.ID, "provider-first", "user1")
			v.BotID = "provider_robot"
			v.SenderIDType = "user_id"
			v.SenderTenant = v.ExternalTenantID
			if provider == "wecom" {
				v.BotID = b.AppID
				v.ExternalTenantID = b.AppID
				v.ProviderDirected = true
			}
			if _, err = ms.ReceiveChannel(context.Background(), v); err != nil {
				t.Fatal(err)
			}
			if err = ms.ProcessChannelInbox(context.Background(), m); err != nil {
				t.Fatal(err)
			}
			b, err = api.GetProvider(context.Background(), w, actor, agent, provider)
			if err != nil || !b.IdentityConfirmed {
				t.Fatal("first identity not confirmed", b, err)
			}
			if provider == "dingtalk" {
				page, e := api.DirectoryPageProvider(context.Background(), w, actor, agent, provider, service.DirectoryQuery{Kind: "users", Limit: 10})
				if e != nil || page.SnapshotVersion != beforeMessage.SnapshotVersion {
					t.Fatal("identity confirmation discarded the directory snapshot", page, e)
				}
				var name string
				if e = db.QueryRow(`SELECT platform_channel_directory_display_name($1,$2,$3,'ou_a')`, b.ID, b.IdentityEpoch, b.TenantID).Scan(&name); e != nil || name != "Alice" {
					t.Fatal("directory snapshot did not follow confirmed tenant", name, e)
				}
				if e = store.PublishDirectory(context.Background(), *refreshClaim, directorySnapshot()); e != nil {
					t.Fatal("first identity confirmation interrupted the directory refresh", e)
				}
			}
			var count int
			if err = db.QueryRow(`SELECT count(*) FROM manager_inputs i JOIN manager_channel_principals p ON p.id=i.channel_principal_id WHERE p.binding_id=$1 AND p.provider=$2`, b.ID, provider).Scan(&count); err != nil || count != 1 {
				t.Fatal("first message not processed", count, err)
			}
			var execution service.ChannelExecution
			if err = db.QueryRow(`SELECT p.id,c.id,b.connection_generation FROM manager_channel_principals p JOIN manager_conversations c ON c.channel_binding_id=p.binding_id JOIN platform_channel_bindings b ON b.id=p.binding_id WHERE b.id=$1 LIMIT 1`, b.ID).Scan(&execution.PrincipalID, &execution.ConversationID, &execution.Generation); err != nil {
				t.Fatal(err)
			}
			tx, e := db.BeginTx(context.Background(), nil)
			if e != nil {
				t.Fatal(e)
			}
			e = (&modelTx{tx: tx, workspace: w}).AuthorizeChannelExecution(context.Background(), execution, agent, false)
			if rollbackErr := tx.Rollback(); rollbackErr != nil {
				t.Fatal(rollbackErr)
			}
			if e != nil {
				t.Fatal("provider runtime authorization", e)
			}
			bad := v
			bad.ProviderEventID = "tenant-conflict"
			bad.ProviderMessageID = bad.ProviderEventID
			bad.ExternalTenantID = "other"
			if _, err = ms.ReceiveChannel(context.Background(), bad); err == nil {
				t.Fatal("cross tenant accepted")
			}
			if _, err = ms.ReceiveChannel(context.Background(), v); err != nil {
				t.Fatal(err)
			}
			rotated, err := api.PutProvider(context.Background(), w, actor, agent, provider, service.ChannelPut{Version: b.Revision, AppID: b.AppID, AppSecret: &secret, Enabled: true, History: b.History, Access: b.Access})
			if err != nil {
				t.Fatal(err)
			}
			var nextClaim *service.DirectoryClaim
			if provider == "dingtalk" {
				if _, e := api.DirectoryPageProvider(context.Background(), w, actor, agent, provider, service.DirectoryQuery{Kind: "users", Limit: 10}); e == nil {
					t.Fatal("rotated source exposed old directory")
				}
				if _, err = api.RequestDirectorySyncProvider(context.Background(), w, actor, agent, provider); err != nil {
					t.Fatal("manual sync blocked before identity reconfirmation", err)
				}
				nextClaim, err = store.ClaimDirectorySync(context.Background())
				if err != nil || nextClaim == nil || nextClaim.Binding.Generation != rotated.Generation {
					t.Fatal("rotated credential could not sync", nextClaim, err)
				}
			}
			if rotated.IdentityConfirmed {
				t.Fatal("new credential generation inherited verification")
			}
			if _, err = ms.ReceiveChannel(context.Background(), v); err == nil {
				t.Fatal("old generation admitted")
			}
			v.ConnectionGeneration = rotated.Generation
			v.ConfigRevision = rotated.Revision
			v.ProviderEventID = "new-generation"
			v.ProviderMessageID = v.ProviderEventID
			if provider == "dingtalk" {
				v.ChatType = "group"
				v.BotMentioned = true
			}
			if _, err = ms.ReceiveChannel(context.Background(), v); err != nil {
				t.Fatal(err)
			}
			if nextClaim != nil {
				if err = store.PublishDirectory(context.Background(), *nextClaim, directorySnapshot()); err != nil {
					t.Fatal("identity reconfirmation interrupted sync", err)
				}
			}
			// A second provider is a separate binding and cannot adopt the first conversation.
			other := "wecom"
			if provider == "wecom" {
				other = "dingtalk"
			}
			second, err := api.PutProvider(context.Background(), w, actor, agent, other, service.ChannelPut{AppID: "provider_app", AppSecret: &secret, History: service.ChannelHistory{Limit: 20}, Access: service.ChannelAccess{Mode: "all"}})
			if err != nil || second.ID == b.ID {
				t.Fatal("provider collision", second, err)
			}
		})
	}
}

func TestChannelProviderGroupNamesDoNotAuthorize(t *testing.T) {
	db, m, _, _, _, _, binding := channelManagerFixture(t)
	if _, err := db.Exec(`UPDATE platform_channel_bindings SET provider='dingtalk',external_tenant_id='',bot_open_id='',app_id='ding' WHERE id=$1`, binding); err != nil {
		t.Fatal(err)
	}
	ms := m.Store.(*managerrepo.Store)
	v := inboundFixture(binding, "group-first", "user")
	v.BotID = "ding"
	v.SenderIDType = "user_id"
	v.SenderTenant = v.ExternalTenantID
	v.ChatType = "group"
	v.ChatName = "Display only"
	if _, err := ms.ReceiveChannel(context.Background(), v); err == nil {
		t.Fatal("unaddressed group pinned identity")
	}
	v.BotMentioned = true
	if _, err := ms.ReceiveChannel(context.Background(), v); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := db.QueryRow(`SELECT display_name FROM platform_channel_observed_chats WHERE binding_id=$1`, binding).Scan(&name); err != nil || name != v.ChatName {
		t.Fatal(name, err)
	}
}

func TestWecomDirectedGroupDenialHasFixedReplyWithoutInput(t *testing.T) {
	db, m, s, _, _, _, binding := channelManagerFixture(t)
	if _, err := db.Exec(`UPDATE platform_channel_bindings SET provider='wecom',app_id='bot',external_tenant_id='',bot_open_id='',access='{"mode":"allowlist"}' WHERE id=$1`, binding); err != nil {
		t.Fatal(err)
	}
	v := inboundFixture(binding, "directed-denied", "user")
	v.ExternalTenantID = "bot"
	v.BotID = "bot"
	v.SenderIDType = "user_id"
	v.ChatType = "group"
	v.ProviderDirected = true
	receipt, err := s.ReceiveChannel(context.Background(), v)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ProcessChannelInbox(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = db.QueryRow(`SELECT count(*) FROM manager_channel_deliveries WHERE inbox_id=$1 AND kind='fixed'`, receipt.ID).Scan(&n); err != nil || n != 1 {
		t.Fatal("no explicit refusal", n, err)
	}
	for _, table := range []string{"manager_inputs", "manager_channel_history_jobs", "manager_channel_history_boundaries"} {
		if err = db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatal(table, n, err)
		}
	}
}

func TestChannelFirstIdentityRacePinsOnlyOneTenant(t *testing.T) {
	db, _, s, _, _, _, binding := channelManagerFixture(t)
	if _, err := db.Exec(`UPDATE platform_channel_bindings SET provider='dingtalk',app_id='app',external_tenant_id='',bot_open_id='' WHERE id=$1`, binding); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, tenant := range []string{"corp_a", "corp_b"} {
		wg.Add(1)
		go func(tenant string) {
			defer wg.Done()
			v := inboundFixture(binding, tenant, "user")
			v.ExternalTenantID = tenant
			v.SenderTenant = tenant
			v.SenderIDType = "user_id"
			v.BotID = "robot"
			_, err := s.ReceiveChannel(context.Background(), v)
			results <- err
		}(tenant)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatal("ambiguous identity winners", success)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM manager_channel_inbox n JOIN platform_channel_bindings b ON b.id=n.binding_id WHERE n.binding_id=$1 AND n.external_tenant_id=b.external_tenant_id`, binding).Scan(&count); err != nil || count != 1 {
		t.Fatal("conflicting event persisted", count, err)
	}
}
