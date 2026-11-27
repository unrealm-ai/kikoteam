package repository

import (
	"context"
	"errors"
	"strings"
	"testing"

	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	platform "github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestWecomVerifiedProfilesSurviveTemporaryCacheOnlyWithinBindingScope(t *testing.T) {
	db, contacts, b, actor := wecomContactFixture(t)
	ctx := context.Background()
	v := contactStart(t, contacts, b, actor, "search-A")
	store := &managerrepo.Store{DB: db, ConsumeWecomControl: ConsumeWecomControl(contacts.Channels.Secret)}
	if _, err := store.ReceiveChannel(ctx, contactInbound(b, v.Code, "callback-A")); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE platform_wecom_contacts SET expires_at=now()-interval '1 hour' WHERE binding_id=$1`, b.ID); err != nil {
		t.Fatal(err)
	}
	cache := &WecomContactStore{DB: db}
	if _, err := cache.CacheWecomContacts(ctx, b, actor, nil); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM platform_wecom_contacts WHERE binding_id=$1 AND search_user_id='search-A' AND expires_at<now()`, b.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("verified display profile lost or freshness fabricated", count, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM platform_wecom_contacts WHERE binding_id=$1 AND search_user_id='search-B'`, b.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("unverified cache retained", count, err)
	}
	// Keeping a name never makes expired search data eligible for a new binding.
	if _, err := contacts.Start(ctx, b.WorkspaceID, actor, b.AgentID, "search-A", platform.NewID()); err == nil {
		t.Fatal("retained stale profile authorized a new verification")
	}
	if _, err := db.Exec(`UPDATE platform_channel_bindings SET connection_generation=connection_generation+1,identity_verified_generation=connection_generation+1 WHERE id=$1`, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.CacheWecomContacts(ctx, b, actor, nil); err == nil {
		t.Fatal("old generation accepted")
	}
	b.Generation++
	if _, err := cache.CacheWecomContacts(ctx, b, actor, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM platform_wecom_contacts WHERE binding_id=$1 AND connection_generation=$2 AND search_user_id='search-A' AND expires_at<now()`, b.ID, b.Generation).Scan(&count); err != nil || count != 1 {
		t.Fatal("same-bot rotation lost or renewed the verified profile", count, err)
	}
}

func TestWecomLiveGroupUsesOnlyVerifiedCallbackName(t *testing.T) {
	db, m, store, _, actor, _, binding := channelManagerFixture(t)
	ctx := context.Background()
	if _, err := db.Exec(`UPDATE platform_channel_bindings SET provider='wecom',app_id='bot',bot_open_id='bot',external_tenant_id='bot',identity_verified_generation=connection_generation WHERE id=$1`, binding); err != nil {
		t.Fatal(err)
	}
	verification := manager.NewID()
	if _, err := db.Exec(`INSERT INTO platform_wecom_contacts(binding_id,identity_epoch,connection_generation,search_user_id,display_name,expires_at) VALUES($1,1,1,'search-A','张三[伪标签]',now()-interval '1 hour');`, binding); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO platform_wecom_verifications(id,binding_id,identity_epoch,connection_generation,search_user_id,created_by,code_hash,code_ciphertext,status,callback_user_id,expires_at)VALUES($1,$2,1,1,'search-A',$3,$4,decode('','hex'),'verified','callback-A',now());`, verification, binding, actor, verification); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO platform_wecom_identity_mappings(binding_id,identity_epoch,connection_generation,search_user_id,callback_user_id,verification_id)VALUES($1,1,1,'search-A','callback-A',$2)`, binding, verification); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ event, sender, kind, want string }{
		{"verified", "callback-A", "group", "[说话人=张三 伪标签]hello"},
		{"unverified-search", "search-A", "group", "[说话人=成员1]hello"},
		{"private", "callback-A", "direct", "hello"},
	} {
		v := inboundFixture(binding, tc.event, tc.sender)
		v.ChatType = tc.kind
		v.ExternalTenantID = "bot"
		v.BotID = "bot"
		v.SenderIDType = "user_id"
		v.ProviderDirected = true
		r, err := store.ReceiveChannel(ctx, v)
		if err != nil {
			t.Fatal(err)
		}
		if err = store.ProcessChannelInbox(ctx, m); err != nil {
			t.Fatal(err)
		}
		var text, sender string
		if err = db.QueryRow(`SELECT i.text,n.sender_id FROM manager_channel_inbox n JOIN manager_inputs i ON i.id=n.input_id WHERE n.id=$1`, r.ID).Scan(&text, &sender); err != nil {
			t.Fatal(err)
		}
		if text != tc.want || sender != tc.sender {
			t.Fatalf("%s text=%q sender=%q", tc.event, text, sender)
		}
	}
	// A new identity epoch must never borrow the old verified name.
	if _, err := db.Exec(`UPDATE platform_channel_bindings SET identity_epoch=identity_epoch+1 WHERE id=$1`, binding); err != nil {
		t.Fatal(err)
	}
	v := inboundFixture(binding, "new-identity", "callback-A")
	v.ChatType = "group"
	v.ExternalTenantID = "bot"
	v.BotID = "bot"
	v.SenderIDType = "user_id"
	v.ProviderDirected = true
	r, err := store.ReceiveChannel(ctx, v)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.ProcessChannelInbox(ctx, m); err != nil {
		t.Fatal(err)
	}
	var text string
	if err = db.QueryRow(`SELECT text FROM manager_inputs WHERE id=(SELECT input_id FROM manager_channel_inbox WHERE id=$1)`, r.ID).Scan(&text); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "张三") {
		t.Fatal("name crossed identity epoch", text)
	}
}

