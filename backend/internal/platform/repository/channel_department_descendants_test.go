package repository

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func TestDirectoryRecursiveMembershipAndTTL(t *testing.T) {
	db, _, store, b, _, _ := directoryFixture(t)
	publishDirectoryFixture(t, store, b)
	var duration float64
	if err := db.QueryRow(`SELECT extract(epoch FROM expires_at-synced_at) FROM platform_channel_directory_state WHERE binding_id=$1`, b.ID).Scan(&duration); err != nil || duration != 24*time.Hour.Seconds() {
		t.Fatal(duration, err)
	}
	check := func(person, rules string, want *bool) {
		t.Helper()
		var result sql.NullBool
		if err := db.QueryRow(`SELECT platform_channel_directory_department_match($1,$2,$3)`, b.ID, person, rules).Scan(&result); err != nil {
			t.Fatal(err)
		}
		if want == nil && result.Valid || want != nil && (!result.Valid || result.Bool != *want) {
			t.Fatalf("%s %s: %+v", person, rules, result)
		}
	}
	yes, no := true, false
	check("ou_b", `["od_team"]`, &no)
	check("ou_b", `[{"department_id":"od_team","include_descendants":false}]`, &no)
	check("ou_b", `[{"department_id":"od_team","include_descendants":true}]`, &yes)
	check("ou_a", `[{"department_id":"od_child","include_descendants":true}]`, &no)
	check("not_in_snapshot", `[{"department_id":"od_team","include_descendants":true}]`, nil)
	if _, err := db.Exec(`UPDATE platform_channel_directory_departments SET parent_id='missing' WHERE binding_id=$1 AND department_id='od_team'`, b.ID); err != nil {
		t.Fatal(err)
	}
	check("ou_b", `[{"department_id":"unrelated","include_descendants":true}]`, nil)
	check("ou_b", `[{"department_id":"unrelated","include_descendants":true},{"department_id":"od_team","include_descendants":true}]`, &yes)
	if _, err := db.Exec(`UPDATE platform_channel_directory_departments SET parent_id='od_child' WHERE binding_id=$1 AND department_id='od_team'`, b.ID); err != nil {
		t.Fatal(err)
	}
	check("ou_b", `[{"department_id":"unrelated","include_descendants":true}]`, nil)

	if _, err := db.Exec(`INSERT INTO platform_channel_directory_departments(binding_id,department_id,display_name,parent_id) SELECT $1,'deep_'||n,'deep','deep_'||(n+1) FROM generate_series(0,101) n`, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE platform_channel_directory_users SET department_ids='["deep_0"]' WHERE binding_id=$1 AND user_id='ou_b'`, b.ID); err != nil {
		t.Fatal(err)
	}
	check("ou_b", `[{"department_id":"deep_99","include_descendants":true}]`, &yes)
	check("ou_b", `[{"department_id":"deep_101","include_descendants":true}]`, nil)
	if _, err := db.Exec(`UPDATE platform_channel_directory_state SET expires_at=now()-interval '1 second' WHERE binding_id=$1`, b.ID); err != nil {
		t.Fatal(err)
	}
	check("ou_b", `[{"department_id":"od_team","include_descendants":true}]`, nil)
}

func TestDirectoryRecursiveSelectionNeedsFreshSnapshot(t *testing.T) {
	db, channels, store, b, actor, _ := directoryFixture(t)
	publishDirectoryFixture(t, store, b)
	api := &service.AgentUsageService{Store: &AgentUsageStore{DB: db}}
	ctx := context.Background()
	q, err := api.Get(ctx, b.WorkspaceID, actor, b.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	q.UsageConfig.Access.Mode = "allowlist"
	q.UsageConfig.Access.Allow = []service.UsageSelector{{Channel: "feishu", Kind: "department", ID: "od_team", BindingID: b.ID, IdentityEpoch: b.IdentityEpoch}}
	q, err = api.Update(ctx, b.WorkspaceID, actor, b.AgentID, q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE platform_channel_directory_state SET expires_at=now()-interval '1 second' WHERE binding_id=$1`, b.ID); err != nil {
		t.Fatal(err)
	}
	q.UsageConfig.Access.Allow[0].IncludeDescendants = true
	if _, err = api.Update(ctx, b.WorkspaceID, actor, b.AgentID, q); err == nil {
		t.Fatal("stale snapshot expanded access")
	}
	b, err = channels.Get(ctx, b.WorkspaceID, actor, b.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	publishDirectoryFixture(t, store, b)
	q, err = api.Update(ctx, b.WorkspaceID, actor, b.AgentID, q)
	if err != nil {
		t.Fatal(err)
	}
	// Credential updates cannot replace the authoritative employee rule.
	b, err = channels.Get(ctx, b.WorkspaceID, actor, b.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	b, err = channels.Put(ctx, b.WorkspaceID, actor, b.AgentID, service.ChannelPut{Version: b.Revision, AppID: b.AppID, Enabled: true, History: b.History, Access: service.ChannelAccess{Mode: "allowlist", AllowDepartmentIDs: []string{"od_team"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Access.AllowDepartmentRules) != 1 || !b.Access.AllowDepartmentRules[0].IncludeDescendants {
		t.Fatal("credentials overwrote recursive rule")
	}
	if _, err = db.Exec(`UPDATE platform_channel_directory_state SET expires_at=now()-interval '1 second' WHERE binding_id=$1`, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = api.Update(ctx, b.WorkspaceID, actor, b.AgentID, q); err != nil {
		t.Fatal("unchanged stale rule retention", err)
	}
}

func TestDirectoryMigrationPreservesExpiredSnapshotsAndDirectRules(t *testing.T) {
	db, _, store, b, _, _ := directoryFixture(t)
	publishDirectoryFixture(t, store, b)
	source, err := os.ReadFile("../../../../event-hub/database/postgres/migrations/000026_channel_department_descendants.sql")
	if err != nil {
		t.Fatal(err)
	}
	statements := strings.SplitN(string(source), "-- +goose StatementBegin", 2)[0]
	for _, test := range []struct {
		name, expires      string
		full, wantExtended bool
	}{{"valid", "now()+interval '1 hour'", true, true}, {"expired", "now()-interval '1 second'", true, false}, {"revoked", "now()+interval '1 hour'", false, false}} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := db.Exec(`UPDATE platform_channel_bindings SET access='{"mode":"allowlist","allow_department_ids":["od_team"]}' WHERE id=$1`, b.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`UPDATE platform_channel_directory_state SET synced_at=now(),expires_at=`+test.expires+`,full_snapshot=$2 WHERE binding_id=$1`, b.ID, test.full); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(statements); err != nil {
				t.Fatal(err)
			}
			var extended, recursive bool
			if err := db.QueryRow(`SELECT expires_at>synced_at+interval '23 hours' FROM platform_channel_directory_state WHERE binding_id=$1`, b.ID).Scan(&extended); err != nil {
				t.Fatal(err)
			}
			if extended != test.wantExtended {
				t.Fatal("unexpected revival/extension", extended)
			}
			if err := db.QueryRow(`SELECT (access->'allow_department_rules'->0->>'include_descendants')::boolean FROM platform_channel_bindings WHERE id=$1`, b.ID).Scan(&recursive); err != nil || recursive {
				t.Fatal("old selection broadened", recursive, err)
			}
		})
	}
}

func TestDirectoryRecursiveTraversalBudgetFailsClosed(t *testing.T) {
	db, _, store, b, _, _ := directoryFixture(t)
	publishDirectoryFixture(t, store, b)
	if _, err := db.Exec(`INSERT INTO platform_channel_directory_departments(binding_id,department_id,display_name,parent_id) SELECT $1,'leaf_'||n,'leaf','od_team' FROM generate_series(1,1000) n`, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE platform_channel_directory_users SET department_ids=(SELECT jsonb_agg('leaf_'||n) FROM generate_series(1,1000) n) WHERE binding_id=$1 AND user_id='ou_b'`, b.ID); err != nil {
		t.Fatal(err)
	}
	var result sql.NullBool
	query := `SELECT platform_channel_directory_department_match($1,'ou_b',(SELECT jsonb_agg(jsonb_build_object('department_id','unrelated_'||n,'include_descendants',true)) FROM generate_series(1,$2) n))`
	if err := db.QueryRow(query, b.ID, 1).Scan(&result); err != nil || !result.Valid || result.Bool {
		t.Fatal("small proven non-match", result, err)
	}
	if err := db.QueryRow(query, b.ID, 6).Scan(&result); err != nil || result.Valid {
		t.Fatal("budget exhaustion must be unknown", result, err)
	}
	if err := db.QueryRow(`SELECT platform_channel_directory_department_match($1,'ou_b',(SELECT jsonb_agg(jsonb_build_object('department_id','unrelated_'||n,'include_descendants',true)) FROM generate_series(1,6) n)||'[{"department_id":"leaf_1","include_descendants":false}]'::jsonb)`, b.ID).Scan(&result); err != nil || !result.Valid || !result.Bool {
		t.Fatal("direct proof precedes budget", result, err)
	}
}
