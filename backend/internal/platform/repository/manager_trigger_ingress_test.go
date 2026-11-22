package repository

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	managerrepo "github.com/unrealm-ai/kikoteam/backend/internal/manager/repository"
	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

func TestHookIngressBucketClockAndConcurrency(t *testing.T) {
	st, _, w, _, _, b, _ := triggerFixture(t)
	tick := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	consume := func(at time.Time) (bool, error) {
		var ok bool
		err := st.DB.QueryRow(`SELECT manager_consume_hook_ingress($1,$2,$3)`, w, b.ID, at).Scan(&ok)
		return ok, err
	}
	var wg sync.WaitGroup
	var accepted atomic.Int32
	for range 36 {
		wg.Go(func() {
			ok, err := consume(tick)
			if err != nil {
				t.Error(err)
				return
			}
			if ok {
				accepted.Add(1)
			}
		})
	}
	wg.Wait()
	if n := accepted.Load(); n != 20 {
		t.Fatalf("expected exactly 20 concurrent admissions, got %d", n)
	}
	for _, tc := range []struct {
		at   time.Time
		want bool
	}{
		{tick.Add(-time.Second), false},
		{tick.Add(499999 * time.Microsecond), false},
		{tick.Add(500000 * time.Microsecond), true},
		{tick.Add(500000 * time.Microsecond), false},
		{tick.Add(1000000 * time.Microsecond), true},
	} {
		got, err := consume(tc.at)
		if err != nil || got != tc.want {
			t.Fatalf("clock boundary %s: got %t want %t err=%v", tc.at, got, tc.want, err)
		}
	}
	for i := range 21 {
		got, err := consume(tick.Add(time.Hour))
		if err != nil || got != (i < 20) {
			t.Fatalf("refill did not cap burst: i=%d ok=%t err=%v", i, got, err)
		}
	}
}

