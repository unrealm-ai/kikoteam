package repository

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	managercontroller "github.com/unrealm-ai/kikoteam/backend/internal/manager/controller"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func TestChannelMediaSharedRetryBudgetAndNonblockingInbox(t *testing.T) {
	for _, tc := range []struct {
		name    string
		text    string
		partial bool
	}{
		{"preserve text", "Analyze the file", false},
		{"submit attachment-only before failure", "", false},
		{"submit attachment post before failure", "[图片附件未读取]", false},
		{"preserve both references with mixed outcomes", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, m, store, _, _, _, binding := channelManagerFixture(t)
			root := t.TempDir()
			m.ProjectRoot = root
			ctx := t.Context()
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			v := inboundFixture(binding, "attachment-retry", "alice")
			v.Text = tc.text
			v.MessageType = "post"
			v.Media = []manager.MediaReference{{Kind: "file", Name: "report.csv", Locator: "sealed"}}
			if tc.partial {
				v.Media = append(v.Media, manager.MediaReference{Kind: "file", Name: "other.csv", Locator: "other-sealed"})
			}
			box, err := store.ReceiveChannel(ctx, v)
			must(err)
			must(store.ProcessChannelInbox(ctx, m))
			// A later ordinary message must be admitted before any download is attempted.
			later, err := store.ReceiveChannel(ctx, inboundFixture(binding, "later-text", "alice"))
			must(err)
			must(store.ProcessChannelInbox(ctx, m))
			var laterInput sql.NullString
			must(db.QueryRow(`SELECT input_id FROM manager_channel_inbox WHERE id=$1`, later.ID).Scan(&laterInput))
			if !laterInput.Valid {
				t.Fatal("later text is blocked by pending attachment")
			}
			const token = "test-channel-media-retry-secret-0000"
			mux := http.NewServeMux()
			(&managercontroller.ChannelServer{Store: store, Manager: m, Token: token}).Routes(mux)
			post := func(method, path, lease, body string, status int) []byte {
				t.Helper()
				r := httptest.NewRequest(method, "/api/internal/v1/channel-media/"+path, strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer "+token)
				r.Header.Set("X-Kiko-Media-Lease", lease)
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, r)
				if w.Code != status {
					t.Fatalf("%s %s: want %d got %d: %s", method, path, status, w.Code, w.Body)
				}
				return w.Body.Bytes()
			}
			claim := func() *manager.MediaJob {
				t.Helper()
				var response struct{ Job *manager.MediaJob }
				must(json.Unmarshal(post(http.MethodPost, "claim", "", "", 200), &response))
				return response.Job
			}
			next := func() manager.MediaJob {
				t.Helper()
				j := claim()
				if j == nil || j.InboxID != box.ID {
					t.Fatalf("expected attachment claim for %s: %+v", box.ID, j)
				}
				return *j
			}
			expire := func() {
				reviewExec(t, db, `UPDATE manager_channel_media SET lease_until=now()-interval '1 second' WHERE inbox_id=$1 AND ordinal=0`, box.ID)
			}
			j := next()
			if tc.partial {
				other := next()
				if other.Ordinal != 1 {
					t.Fatal("wanted the next attachment", other.Ordinal)
				}
				post(http.MethodPut, fmt.Sprintf("%s/%d", other.InboxID, other.Ordinal), other.Lease, "remaining attachment", 200)
			}
			post(http.MethodPost, fmt.Sprintf("%s/%d/failed", j.InboxID, j.Ordinal), j.Lease, "", 200)
			if j := claim(); j != nil {
				t.Fatalf("download failure ignored backoff: %+v", j)
			}
			expire()
			j = next()
			// Real internal handler with unusable disk: PUT fails; the same
			// claimed attempt must also account for storage and settle it.
			m.ProjectRoot = filepath.Join(root, "not-a-directory")
			must(os.WriteFile(m.ProjectRoot, []byte("x"), 0600))
			path := fmt.Sprintf("%s/%d", j.InboxID, j.Ordinal)
			post(http.MethodPut, path, j.Lease, "frozen", 503)
			post(http.MethodPost, path+"/failed", j.Lease, "", 200)
			if j := claim(); j != nil {
				t.Fatalf("storage failure ignored backoff: %+v", j)
			}
			expire()
			j = next()
			// Worker disappears before providing a result on the final claim.
			expire()
			if j := claim(); j != nil {
				t.Fatalf("expired final claim granted another attempt: %+v", j)
			}
			post(http.MethodPut, path, j.Lease, "late", 404)
			var count int
			var code sql.NullString
			must(db.QueryRow(`SELECT attempts,error_code FROM manager_channel_media WHERE inbox_id=$1 AND ordinal=0`, box.ID).Scan(&count, &code))
			if count != 3 || !code.Valid || code.String != "unavailable" {
				t.Fatal(count, code)
			}
			m.ProjectRoot = root
			reviewExec(t, db, `UPDATE manager_channel_inbox SET lease_until=NULL WHERE id=$1`, box.ID)
			must(store.ProcessChannelInbox(ctx, m))
			var state string
			var input, text, inboxError sql.NullString
			must(db.QueryRow(`SELECT n.status,n.input_id,i.text,n.error_code FROM manager_channel_inbox n LEFT JOIN manager_inputs i ON i.id=n.input_id WHERE n.id=$1`, box.ID).Scan(&state, &input, &text, &inboxError))
			if state != "accepted" || !input.Valid || text.String != tc.text || inboxError.Valid {
				t.Fatal("original message was not admitted independently of the attachment", state, input, text, inboxError)
			}
			var metadata string
			must(db.QueryRow(`SELECT attachments::text FROM manager_inputs WHERE id=$1`, input.String).Scan(&metadata))
			var refs []manager.Attachment
			must(json.Unmarshal([]byte(metadata), &refs))
			want := 1
			if tc.partial {
				want++
			}
			if len(refs) != want {
				t.Fatalf("expected %d attachment references: %+v", want, refs)
			}
			for i, a := range refs {
				if a.ID != fmt.Sprintf("%s:%d", box.ID, i) || a.Status != "downloading" || a.Path != "" {
					t.Fatalf("invalid immutable input reference: %+v", a)
				}
			}

		})
	}
}