func TestWecomSearchMergesVerifiedMatchesWithoutMaskingRemoteFailure(t *testing.T) {
	db, contacts, b, actor := wecomContactFixture(t)
	ctx := context.Background()
	verification := contactStart(t, contacts, b, actor, "search-A")
	store := &managerrepo.Store{DB: db, ConsumeWecomControl: ConsumeWecomControl(contacts.Channels.Secret)}
	if _, err := store.ReceiveChannel(ctx, contactInbound(b, verification.Code, "callback-A")); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE platform_wecom_contacts SET expires_at=now()-interval '1 hour' WHERE binding_id=$1`, b.ID); err != nil {
		t.Fatal(err)
	}
	contacts.Provider = contactProviderFunc(func(context.Context, string, string, string) (platform.WecomContactSearch, error) {
		return platform.WecomContactSearch{Items: []platform.WecomContact{}}, nil
	})
	found, err := contacts.Search(ctx, b.WorkspaceID, actor, b.AgentID, "Alice")
	if err != nil || len(found.Items) != 1 || found.Items[0].CallbackUserID != "callback-A" || found.Items[0].VerificationStatus != "verified" {
		t.Fatal(found, err)
	}
	found, err = contacts.Search(ctx, b.WorkspaceID, actor, b.AgentID, "Bob")
	if err != nil || len(found.Items) != 0 {
		t.Fatal("unverified cache became candidate", found, err)
	}
	contacts.Provider = contactProviderFunc(func(context.Context, string, string, string) (platform.WecomContactSearch, error) {
		return platform.WecomContactSearch{Items: []platform.WecomContact{{SearchUserID: "search-A", DisplayName: "Alice updated"}}}, nil
	})
	found, err = contacts.Search(ctx, b.WorkspaceID, actor, b.AgentID, "Alice")
	if err != nil || len(found.Items) != 1 || found.Items[0].DisplayName != "Alice updated" {
		t.Fatal("duplicate or stale name", found, err)
	}
	failure := errors.New("synthetic provider failure")
	contacts.Provider = contactProviderFunc(func(context.Context, string, string, string) (platform.WecomContactSearch, error) {
		return platform.WecomContactSearch{}, failure
	})
	found, err = contacts.Search(ctx, b.WorkspaceID, actor, b.AgentID, "Alice")
	if !errors.Is(err, failure) || len(found.Items) != 0 {
		t.Fatal("provider failure masked by retained matches", found, err)
	}
	if _, err = contacts.Search(ctx, b.WorkspaceID, actor, b.AgentID, ""); err == nil {
		t.Fatal("empty keyword browsing enabled")
	}
	contacts.Provider = contactProviderFunc(func(context.Context, string, string, string) (platform.WecomContactSearch, error) {
		return platform.WecomContactSearch{Items: []platform.WecomContact{}}, nil
	})
	selectors := &platform.PeopleSelectorService{Secret: []byte(strings.Repeat("s", 32)), Channels: contacts.Channels, Wecom: contacts, Store: NewPeopleSelectorStore(db, []byte(strings.Repeat("s", 32)))}
	page, err := selectors.Search(ctx, b.WorkspaceID, actor, b.AgentID, platform.PeopleQuery{Provider: "wecom", Purpose: "usage", Kind: "user", Q: "Alice", Limit: 50})
	if err != nil || len(page.Items) != 1 || !page.Items[0].Selectable || page.Items[0].Ref.ExternalID != "callback-A" {
		t.Fatal(page, err)
	}
	ref := page.Items[0].Ref
	if _, err = db.Exec(`DELETE FROM platform_wecom_identity_mappings WHERE binding_id=$1`, b.ID); err != nil {
		t.Fatal(err)
	}
	page, err = selectors.Resolve(ctx, b.WorkspaceID, actor, b.AgentID, "usage", []platform.SelectionRef{ref})
	if err != nil || len(page.Items) != 1 || page.Items[0].Selectable {
		t.Fatal("cached profile bypassed deleted mapping", page, err)
	}

}

func TestWecomVerifiedSearchIsBoundedSortedAndDoesNotRenewProfiles(t *testing.T) {
	db, contacts, b, actor := wecomContactFixture(t)
	ctx := context.Background()
	verification := contactStart(t, contacts, b, actor, "search-A")
	store := &managerrepo.Store{DB: db, ConsumeWecomControl: ConsumeWecomControl(contacts.Channels.Secret)}
	if _, err := store.ReceiveChannel(ctx, contactInbound(b, verification.Code, "callback-A")); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO platform_wecom_contacts(binding_id,identity_epoch,connection_generation,search_user_id,display_name,expires_at) SELECT $1,$2,$3,'local-'||lpad(n::text,3,'0'),'匹配姓名',now()-interval '1 hour' FROM generate_series(1,105)n`, b.ID, b.IdentityEpoch, b.Generation); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO platform_wecom_identity_mappings(binding_id,identity_epoch,connection_generation,search_user_id,callback_user_id,verification_id) SELECT $1,$2,$3,'local-'||lpad(n::text,3,'0'),'callback-'||n,$4 FROM generate_series(1,105)n`, b.ID, b.IdentityEpoch, b.Generation, verification.ID); err != nil {
		t.Fatal(err)
	}
	contacts.Provider = contactProviderFunc(func(context.Context, string, string, string) (platform.WecomContactSearch, error) {
		return platform.WecomContactSearch{Items: []platform.WecomContact{}}, nil
	})
	out, err := contacts.Search(ctx, b.WorkspaceID, actor, b.AgentID, "匹配")
	if err != nil || len(out.Items) != 100 || !out.Limited || out.Items[0].SearchUserID != "local-001" || out.Items[99].SearchUserID != "local-100" {
		t.Fatal(out, err)
	}
	var renewed int
	if err = db.QueryRow(`SELECT count(*) FROM platform_wecom_contacts WHERE binding_id=$1 AND search_user_id LIKE 'local-%' AND expires_at>now()`, b.ID).Scan(&renewed); err != nil || renewed != 0 {
		t.Fatal("local lookup renewed candidate freshness", renewed, err)
	}
}

func TestWecomSameBotRotationAndReenablePreserveVerifiedPeople(t *testing.T) {
	db, contacts, b, actor := wecomContactFixture(t)
	ctx := t.Context()
	store := &managerrepo.Store{DB: db, ConsumeWecomControl: ConsumeWecomControl(contacts.Channels.Secret)}
	verified := contactStart(t, contacts, b, actor, "search-A")
	if _, err := store.ReceiveChannel(ctx, contactInbound(b, verified.Code, "callback-A")); err != nil {
		t.Fatal(err)
	}
	pending := contactStart(t, contacts, b, actor, "search-B")
	original := platform.SelectionRef{Provider: "wecom", Kind: "user", WorkspaceID: b.WorkspaceID, AgentID: b.AgentID, BindingID: b.ID, TenantID: b.TenantID, IdentityEpoch: b.IdentityEpoch, Generation: b.Generation, Namespace: "user_id", ExternalID: "callback-A"}
	selectors := NewPeopleSelectorStore(db, contacts.Channels.Secret)
	save := func(id string, enabled bool, secret *string) {
		t.Helper()
		var err error
		b, err = contacts.Channels.PutManual(ctx, b.WorkspaceID, actor, b.AgentID, "wecom", platform.ChannelPut{Version: b.Revision, Enabled: enabled, AppID: id, AppSecret: secret, History: b.History, Trigger: b.Trigger, Access: platform.ChannelAccess{Mode: "all"}}, "", true)
		if err != nil {
			t.Fatal(err)
		}
	}
	// Use the normal configuration write so the saved recipient is authorized.
	save(b.AppID, true, nil)
	rules, err := store.HumanRecipients(ctx, b.WorkspaceID, actor, b.AgentID, &manager.HumanRecipients{Version: 1, Recipients: []manager.HumanRecipient{{Ref: original, IsDefault: true}}})
	if err != nil {
		t.Fatal(err)
	}
	check := func(want bool) {
		t.Helper()
		person, err := selectors.ResolvePerson(ctx, b.WorkspaceID, actor, b.AgentID, original)
		if err != nil || person.Selectable != want || want && (person.Ref.Generation != b.Generation || person.DisplayName != "Alice") {
			t.Fatal("resolve saved person", person, err)
		}
	}
	confirm := func() {
		t.Helper()
		if _, err := store.ReceiveChannel(ctx, contactInbound(b, "hello", "observer")); err != nil {
			t.Fatal(err)
		}
	}
	checkCarried := func() {
		t.Helper()
		check(true)
		v, err := contacts.Get(ctx, b.WorkspaceID, actor, b.AgentID, verified.ID, false)
		if err != nil || v.Status != "verified" || v.CallbackUserID != "callback-A" || v.Code != "" {
			t.Fatal("verified receipt", v, err)
		}
		v, err = contacts.Start(ctx, b.WorkspaceID, actor, b.AgentID, verified.SearchUserID, verified.ID)
		if err != nil || v.Status != "verified" || v.ID != verified.ID || v.Code != "" {
			t.Fatal("replayed verification request", v, err)
		}
		now, err := store.HumanRecipients(ctx, b.WorkspaceID, actor, b.AgentID, nil)
		if err != nil || len(now.Recipients) != 1 || now.Recipients[0].ID != rules.Recipients[0].ID || !now.Recipients[0].IsDefault || now.Recipients[0].Ref.Generation != b.Generation || now.Version <= rules.Version {
			t.Fatal("saved confirmation rule", now, err)
		}
		rules = now
		if _, err = store.HumanRecipients(ctx, b.WorkspaceID, actor, b.AgentID, &manager.HumanRecipients{Version: rules.Version, Recipients: rules.Recipients}); err != nil {
			t.Fatal("confirmation authorization", err)
		}
	}
	rotated := "rotated-fixture-secret"
	save(b.AppID, true, &rotated)
	check(false)
	confirm()
	checkCarried()
	if _, err = store.ReceiveChannel(ctx, contactInbound(b, pending.Code, "callback-B")); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM platform_wecom_identity_mappings WHERE binding_id=$1 AND search_user_id='search-B'`, b.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("old pending code granted identity", count, err)
	}
	save(b.AppID, false, nil)
	check(false)
	save(b.AppID, true, nil)
	check(false)
	confirm()
	checkCarried()
	save("replacement-bot", true, &rotated)
	confirm()
	check(false)
	if err = db.QueryRow(`SELECT count(*) FROM platform_wecom_identity_mappings WHERE binding_id=$1 AND identity_epoch=$2`, b.ID, b.IdentityEpoch).Scan(&count); err != nil || count != 0 {
		t.Fatal("replacement bot inherited people", count, err)
	}
}