func TestHookIngressHTTPAdmissionReplayAndFailures(t *testing.T) {
	db, m, mux, w, actor, agent, _ := managerFixture(t)
	st := m.Store.(*managerrepo.Store)
	st.TriggerSecret = []byte(strings.Repeat("x", 32))
	ctx := context.Background()
	makeBinding := func(adapter string) (manager.Trigger, manager.TriggerCredential) {
		t.Helper()
		config := `{"adapter":"generic"}`
		actions := []string{"create_task", "supplement_task"}
		if adapter == "github_issue" {
			config = `{"adapter":"github_issue","repositoryId":123,"installationId":44}`
			actions = []string{"create_task"}
		}
		b, err := st.SaveTrigger(ctx, w, actor, agent, "", manager.TriggerWrite{Kind: "webhook", Config: json.RawMessage(config), AllowedActions: actions})
		if err != nil {
			t.Fatal(err)
		}
		key, err := st.CreateTriggerCredential(ctx, w, actor, agent, b.ID)
		if err != nil {
			t.Fatal(err)
		}
		b, err = st.SaveTrigger(ctx, w, actor, agent, b.ID, manager.TriggerWrite{Kind: b.Kind, Version: b.Version, Enabled: true, Config: b.Config, AllowedActions: b.AllowedActions})
		if err != nil {
			t.Fatal(err)
		}
		return b, key
	}
	send := func(b manager.Trigger, k manager.TriggerCredential, adapter, body string, auth bool) *httptest.ResponseRecorder {
		t.Helper()
		path := "/api/triggers/" + adapter + "/" + b.ID
		req := httptest.NewRequest(http.MethodPost, "https://kiko.example.test"+path, strings.NewReader(body))
		mac := hmac.New(sha256.New, []byte(k.Secret))
		var header, prefix string
		if adapter == "github" {
			header, prefix = "X-Hub-Signature-256", "sha256="
			req.Header.Set("X-GitHub-Event", "issues")
			req.Header.Set("X-GitHub-Delivery", manager.NewID())
			_, _ = mac.Write([]byte(body))
		} else {
			header = "X-Signature"
			stamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
			req.Header.Set("X-Key-Id", k.KeyID)
			req.Header.Set("X-Timestamp", stamp)
			hash := sha256.Sum256([]byte(body))
			_, _ = mac.Write([]byte(strings.Join([]string{"v1", "POST", path, stamp, hex.EncodeToString(hash[:])}, "\n")))
		}
		signature := hex.EncodeToString(mac.Sum(nil))
		if !auth {
			signature = strings.Repeat("0", 64)
		}
		req.Header.Set(header, prefix+signature)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		return rr
	}
	expect := func(rr *httptest.ResponseRecorder, status int) {
		t.Helper()
		if rr.Code != status {
			t.Fatalf("unexpected status %d want %d: %s", rr.Code, status, rr.Body)
		}
		if status == 429 && (rr.Header().Get("Retry-After") != "1" || !strings.Contains(rr.Body.String(), "trigger_rate_limited")) {
			t.Fatal("missing throttle contract")
		}
	}
	seed := func(b manager.Trigger, credit int) {
		t.Helper()
		reviewExec(t, db, `INSERT INTO manager_hook_ingress_budgets(workspace_id,trigger_id,credit_us,refilled_at)
  VALUES($1,$2,$3,now()+interval '10 minutes') ON CONFLICT(workspace_id,trigger_id)
  DO UPDATE SET credit_us=EXCLUDED.credit_us,refilled_at=EXCLUDED.refilled_at`, w, b.ID, credit)
	}
	countEvents := func(b manager.Trigger, want int) {
		t.Helper()
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM manager_trigger_events WHERE trigger_id=$1`, b.ID).Scan(&n); err != nil || n != want {
			t.Fatalf("unexpected event count %d, want %d, err=%v", n, want, err)
		}
	}
	b, k := makeBinding("generic")
	k2, err := st.CreateTriggerCredential(ctx, w, actor, agent, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"version":1,"eventId":"same","action":"create_task","description":"check"}`
	expect(send(b, k, "webhook", body, false), 401)
	var buckets int
	if err = db.QueryRow(`SELECT count(*) FROM manager_hook_ingress_budgets WHERE trigger_id=$1`, b.ID).Scan(&buckets); err != nil || buckets != 0 {
		t.Fatal("unauthenticated request created bucket", err)
	}
	seed(b, 1500000)
	expect(send(b, k, "webhook", body, false), 401)
	expect(send(b, k, "webhook", body, true), 202)
	expect(send(b, k2, "webhook", body, true), 200)
	badTask := `{"version":1,"eventId":"retry","action":"supplement_task","description":"update","taskId":"` + manager.NewID() + `"}`
	expect(send(b, k2, "webhook", badTask, true), 403)
	countEvents(b, 1)
	corrected := strings.Replace(body, "same", "retry", 1)
	expect(send(b, k, "webhook", corrected, true), 429)
	countEvents(b, 1)
	seed(b, 500000)
	expect(send(b, k2, "webhook", corrected, true), 202)
	countEvents(b, 2)

	gh, gkey := makeBinding("github_issue")
	ghBody := `{"action":"opened","repository":{"id":123},"installation":{"id":44},"issue":{"id":77,"number":7,"title":"bug","body":"check"}}`
	seed(gh, 500000)
	expect(send(gh, gkey, "github", ghBody, false), 401)
	expect(send(gh, gkey, "github", strings.Replace(ghBody, "opened", "closed", 1), true), 204)
	expect(send(gh, gkey, "github", ghBody, true), 429)
	countEvents(gh, 0)
	seed(gh, 500000)
	expect(send(gh, gkey, "github", ghBody, true), 202)
	countEvents(gh, 1)

	// Failure of the budget dependency must not let valid signatures bypass it.
	reviewExec(t, db, `ALTER FUNCTION manager_consume_hook_ingress(uuid,uuid,timestamptz) RENAME TO temporarily_unavailable_hook_ingress`)
	expect(send(b, k2, "webhook", strings.Replace(body, "same", "dependency", 1), true), 503)
	countEvents(b, 2)
}
