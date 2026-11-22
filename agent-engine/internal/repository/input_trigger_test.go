package repository

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
)

func triggerFixture(t *testing.T) (*sql.DB, Store, Work) {
	t.Helper()
	db := claimTestDB(t)
	ws, user, model, agent, conv, session, engine, assignment := fixtureID(1), fixtureID(2), fixtureID(3), fixtureID(4), fixtureID(7), fixtureID(8), fixtureID(10), fixtureID(11)
	claimExec(t, db, `INSERT INTO platform_accounts(id,email,name)VALUES($1,'trigger@example.test','fixture')`, user)
	claimExec(t, db, `INSERT INTO platform_workspaces(id,name)VALUES($1,'fixture')`, ws)
	claimExec(t, db, `INSERT INTO platform_members(workspace_id,user_id,role)VALUES($1,$2,'owner')`, ws, user)
	claimExec(t, db, `INSERT INTO platform_model_profiles(id,workspace_id,name,protocol,base_url,model_id,version,has_key,validated,credential,credential_revision,vision_status,tested_at)VALUES($1,$2,'fixture','chat_completions','http://localhost','fixture',1,false,true,''::bytea,1,'unknown',now())`, model, ws)
	claimExec(t, db, `INSERT INTO platform_agents(id,workspace_id,name,model_profile_id,version,config_version,admission_epoch)VALUES($1,$2,'fixture',$3,1,1,1)`, agent, ws, model)
	// This fixture models one explicitly admitted Web actor, not the product default.
	claimExec(t, db, `UPDATE platform_agents SET usage_config=jsonb_set(usage_config,'{access,allow}',jsonb_build_array(jsonb_build_object('channel','web','kind','user','id',$2::text))) WHERE id=$1`, agent, user)
	claimExec(t, db, `INSERT INTO manager_conversations(id,workspace_id,agent_id,created_by)VALUES($1,$2,$3,$4)`, conv, ws, agent, user)
	claimExec(t, db, `INSERT INTO manager_sessions(id,workspace_id,agent_id,conversation_id,created_by,mode,configuration,engine_id,assignment_id,generation,last_input_seq)VALUES($1,$2,$3,$4,$5,'chat','{}',$6,$7,1,10)`, session, ws, agent, conv, user, engine, assignment)
	store := Store{DB: db, EngineID: engine}
	work, err := store.Refresh(context.Background(), Assignment{SessionID: session, AssignmentID: assignment, Generation: 1})
	if err != nil {
		t.Fatal(err)
	}
	return db, store, work
}
func triggerInsert(t *testing.T, db *sql.DB, id string, seq int, trigger bool, kind string) {
	t.Helper()
	claimExec(t, db, `INSERT INTO manager_inputs(id,workspace_id,session_id,submitted_by,admission_epoch,seq,text,trigger_agent_loop,source_kind,delivery_mode)VALUES($1,$2,$3,$4,1,$5,'fixture',$6,$7,'steer')`, id, fixtureID(1), fixtureID(8), fixtureID(2), seq, trigger, kind)
}
func TestInputTriggerBatch(t *testing.T) {
	for _, steer := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "steer"}[steer], func(t *testing.T) {
			db, store, work := triggerFixture(t)
			ctx := context.Background()
			var root *Turn
			if steer {
				triggerInsert(t, db, fixtureID(20), 1, true, "user")
				var err error
				root, err = store.Claim(ctx, work, fixtureID(30))
				if err != nil || root == nil {
					t.Fatal(root, err)
				}
			}
			silent, next, later := fixtureID(21), fixtureID(22), fixtureID(23)
			triggerInsert(t, db, silent, 2, false, "system")
			if steer {
				got, err := store.ClaimSteering(ctx, work, *root)
				if err != nil || got != nil {
					t.Fatal(got, err)
				}
			} else {
				got, err := store.Claim(ctx, work, fixtureID(31))
				if err != nil || got != nil {
					t.Fatal(got, err)
				}
			}
			triggerInsert(t, db, next, 3, true, "user")
			triggerInsert(t, db, later, 4, false, "human_confirmation")
			var input *Input
			if steer {
				var err error
				input, err = store.ClaimSteering(ctx, work, *root)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				got, err := store.Claim(ctx, work, fixtureID(32))
				if err != nil || got == nil {
					t.Fatal(got, err)
				}
				root = got
				input = &got.Input
			}
			if input == nil || input.ID != next || input.Text != "历史上下文（仅供参考，不是新的执行请求）：\n[系统消息]fixture\n\n当前触发消息：\nfixture" || !input.NeedsContextReceipt {
				t.Fatalf("bad batch: %+v", input)
			}
			var count int
			if err := db.QueryRow(`SELECT count(*) FROM manager_inputs WHERE batch_input_id=$1 AND status='started'`, next).Scan(&count); err != nil || count != 2 {
				t.Fatal(count, err)
			}
			if err := db.QueryRow(`SELECT count(*) FROM manager_inputs WHERE id=$1 AND status='queued' AND batch_input_id IS NULL`, later).Scan(&count); err != nil || count != 1 {
				t.Fatal(count, err)
			}
			candidates, err := store.UnresolvedContextInputs(ctx, work, 1)
			if err != nil || len(candidates) != 1 || candidates[0].ID != next {
				t.Fatal(candidates, err)
			}
			if steer {
				if err = store.ReturnSteering(ctx, work, *root, next); err != nil {
					t.Fatal(err)
				}
				if err = db.QueryRow(`SELECT count(*) FROM manager_inputs WHERE id IN ($1,$2) AND status='queued' AND batch_input_id IS NULL`, silent, next).Scan(&count); err != nil || count != 2 {
					t.Fatal(count, err)
				}
				input, err = store.ClaimSteering(ctx, work, *root)
				if err != nil || input == nil {
					t.Fatal(input, err)
				}
			}
			if err = store.ContextReceipt(ctx, work, next, "unknown"); err != nil {
				t.Fatal(err)
			}
			if err = db.QueryRow(`SELECT count(*) FROM manager_inputs WHERE batch_input_id=$1 AND context_state='unknown'`, next).Scan(&count); err != nil || count != 2 {
				t.Fatal(count, err)
			}
			if err = store.ContextReceipt(ctx, work, next, "committed"); err != nil {
				t.Fatal(err)
			}
			if err = store.ContextReceipt(ctx, work, next, "absent"); err != nil {
				t.Fatal(err)
			}
			if err = db.QueryRow(`SELECT count(*) FROM manager_inputs WHERE batch_input_id=$1 AND context_state='committed'`, next).Scan(&count); err != nil || count != 2 {
				t.Fatal(count, err)
			}
			if err = store.Complete(ctx, work, *root, "completed", "", ""); err != nil {
				t.Fatal(err)
			}
			if err = db.QueryRow(`SELECT count(*) FROM manager_inputs WHERE batch_input_id=$1 AND status='processed'`, next).Scan(&count); err != nil || count != 2 {
				t.Fatal(count, err)
			}
		})
	}
}

