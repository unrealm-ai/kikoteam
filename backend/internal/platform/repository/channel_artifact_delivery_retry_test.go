package repository

import (
	"testing"

	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func TestChannelArtifactDeliveryRetriesAreBoundedAndIndependent(t *testing.T) {
	for _, scenario := range []string{"negative_receipt", "expired_claim", "unknown", "expired_send"} {
		t.Run(scenario, func(t *testing.T) {
			db, svc, store, workspace, _, _, binding := channelManagerFixture(t)
			ctx := t.Context()
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			notice, err := store.ReceiveChannel(ctx, inboundFixture(binding, "artifact-retry", "alice"))
			must(err)
			must(store.ProcessChannelInbox(ctx, svc))

			turn, first, next, textID := manager.NewID(), manager.NewID(), manager.NewID(), manager.NewID()
			reviewExec(t, db, `INSERT INTO manager_turns(id,workspace_id,session_id,input_id,turn_no,assignment_id,generation,status,finished_at)
 SELECT $2,$3,i.session_id,i.id,1,$4,1,'completed',now() FROM manager_channel_inbox n JOIN manager_inputs i ON i.id=n.input_id WHERE n.id=$1`, notice.ID, turn, workspace, manager.NewID())
			deliveries := make(map[string]string)
			for _, artifact := range []string{first, next} {
				id := manager.NewID()
				deliveries[artifact] = id
				reviewExec(t, db, `INSERT INTO manager_task_artifacts(id,turn_id,path,name,media_type,version,size_bytes)
 VALUES($1,$2,$1::uuid::text,'artifact.csv','text/csv','fixture',0)`, artifact, turn)
				reviewExec(t, db, `INSERT INTO manager_artifact_selections(chat_turn_id,artifact_id,inbox_id) VALUES($1,$2,$3)`, turn, artifact, notice.ID)
				reviewExec(t, db, `INSERT INTO manager_channel_deliveries(id,inbox_id,binding_id,connection_generation,kind,text,content_format,artifact_id)
 VALUES($1,$2,$3,1,'artifact:' || $4::uuid::text || ':' || $5::uuid::text,'','file',$5)`, id, notice.ID, binding, turn, artifact)
			}
			reviewExec(t, db, `INSERT INTO manager_channel_deliveries(id,inbox_id,binding_id,connection_generation,kind,text)
 VALUES($1,$2,$3,1,'reply','ordinary text reply')`, textID, notice.ID, binding)

			claim := func() []manager.ChannelDelivery {
				t.Helper()
				rows, err := store.ClaimChannelDeliveries(ctx, 50, true, true)
				must(err)
				return rows
			}
			status := func(id, want string, wantAttempts int) {
				t.Helper()
				var got string
				var attempts int
				must(db.QueryRow(`SELECT status,attempts FROM manager_channel_deliveries WHERE id=$1`, id).Scan(&got, &attempts))
				if got != want || attempts != wantAttempts {
					t.Fatalf("delivery %s: got %s/%d, want %s/%d", id, got, attempts, want, wantAttempts)
				}
			}
			authorize := func(d manager.ChannelDelivery) manager.ChannelDelivery {
				t.Helper()
				v, err := store.AuthorizeChannelDelivery(ctx, d.ID, d.LeaseToken)
				must(err)
				return v
			}
			sent := func(d manager.ChannelDelivery) {
				t.Helper()
				v := authorize(d)
				must(store.CompleteChannelDelivery(ctx, d.ID, manager.DeliveryCompletion{LeaseToken: v.LeaseToken, TargetDigest: v.TargetDigest,
					Status: "sent", EvidenceType: "send_response", ProviderMessageID: d.ID, EvidenceID: d.ID}))
			}
			var oldReceipt manager.DeliveryCompletion
			var textSent bool
			maxAttempts := 3
			if scenario == "unknown" || scenario == "expired_send" {
				maxAttempts = 1
			}
			for attempt := 1; attempt <= maxAttempts; attempt++ {
				var found bool
				for _, d := range claim() {
					switch d.ID {
					case textID:
						sent(d)
						textSent = true
					case deliveries[first]:
						found = true
						if scenario == "expired_claim" {
							reviewExec(t, db, `UPDATE manager_channel_deliveries SET lease_until=now()-interval '1 minute' WHERE id=$1`, d.ID)
							continue
						}
						v := authorize(d)
						if scenario == "expired_send" {
							reviewExec(t, db, `UPDATE manager_channel_deliveries SET lease_until=now()-interval '1 minute' WHERE id=$1`, d.ID)
							continue
						}
						if attempt > 1 {
							must(store.CompleteChannelDelivery(ctx, d.ID, oldReceipt))
							status(d.ID, "sending", attempt)
						}
						receipt := manager.DeliveryCompletion{LeaseToken: v.LeaseToken, TargetDigest: v.TargetDigest,
							Status: "failed", EvidenceType: "not_dispatched", ErrorCode: "media_upload_failed"}
						if scenario == "unknown" {
							receipt.Status, receipt.EvidenceType, receipt.ErrorCode = "unknown", "", "provider_result_unknown"
						}
						must(store.CompleteChannelDelivery(ctx, d.ID, receipt))
						// A lost success response can replay the same receipt.
						must(store.CompleteChannelDelivery(ctx, d.ID, receipt))
						oldReceipt = receipt
					default:
						t.Fatalf("unexpected delivery before first file settles: %s", d.ID)
					}
				}
				if !found {
					t.Fatalf("missing attachment attempt %d", attempt)
				}
			}
			batch := claim()
			if len(batch) != 1 || batch[0].ID != deliveries[next] {
				t.Fatalf("sibling attachment was not released independently: %+v", batch)
			}
			sent(batch[0])
			if !textSent {
				t.Fatal("attachment failure held back the text reply")
			}
			status(textID, "sent", 1)
			status(deliveries[next], "sent", 1)
			want := "failed"
			if scenario == "unknown" || scenario == "expired_send" {
				want = "unknown"
			}
			status(deliveries[first], want, maxAttempts)
			if rows := claim(); len(rows) != 0 {
				t.Fatalf("terminal attachment was retried: %+v", rows)
			}
		})
	}
}