func TestChannelMediaReconcilesPublishedFileAfterFinalLeaseExpires(t *testing.T) {
	db, m, store, workspace, _, _, binding := channelManagerFixture(t)
	m.ProjectRoot = t.TempDir()
	ctx := t.Context()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	v := inboundFixture(binding, "attachment-recover", "alice")
	v.MessageType = "file"
	v.Text = ""
	v.Media = []manager.MediaReference{{Kind: "file", Name: "report.csv", Locator: "sealed"}}
	box, err := store.ReceiveChannel(ctx, v)
	must(err)
	must(store.ProcessChannelInbox(ctx, m))
	reviewExec(t, db, `UPDATE manager_channel_media SET attempts=2 WHERE inbox_id=$1`, box.ID)
	j, err := store.ClaimMedia(ctx, m)
	must(err)
	if j == nil {
		t.Fatal("missing final media attempt")
	}
	// Fail only the DB receipt write after the project's upload journal
	// has published the file. No callback makes it back before expiry.
	reviewExec(t, db, `CREATE FUNCTION test_lose_media_receipt() RETURNS trigger LANGUAGE plpgsql AS $$
	 BEGIN IF NEW.path IS NOT NULL THEN RAISE EXCEPTION 'test: media receipt unavailable'; END IF; RETURN NEW; END $$`)
	reviewExec(t, db, `CREATE TRIGGER test_lose_media_receipt BEFORE UPDATE ON manager_channel_media FOR EACH ROW EXECUTE FUNCTION test_lose_media_receipt()`)
	if err := store.FinishMedia(ctx, m, j.InboxID, j.Ordinal, j.Lease, []byte("frozen"), false); err == nil {
		t.Fatal("expected receipt failure after upload")
	}
	reviewExec(t, db, `DROP TRIGGER test_lose_media_receipt ON manager_channel_media`)
	reviewExec(t, db, `UPDATE manager_channel_media SET lease_until=now()-interval '1 second' WHERE inbox_id=$1`, box.ID)
	retry, err := store.ClaimMedia(ctx, m)
	must(err)
	if retry != nil {
		t.Fatalf("published file should reconcile without another download: %+v", retry)
	}
	var conv, path string
	var count int
	must(db.QueryRow(`SELECT n.conversation_id,m.path,m.attempts FROM manager_channel_media m JOIN manager_channel_inbox n ON n.id=m.inbox_id WHERE m.inbox_id=$1`, box.ID).Scan(&conv, &path, &count))
	if count != 3 || path != "uploads/report.csv" {
		t.Fatal(count, path)
	}
	dir := filepath.Join(m.ProjectRoot, "workspaces", workspace, "projects", "conv-"+conv)
	data, err := os.ReadFile(filepath.Join(dir, path))
	must(err)
	entries, err := os.ReadDir(filepath.Join(dir, "uploads"))
	must(err)
	if string(data) != "frozen" || len(entries) != 1 {
		t.Fatal("upload not recovered exactly once", string(data), entries)
	}
	reviewExec(t, db, `UPDATE manager_channel_inbox SET lease_until=NULL WHERE id=$1`, box.ID)
	must(store.ProcessChannelInbox(ctx, m))
	var state string
	must(db.QueryRow(`SELECT status FROM manager_channel_inbox WHERE id=$1`, box.ID).Scan(&state))
	if state != "accepted" {
		t.Fatal("recovered attachment not delivered to inbox", state)
	}
}
