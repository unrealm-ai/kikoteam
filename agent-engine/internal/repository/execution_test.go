package repository

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func claimTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("BACKEND_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("BACKEND_TEST_DATABASE_URL required for real PostgreSQL")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*cfg)
	t.Cleanup(func() {
		if err := admin.Close(); err != nil {
			t.Error(err)
		}
	})
	schema := fmt.Sprintf("engine_claim_%d", time.Now().UnixNano())
	if _, err = admin.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); err != nil {
			t.Error(err)
		}
	})
	cfg.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	files, err := filepath.Glob("../../../event-hub/database/postgres/migrations/*.sql")
	if err != nil || len(files) == 0 {
		t.Fatal("missing migrations", err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(strings.Split(string(data), "-- +goose Down")[0]); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	return db
}
func fixtureID(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }
func claimExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}
func TestClaimChannelIdentityAndRevocation(t *testing.T) {
	for _, mode := range []string{"channel", "tracked_channel", "web", "disabled_binding", "changed_generation", "denied_sender", "wrong_tenant", "stale_employee", "disabled_web", "concurrent_revocation"} {
		t.Run(mode, func(t *testing.T) {
			db := claimTestDB(t)
			ws, user, model, agent, binding, principal, conv, session, input, engine, assignment := fixtureID(1), fixtureID(2), fixtureID(3), fixtureID(4), fixtureID(5), fixtureID(6), fixtureID(7), fixtureID(8), fixtureID(9), fixtureID(10), fixtureID(11)
			claimExec(t, db, `INSERT INTO platform_accounts(id,email,name)VALUES($1,'fixture@example.test','fixture')`, user)
			claimExec(t, db, `INSERT INTO platform_workspaces(id,name)VALUES($1,'fixture')`, ws)
			claimExec(t, db, `INSERT INTO platform_members(workspace_id,user_id,role)VALUES($1,$2,'owner')`, ws, user)
			claimExec(t, db, `INSERT INTO platform_model_profiles(id,workspace_id,name,protocol,base_url,model_id,version,has_key,validated,credential,credential_revision,vision_status,tested_at)VALUES($1,$2,'fixture','chat_completions','http://localhost','fixture',1,false,true,''::bytea,1,'unknown',now())`, model, ws)
			claimExec(t, db, `INSERT INTO platform_agents(id,workspace_id,name,model_profile_id,version,config_version,admission_epoch)VALUES($1,$2,'fixture',$3,1,1,1)`, agent, ws, model)
			claimExec(t, db, `UPDATE platform_agents SET usage_config=jsonb_set(usage_config,'{access,allow}',jsonb_build_array(jsonb_build_object('channel','web','kind','user','id',$2::text))) WHERE id=$1`, agent, user)
			claimExec(t, db, `INSERT INTO platform_channel_bindings(id,workspace_id,agent_id,provider,connection_generation,rules_version,config_revision,enabled,app_id,external_tenant_id,credential_ciphertext,trigger,history,access)VALUES($1,$2,$3,'feishu',1,1,1,true,'app','tenant',''::bytea,'{}','{}','{"mode":"all"}')`, binding, ws, agent)
			claimExec(t, db, `INSERT INTO manager_channel_principals(id,workspace_id,binding_id,provider,external_tenant_id,external_user_id)VALUES($1,$2,$3,'feishu','tenant','sender')`, principal, ws, binding)
			web := mode == "web" || mode == "disabled_web"
			var actor, channel, channelBinding, chatID, chatType any
			if web {
				actor = user
			} else {
				channel = principal
				channelBinding = binding
				chatID = "chat"
				chatType = "direct"
			}
			claimExec(t, db, `INSERT INTO manager_conversations(id,workspace_id,agent_id,created_by,channel_created_by,channel_binding_id,external_chat_id,channel_chat_type,channel_tenant_id)VALUES($1,$2,$3,$4,$5,$6,$7,$8,'tenant')`, conv, ws, agent, actor, channel, channelBinding, chatID, chatType)
			claimExec(t, db, `INSERT INTO manager_sessions(id,workspace_id,agent_id,conversation_id,created_by,channel_created_by,mode,configuration,engine_id,assignment_id,generation,last_input_seq)VALUES($1,$2,$3,$4,$5,$6,'chat','{}',$7,$8,1,1)`, session, ws, agent, conv, actor, channel, engine, assignment)
			var generation any
			if !web {
				generation = 1
			}
			claimExec(t, db, `INSERT INTO manager_inputs(id,workspace_id,session_id,submitted_by,channel_principal_id,channel_generation,admission_epoch,seq,text)VALUES($1,$2,$3,$4,$5,$6,1,1,'hello')`, input, ws, session, actor, channel, generation)
			if mode == "tracked_channel" {
				claimExec(t, db, `UPDATE manager_inputs SET context_state='pending' WHERE id=$1`, input)
			}
			switch mode {
			case "disabled_binding":
				claimExec(t, db, `UPDATE platform_channel_bindings SET enabled=false`)
			case "changed_generation":
				claimExec(t, db, `UPDATE platform_channel_bindings SET connection_generation=2`)
			case "denied_sender":
				claimExec(t, db, `UPDATE platform_channel_bindings SET access='{"mode":"all","deny_ids":["sender"]}'`)
			case "wrong_tenant":
				claimExec(t, db, `UPDATE platform_channel_bindings SET external_tenant_id='other'`)
			case "stale_employee":
				claimExec(t, db, `UPDATE platform_agents SET admission_epoch=2`)
			case "disabled_web":
				claimExec(t, db, `UPDATE platform_members SET status='disabled'`)
			}
			store := Store{DB: db, EngineID: engine}
			w, err := store.Refresh(context.Background(), Assignment{SessionID: session, AssignmentID: assignment, Generation: 1})
			if err != nil {
				t.Fatal(err)
			}
			var turn *Turn
			if mode == "concurrent_revocation" {
				revocation, e := db.Begin()
				if e != nil {
					t.Fatal(e)
				}
				if _, e = revocation.Exec(`UPDATE platform_channel_bindings SET enabled=false`); e != nil {
					t.Fatal(e)
				}
				type result struct {
					turn *Turn
					err  error
				}
				done := make(chan result, 1)
				go func() { v, e := store.Claim(context.Background(), w, fixtureID(12)); done <- result{v, e} }()
				select {
				case v := <-done:
					if rollbackErr := revocation.Rollback(); rollbackErr != nil {
						t.Error(rollbackErr)
					}
					t.Fatalf("claim crossed pending revocation: %+v", v)
				case <-time.After(100 * time.Millisecond):
				}
				if e = revocation.Commit(); e != nil {
					t.Fatal(e)
				}
				select {
				case v := <-done:
					turn, err = v.turn, v.err
				case <-time.After(5 * time.Second):
					t.Fatal("claim blocked after revocation")
				}
			} else {
				turn, err = store.Claim(context.Background(), w, fixtureID(12))
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "channel" {
				claimExec(t, db, `UPDATE manager_inputs SET context_state='pending' WHERE id=$1`, input)
				if err = store.ContextReceipt(context.Background(), w, input, "unknown"); err != nil {
					t.Fatal(err)
				}
				if err = store.ContextReceipt(context.Background(), w, input, "committed"); err != nil {
					t.Fatal(err)
				}
				if err = store.ContextReceipt(context.Background(), w, input, "unknown"); err != nil {
					t.Fatal(err)
				}
				var state string
				var committed bool
				if err = db.QueryRow(`SELECT context_state,context_committed_at IS NOT NULL FROM manager_inputs WHERE id=$1`, input).Scan(&state, &committed); err != nil || state != "committed" || !committed {
					t.Fatalf("receipt downgrade state=%s err=%v", state, err)
				}
			}
			if mode == "channel" {
				for seq := int64(2); seq <= 11; seq++ {
					claimExec(t, db, `INSERT INTO manager_inputs(id,workspace_id,session_id,channel_principal_id,channel_generation,admission_epoch,seq,text,status,started_at,finished_at,context_state)VALUES($1,$2,$3,$4,1,1,$5,'old','processed',now(),now(),'unknown')`, fixtureID(100+int(seq)), ws, session, principal, seq)
				}
				batch, e := store.UnresolvedContextInputs(context.Background(), w, 0)
				if e != nil || len(batch) != 8 || batch[0].Seq != 2 || batch[7].Seq != 9 {
					t.Fatalf("unbounded receipt batch %+v %v", batch, e)
				}
				next, e := store.UnresolvedContextInputs(context.Background(), w, batch[7].Seq)
				if e != nil || len(next) != 2 || next[0].Seq != 10 {
					t.Fatalf("receipt pagination starved %+v %v", next, e)
				}
			}
			if mode == "channel" || mode == "tracked_channel" || mode == "web" {
				if turn == nil {
					t.Fatal("authorized input rejected")
				}
				if !turn.Input.NeedsContextReceipt {
					t.Fatal("receipt leaked to untracked input")
				}
				if web {
					if turn.Input.SubmittedBy != user || turn.Input.ChannelPrincipalID != nil || turn.Input.PrincipalType != "web" {
						t.Fatalf("web identity %+v", turn.Input)
					}
					verifyInterruptedAssignment(t, store, w, *turn)
				} else {
					if turn.Input.SubmittedBy != "" || turn.Input.ChannelPrincipalID == nil || *turn.Input.ChannelPrincipalID != principal || turn.Input.PrincipalType != "channel" {
						t.Fatalf("channel impersonates web: %+v", turn.Input)
					}
				}
			} else {
				if turn != nil {
					t.Fatal("revoked input executed")
				}
				var status string
				if err = db.QueryRow(`SELECT status FROM manager_inputs WHERE id=$1`, input).Scan(&status); err != nil || status != "rejected" {
					t.Fatalf("status=%s err=%v", status, err)
				}
			}
		})
	}
}

func verifyInterruptedAssignment(t *testing.T, store Store, w Work, turn Turn) {
	t.Helper()
	ctx := context.Background()
	w.ActiveTurnID = &turn.ID
	if settled, err := store.InterruptPreviousTurn(ctx, w); err != nil || settled {
		t.Fatalf("interrupted current execution: %v %v", settled, err)
	}
	store.EngineID = fixtureID(40)
	w.EngineID, w.AssignmentID, w.Generation = store.EngineID, fixtureID(41), 2
	claimExec(t, store.DB, `UPDATE manager_sessions SET engine_id=$1,assignment_id=$2,generation=$3 WHERE id=$4`, w.EngineID, w.AssignmentID, w.Generation, w.SessionID)
	if settled, err := store.InterruptPreviousTurn(ctx, w); err != nil || !settled {
		t.Fatalf("settle abandoned execution: %v %v", settled, err)
	}
	next, err := store.Claim(ctx, w, fixtureID(42))
	if err != nil || next != nil {
		t.Fatalf("interrupted input was replayed: %+v %v", next, err)
	}
	var status, inputStatus string
	if err = store.DB.QueryRow(`SELECT t.status,i.status FROM manager_turns t JOIN manager_inputs i ON i.id=t.input_id WHERE t.id=$1`, turn.ID).Scan(&status, &inputStatus); err != nil || status != "interrupted" || inputStatus != "processed" {
		t.Fatalf("turn=%q input=%q err=%v", status, inputStatus, err)
	}
}
