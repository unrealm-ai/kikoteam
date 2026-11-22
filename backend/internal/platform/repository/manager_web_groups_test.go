package repository

import (
	"context"
	"database/sql"
	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
	"sync"
	"testing"
)

func webMessageFixture(t *testing.T) (*sql.DB, *manager.Service, *managerrepo.Store, string, string, string, string) {
	t.Helper()
	db, m, _, w, actor, agent, _ := managerFixture(t)
	g := manager.NewID()
	if _, err := db.Exec(`UPDATE platform_agents SET usage_config='{"access":{"mode":"all","allow":[],"deny":[]},"trigger":{"mention":true,"keywords":[],"regexes":[]},"history":{"enabled":true,"limit":50,"channels":["web"]}}' WHERE id=$1`, agent); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO platform_web_groups(id,workspace_id,title,created_by) VALUES($1,$2,'group',$3)`, g, w, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO platform_web_group_members(group_id,workspace_id,user_id,role,status) VALUES($1,$2,$3,'owner','active')`, g, w, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO platform_web_group_agents(group_id,workspace_id,agent_id,status) VALUES($1,$2,$3,'active')`, g, w, agent); err != nil {
		t.Fatal(err)
	}
	return db, m, m.Store.(*managerrepo.Store), w, actor, agent, g
}
func TestWebGroupMessagesBatchBoundaryAndPrivateExecution(t *testing.T) {
	db, m, s, w, a, agent, g := webMessageFixture(t)
	ctx := context.Background()
	first, _, err := m.PostWebGroupMessage(ctx, w, a, g, "one", manager.WebGroupPost{Text: "context one"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ProcessWebGroupInbox(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM manager_inputs`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	second, _, err := m.PostWebGroupMessage(ctx, w, a, g, "two", manager.WebGroupPost{Text: "summarize", MentionAgentIDs: []string{agent}})
	if err != nil {
		t.Fatal(err)
	}
	if second.Seq != first.Seq+1 {
		t.Fatal(first, second)
	}
	if err = s.ProcessWebGroupInbox(ctx); err != nil {
		t.Fatal(err)
	}
	var session, conv, input string
	if err = db.QueryRow(`SELECT i.session_id,s.conversation_id,n.input_id FROM manager_web_group_inbox n JOIN manager_inputs i ON i.id=n.input_id JOIN manager_sessions s ON s.id=i.session_id WHERE n.message_id=$1`, second.ID).Scan(&session, &conv, &input); err != nil {
		t.Fatal(err)
	}
	var triggers int
	if err = db.QueryRow(`SELECT count(*),count(*) FILTER(WHERE trigger_agent_loop) FROM manager_inputs WHERE session_id=$1`, session).Scan(&count, &triggers); err != nil || count != 2 || triggers != 1 {
		t.Fatal(count, triggers, err)
	}
	if _, err = s.Conversation(ctx, w, a, conv); err == nil {
		t.Fatal("internal conversation exposed")
	}
	if _, err = s.Session(ctx, w, a, session); err == nil {
		t.Fatal("internal session exposed")
	}
	if _, _, err = m.SubmitInput(ctx, w, a, session, "bypass", "attack"); err == nil {
		t.Fatal("public group session write")
	}
	internal := manager.WithWebGroupExecution(ctx, manager.WebGroupExecution{ConversationID: conv})
	if _, err = s.Session(internal, w, a, session); err != nil {
		t.Fatal("trusted task scope blocked", err)
	}
	var allowed bool
	if err = db.QueryRow(`SELECT manager_input_actor_allowed($1)`, input).Scan(&allowed); err != nil || !allowed {
		t.Fatal("group execution blocked", allowed, err)
	}
	if _, err = db.Exec(`UPDATE platform_web_group_members SET status='removed',admission_epoch=admission_epoch+1 WHERE group_id=$1 AND user_id=$2`, g, a); err != nil {
		t.Fatal(err)
	}
	if _, err = m.WebGroupMessages(ctx, w, a, g, 0, 100); err == nil {
		t.Fatal("removed member reads")
	}
	if err = db.QueryRow(`SELECT manager_input_actor_allowed($1)`, input).Scan(&allowed); err != nil || allowed {
		t.Fatal("removed actor executes", allowed, err)
	}
	if _, err = db.Exec(`UPDATE platform_web_group_members SET status='active',admission_epoch=admission_epoch+1 WHERE group_id=$1 AND user_id=$2`, g, a); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT manager_input_actor_allowed($1)`, input).Scan(&allowed); err != nil || allowed {
		t.Fatal("rejoin revived input", allowed, err)
	}
	items, err := m.WebGroupMessages(ctx, w, a, g, 0, 100)
	if err != nil || len(items) != 2 {
		t.Fatal("rejoin full history", len(items), err)
	}
}
func TestWebGroupConcurrentDedupAndNewMemberFullHistory(t *testing.T) {
	db, m, s, w, a, _, g := webMessageFixture(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for n := 0; n < 10; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, e := m.PostWebGroupMessage(ctx, w, a, g, "same", manager.WebGroupPost{Text: "before joining"})
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	items, err := s.WebGroupMessages(ctx, w, a, g, 0, 100)
	if err != nil || len(items) != 1 {
		t.Fatal(len(items), err)
	}
	member := manager.NewID()
	insertWorkspaceAccount(t, db, member, member+"@example.test")
	if _, err = db.Exec(`INSERT INTO platform_members(workspace_id,user_id,role) VALUES($1,$2,'member')`, w, member); err != nil {
		t.Fatal(err)
	}
	if _, err = s.WebGroupMessages(ctx, w, member, g, 0, 100); err == nil {
		t.Fatal("workspace member read private group")
	}
	if _, err = db.Exec(`INSERT INTO platform_web_group_members(group_id,workspace_id,user_id,role,status) VALUES($1,$2,$3,'member','active')`, g, w, member); err != nil {
		t.Fatal(err)
	}
	items, err = s.WebGroupMessages(ctx, w, member, g, 0, 100)
	if err != nil || len(items) != 1 {
		t.Fatal("new member should see earlier history", len(items), err)
	}
}

func TestWebGroupReplyLedgerAndRemovalFence(t *testing.T) {
	for _, mode := range []string{"normal", "without_inbox", "removed"} {
		t.Run(mode, func(t *testing.T) {
			db, m, s, w, a, agent, g := webMessageFixture(t)
			ctx := context.Background()
			// Group availability grants use without granting private employee access.
			if _, err := db.Exec(`UPDATE platform_agents SET usage_config=jsonb_set(usage_config,'{access,mode}','"allowlist"') WHERE id=$1`, agent); err != nil {
				t.Fatal(err)
			}
			msg, _, err := m.PostWebGroupMessage(ctx, w, a, g, "trigger", manager.WebGroupPost{Text: "hello", MentionAgentIDs: []string{agent}})
			if err != nil {
				t.Fatal(err)
			}
			if err = s.ProcessWebGroupInbox(ctx); err != nil {
				t.Fatal(err)
			}
			var input string
			if err = db.QueryRow(`SELECT input_id FROM manager_web_group_inbox WHERE message_id=$1`, msg.ID).Scan(&input); err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(`UPDATE manager_inputs SET status='rejected' WHERE id=$1`, input); err != nil {
				t.Fatal(err)
			}
			if mode == "without_inbox" {
				if _, err = db.Exec(`DELETE FROM manager_web_group_inbox WHERE input_id=$1`, input); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "removed" {
				if _, err = db.Exec(`UPDATE platform_web_group_members SET status='removed',admission_epoch=admission_epoch+1 WHERE group_id=$1 AND user_id=$2`, g, a); err != nil {
					t.Fatal(err)
				}
			}
			for n := 0; n < 2; n++ {
				if err = s.MaterializeWebGroupReplies(ctx); err != nil {
					t.Fatal(err)
				}
			}
			var count int
			if err = db.QueryRow(`SELECT count(*) FROM manager_web_group_messages WHERE origin_input_id=$1`, input).Scan(&count); err != nil {
				t.Fatal(err)
			}
			expected := 1
			if mode == "removed" {
				expected = 0
			}
			if count != expected {
				t.Fatal("reply duplicated or leaked", count, expected)
			}
			var done bool
			if err = db.QueryRow(`SELECT web_group_reply_done FROM manager_inputs WHERE id=$1`, input).Scan(&done); err != nil || !done {
				t.Fatal(done, err)
			}
		})
	}
}

func TestWebGroupUsesUnifiedTriggerAndHistory(t *testing.T) {
	for _, tc := range []struct {
		name, channel string
		history       bool
		expected      int
	}{
		{"web_keyword_history", "web", true, 2},
		{"web_keyword_no_history", "web", false, 1},
		{"other_provider_keyword", "feishu", true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, m, s, w, a, agent, g := webMessageFixture(t)
			_, err := db.Exec(`UPDATE platform_agents SET usage_config=jsonb_set(jsonb_set(usage_config,'{trigger}',jsonb_build_object('mention',false,'keywords',jsonb_build_array(jsonb_build_object('text','go now','channels',jsonb_build_array($2::text))),'regexes','[]'::jsonb)),'{history,enabled}',to_jsonb($3::boolean)) WHERE id=$1`, agent, tc.channel, tc.history)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			for _, text := range []string{"context", "go now"} {
				if _, _, err = m.PostWebGroupMessage(ctx, w, a, g, manager.NewID(), manager.WebGroupPost{Text: text}); err != nil {
					t.Fatal(err)
				}
			}
			if err = s.ProcessWebGroupInbox(ctx); err != nil {
				t.Fatal(err)
			}
			var count int
			if err = db.QueryRow(`SELECT count(*) FROM manager_inputs`).Scan(&count); err != nil || count != tc.expected {
				t.Fatal(count, tc.expected, err)
			}
		})
	}
}
