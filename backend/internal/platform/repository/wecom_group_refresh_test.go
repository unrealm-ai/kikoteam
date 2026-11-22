package repository

import (
	"context"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
	"testing"
)

func TestWecomGroupRefreshScopedAtomicAndStableIDs(t *testing.T) {
	db, _, _, _, actor, _, binding := channelManagerFixture(t)
	ctx := context.Background()
	reviewExec(t, db, `UPDATE platform_channel_bindings SET provider='wecom',app_id='bot-test',external_tenant_id='bot-test',identity_verified_generation=connection_generation WHERE id=$1`, binding)
	b := service.ChannelBinding{ID: binding, Generation: 1, Revision: 1, TenantID: "bot-test"}
	if e := db.QueryRow(`SELECT identity_epoch FROM platform_channel_bindings WHERE id=$1`, binding).Scan(&b.IdentityEpoch); e != nil {
		t.Fatal(e)
	}
	s := &WecomGroupRefreshStore{DB: db}
	job, e := s.RequestWecomGroups(ctx, b, actor)
	if e != nil {
		t.Fatal(e)
	}
	dup, e := s.RequestWecomGroups(ctx, b, actor)
	if e != nil || dup.ID != job.ID {
		t.Fatal(dup, e)
	}
	jobs, e := s.ClaimWecomGroups(ctx)
	if e != nil || len(jobs) != 1 {
		t.Fatal(jobs, e)
	}
	j := jobs[0]
	if _, e = s.AuthorizeWecomGroups(ctx, j.ID, j.LeaseToken); e != nil {
		t.Fatal(e)
	}
	receipt := service.WecomGroupRefreshCompletion{LeaseToken: j.LeaseToken, Status: "completed", Items: []service.WecomGroupRefreshItem{{ID: "chat-a", Name: "群A"}}}
	if e = s.CompleteWecomGroups(ctx, j.ID, receipt); e != nil {
		t.Fatal(e)
	}
	var candidate string
	if e = db.QueryRow(`SELECT id FROM platform_channel_group_candidates WHERE binding_id=$1 AND external_chat_id='chat-a'`, binding).Scan(&candidate); e != nil {
		t.Fatal(e)
	}
	status, e := s.ReadWecomGroups(ctx, b, actor, j.ID)
	if e != nil || status.Status != "completed" {
		t.Fatal(status, e)
	}
	if e = s.CompleteWecomGroups(ctx, j.ID, receipt); e == nil {
		t.Fatal("consumed lease reused")
	}
	if _, e = s.RequestWecomGroups(ctx, b, actor); e != nil {
		t.Fatal(e)
	}
	jobs, e = s.ClaimWecomGroups(ctx)
	if e != nil || len(jobs) != 1 {
		t.Fatal(jobs, e)
	}
	receipt.LeaseToken = jobs[0].LeaseToken
	if e = s.CompleteWecomGroups(ctx, jobs[0].ID, receipt); e != nil {
		t.Fatal(e)
	}
	var next string
	if e = db.QueryRow(`SELECT id FROM platform_channel_group_candidates WHERE binding_id=$1 AND external_chat_id='chat-a'`, binding).Scan(&next); e != nil || next != candidate {
		t.Fatal("unstable candidate identity", next, candidate, e)
	}
	if _, e = s.RequestWecomGroups(ctx, b, actor); e != nil {
		t.Fatal(e)
	}
	jobs, e = s.ClaimWecomGroups(ctx)
	if e != nil || len(jobs) != 1 {
		t.Fatal(jobs, e)
	}
	reviewExec(t, db, `UPDATE platform_channel_bindings SET connection_generation=connection_generation+1 WHERE id=$1`, binding)
	if _, e = s.AuthorizeWecomGroups(ctx, jobs[0].ID, jobs[0].LeaseToken); e == nil {
		t.Fatal("old binding authorized")
	}
	receipt.LeaseToken = jobs[0].LeaseToken
	receipt.Items = []service.WecomGroupRefreshItem{{ID: "bad", Name: "wrong binding"}}
	if e = s.CompleteWecomGroups(ctx, jobs[0].ID, receipt); e == nil {
		t.Fatal("old binding persisted")
	}
	var count int
	if e = db.QueryRow(`SELECT count(*) FROM platform_channel_group_candidates WHERE external_chat_id='bad'`).Scan(&count); e != nil || count != 0 {
		t.Fatal(count, e)
	}
}

func TestWecomGroupRefreshNeverPublishesPartialOrRevoked(t *testing.T) {
	for _, mode := range []string{"failed", "expired", "revoked", "tenant", "duplicate", "rollback"} {
		t.Run(mode, func(t *testing.T) {
			db, _, _, _, actor, _, binding := channelManagerFixture(t)
			ctx := context.Background()
			reviewExec(t, db, `UPDATE platform_channel_bindings SET provider='wecom',app_id='bot',external_tenant_id='bot',identity_verified_generation=connection_generation WHERE id=$1`, binding)
			b := service.ChannelBinding{ID: binding, Generation: 1, Revision: 1, TenantID: "bot"}
			if e := db.QueryRow(`SELECT identity_epoch FROM platform_channel_bindings WHERE id=$1`, binding).Scan(&b.IdentityEpoch); e != nil {
				t.Fatal(e)
			}
			s := &WecomGroupRefreshStore{DB: db}
			if _, e := s.RequestWecomGroups(ctx, b, actor); e != nil {
				t.Fatal(e)
			}
			jobs, e := s.ClaimWecomGroups(ctx)
			if e != nil || len(jobs) != 1 {
				t.Fatal(jobs, e)
			}
			j := jobs[0]
			v := service.WecomGroupRefreshCompletion{LeaseToken: j.LeaseToken, Status: "completed", Items: []service.WecomGroupRefreshItem{{ID: "group", Name: "群"}}}
			switch mode {
			case "failed":
				v.Status = "failed"
			case "expired":
				reviewExec(t, db, `UPDATE platform_wecom_group_refresh_jobs SET lease_until=now()-interval '1 second' WHERE id=$1`, j.ID)
			case "revoked":
				reviewExec(t, db, `UPDATE platform_members SET role='member' WHERE user_id=$1`, actor)
			case "tenant":
				reviewExec(t, db, `UPDATE platform_channel_bindings SET external_tenant_id='other' WHERE id=$1`, binding)
			case "duplicate":
				v.Items = append(v.Items, v.Items[0])
			case "rollback":
				reviewExec(t, db, `CREATE FUNCTION fail_group_refresh_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected';END $$;CREATE TRIGGER fail_group_refresh_test BEFORE INSERT ON platform_channel_group_candidates FOR EACH ROW EXECUTE FUNCTION fail_group_refresh_test()`)
			}
			e = s.CompleteWecomGroups(ctx, j.ID, v)
			if mode == "failed" {
				if e != nil {
					t.Fatal(e)
				}
			} else if e == nil {
				t.Fatal("invalid refresh accepted")
			}
			var count int
			if e = db.QueryRow(`SELECT count(*) FROM platform_channel_group_candidates WHERE binding_id=$1`, binding).Scan(&count); e != nil || count != 0 {
				t.Fatal("partial published", count, e)
			}
		})
	}
}