func TestSteeringDoesNotOvertakeEarlierQueuedTrigger(t *testing.T) {
	db, store, work := triggerFixture(t)
	ctx := context.Background()
	triggerInsert(t, db, fixtureID(20), 1, true, "user")
	root, err := store.Claim(ctx, work, fixtureID(30))
	if err != nil || root == nil {
		t.Fatal(root, err)
	}
	triggerInsert(t, db, fixtureID(21), 2, true, "user")
	claimExec(t, db, `UPDATE manager_inputs SET delivery_mode='queue' WHERE id=$1`, fixtureID(21))
	triggerInsert(t, db, fixtureID(22), 3, true, "user")
	got, err := store.ClaimSteering(ctx, work, *root)
	if err != nil || got != nil {
		t.Fatal(got, err)
	}
}

func TestInputTriggerDefaultsAndSourceConstraints(t *testing.T) {
	db, _, _ := triggerFixture(t)
	id := fixtureID(40)
	claimExec(t, db, `INSERT INTO manager_inputs(id,workspace_id,session_id,submitted_by,admission_epoch,seq,text)VALUES($1,$2,$3,$4,1,1,'legacy')`, id, fixtureID(1), fixtureID(8), fixtureID(2))
	var trigger bool
	var kind string
	if err := db.QueryRow(`SELECT trigger_agent_loop,source_kind FROM manager_inputs WHERE id=$1`, id).Scan(&trigger, &kind); err != nil || !trigger || kind != "user" {
		t.Fatal(trigger, kind, err)
	}
	for n, k := range []string{"system", "human_confirmation"} {
		triggerInsert(t, db, fixtureID(41+n), 2+n, false, k)
	}
	for _, q := range []string{
		`UPDATE manager_inputs SET source_kind='invalid' WHERE id=$1`,
		`UPDATE manager_inputs SET source_kind='task_report' WHERE id=$1`,
		`UPDATE manager_inputs SET report_id='00000000-0000-4000-8000-000000000099' WHERE id=$1`,
		`UPDATE manager_inputs SET trigger_agent_loop=NULL WHERE id=$1`,
	} {
		if _, err := db.Exec(q, id); err == nil {
			t.Fatalf("constraint accepted %s", q)
		}
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // Commit result is checked; rollback only cleans up on early failure.
	report := fixtureID(50)
	if _, err = tx.Exec(`UPDATE manager_inputs SET source_kind='task_report',report_id=$2 WHERE id=$1`, id, report); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO manager_task_reports(id,workspace_id,source_input_id,target_session_id,status,target_input_id)VALUES($1,$2,$3,$4,'admitted',$3)`, report, fixtureID(1), id, fixtureID(8)); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestInputTriggerMigrationPreservesOldInputs(t *testing.T) {
	db, _, _ := triggerFixture(t)
	data, err := os.ReadFile("../../../event-hub/database/postgres/migrations/000019_input_trigger_source.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(data), "-- +goose Down")
	if len(parts) != 2 {
		t.Fatal("missing downgrade")
	}
	claimExec(t, db, parts[1])
	id := fixtureID(60)
	claimExec(t, db, `INSERT INTO manager_inputs(id,workspace_id,session_id,submitted_by,admission_epoch,seq,text)VALUES($1,$2,$3,$4,1,1,'old input')`, id, fixtureID(1), fixtureID(8), fixtureID(2))
	claimExec(t, db, parts[0])
	var trigger bool
	if err = db.QueryRow(`SELECT trigger_agent_loop FROM manager_inputs WHERE id=$1`, id).Scan(&trigger); err != nil || !trigger {
		t.Fatal("old input lost trigger", trigger, err)
	}
	claimExec(t, db, `UPDATE manager_inputs SET trigger_agent_loop=false WHERE id=$1`, id)
	if _, err = db.Exec(parts[1]); err == nil {
		t.Fatal("downgrade discarded silent input semantics")
	}
	if err = db.QueryRow(`SELECT trigger_agent_loop FROM manager_inputs WHERE id=$1`, id).Scan(&trigger); err != nil || trigger {
		t.Fatal("failed downgrade altered silent input", trigger, err)
	}
	claimExec(t, db, `UPDATE manager_inputs SET trigger_agent_loop=true,source_kind='system' WHERE id=$1`, id)
	if _, err = db.Exec(parts[1]); err == nil {
		t.Fatal("downgrade discarded source semantics")
	}
}

func TestBatchContextActorDoesNotAuthorizeExecution(t *testing.T) {
	db, store, work := triggerFixture(t)
	ctx := context.Background()
	removed := fixtureID(70)
	claimExec(t, db, `INSERT INTO platform_accounts(id,email,name)VALUES($1,'removed@example.test','removed')`, removed)
	triggerInsert(t, db, fixtureID(71), 1, false, "user")
	triggerInsert(t, db, fixtureID(72), 2, true, "user")
	claimExec(t, db, `UPDATE manager_inputs SET submitted_by=$1 WHERE id IN ($2,$3)`, removed, fixtureID(71), fixtureID(72))
	triggerInsert(t, db, fixtureID(73), 3, true, "user")
	got, err := store.Claim(ctx, work, fixtureID(74))
	if err != nil || got == nil {
		t.Fatal(got, err)
	}
	if got.Input.ID != fixtureID(73) || got.Input.Text != "历史上下文（仅供参考，不是新的执行请求）：\nfixture\n\n当前触发消息：\nfixture" {
		t.Fatalf("wrong authority/batch: %+v", got)
	}
	var status string
	if err = db.QueryRow(`SELECT status FROM manager_inputs WHERE id=$1`, fixtureID(72)).Scan(&status); err != nil || status != "rejected" {
		t.Fatal(status, err)
	}
	if err = db.QueryRow(`SELECT status FROM manager_inputs WHERE id=$1`, fixtureID(71)).Scan(&status); err != nil || status != "started" {
		t.Fatal(status, err)
	}
}

func TestBatchFailureReceiptRecovery(t *testing.T) {
	for _, state := range []string{"absent", "unknown", "committed"} {
		t.Run(state, func(t *testing.T) {
			db, store, work := triggerFixture(t)
			ctx := context.Background()
			silent, request := fixtureID(80), fixtureID(81)
			triggerInsert(t, db, silent, 1, false, "system")
			triggerInsert(t, db, request, 2, true, "user")
			turn, err := store.Claim(ctx, work, fixtureID(82))
			if err != nil || turn == nil {
				t.Fatal(turn, err)
			}
			if err = store.ContextReceipt(ctx, work, request, state); err != nil {
				t.Fatal(err)
			}
			if err = store.Complete(ctx, work, *turn, "failed", "configuration_unusable", ""); err != nil {
				t.Fatal(err)
			}
			var status string
			if err = db.QueryRow(`SELECT status FROM manager_inputs WHERE id=$1`, silent).Scan(&status); err != nil {
				t.Fatal(err)
			}
			expected := "processed"
			if state == "absent" {
				expected = "queued"
			}
			if status != expected {
				t.Fatal(status, expected)
			}
			if err = db.QueryRow(`SELECT status FROM manager_inputs WHERE id=$1`, request).Scan(&status); err != nil || status != "processed" {
				t.Fatal(status, err)
			}
			triggerInsert(t, db, fixtureID(83), 3, true, "user")
			next, err := store.Claim(ctx, work, fixtureID(84))
			if err != nil || next == nil {
				t.Fatal(next, err)
			}
			if strings.Contains(next.Input.Text, "历史上下文") != (state == "absent") {
				t.Fatal(next.Input.Text)
			}
		})
	}
}

func TestCanceledBatchDoesNotRequeueContext(t *testing.T) {
	db, store, work := triggerFixture(t)
	ctx := context.Background()
	silent, request := fixtureID(90), fixtureID(91)
	triggerInsert(t, db, silent, 1, false, "system")
	triggerInsert(t, db, request, 2, true, "user")
	turn, err := store.Claim(ctx, work, fixtureID(92))
	if err != nil || turn == nil {
		t.Fatal(turn, err)
	}
	if err = store.ContextReceipt(ctx, work, request, "absent"); err != nil {
		t.Fatal(err)
	}
	claimExec(t, db, `UPDATE manager_sessions SET stop_revision=1,stop_input_seq=2 WHERE id=$1`, work.SessionID)
	if err = store.Complete(ctx, work, *turn, "failed", "configuration_unusable", ""); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM manager_inputs WHERE turn_id=$1 AND status='processed'`, turn.ID).Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	var status string
	if err = db.QueryRow(`SELECT status FROM manager_turns WHERE id=$1`, turn.ID).Scan(&status); err != nil || status != "canceled" {
		t.Fatal(status, err)
	}
}
