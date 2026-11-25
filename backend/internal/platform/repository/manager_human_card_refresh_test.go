package repository

import (
	"context"
	"database/sql"
	"testing"

	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
)

func assertHumanCardRefreshAfterText(t *testing.T, db *sql.DB, store *managerrepo.Store, request, provider string) {
	t.Helper()
	ctx := context.Background()
	jobs, err := store.ClaimHumanCardRefresh(ctx)
	if provider != "feishu" {
		if err != nil || len(jobs) != 0 {
			t.Fatal("non-card DingTalk or WeCom receipt produced proactive refresh", err)
		}
		return
	}
	if err != nil || len(jobs) != 1 {
		t.Fatal("committed first text answer did not create one refresh", err)
	}
	j := jobs[0]
	var before string
	if err = db.QueryRow(`SELECT receipt::text FROM manager_human_notices WHERE id=$1`, j.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if j.Provider != "feishu" || j.ProviderCardID != "om-confirmation" || j.State != "approved" || j.RequestVersion != 1 || j.Card == nil {
		t.Fatal("text answer tried updating incorrect original or version")
	}
	if next, e := store.ClaimHumanCardRefresh(ctx); e != nil || len(next) != 0 {
		t.Fatal("claim was not exclusive", e)
	}
	reviewExec(t, db, `UPDATE manager_human_requests SET version=version+1 WHERE id=$1`, request)
	if _, err = store.AuthorizeHumanCardRefresh(ctx, j.ID, j.LeaseToken); err == nil {
		t.Fatal("stale pre-reissue card was still authorized")
	}
	reviewExec(t, db, `UPDATE manager_human_requests SET version=version-1 WHERE id=$1`, request)
	reviewExec(t, db, `UPDATE platform_channel_bindings SET config_revision=config_revision+1 WHERE id=$1`, j.BindingID)
	if _, err = store.AuthorizeHumanCardRefresh(ctx, j.ID, j.LeaseToken); err == nil {
		t.Fatal("stale binding authorized an original card mutation")
	}
	reviewExec(t, db, `UPDATE platform_channel_bindings SET config_revision=config_revision-1 WHERE id=$1`, j.BindingID)
	if _, err = store.AuthorizeHumanCardRefresh(ctx, j.ID, j.LeaseToken); err != nil {
		t.Fatal("same original not authorized", err)
	}
	for range 2 {
		if err = store.CompleteHumanCardRefresh(ctx, j.ID, j.LeaseToken, "updated"); err != nil {
			t.Fatal("refresh completion not idempotent", err)
		}
	}
	var after string
	if err = db.QueryRow(`SELECT receipt::text FROM manager_human_notices WHERE id=$1`, j.ID).Scan(&after); err != nil || before != after {
		t.Fatal("UI refresh modified delivery receipt/history anchor", err)
	}
	if next, e := store.ClaimHumanCardRefresh(ctx); e != nil || len(next) != 0 {
		t.Fatal("finished update was reclaimed", e)
	}
}
