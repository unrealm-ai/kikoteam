package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fixtureBinding() Binding {
	return Binding{ID: "b", Provider: "feishu", Enabled: true, Generation: 1, RulesVersion: 1, Revision: 1, Credentials: Credentials{AppID: "app", AppSecret: "secret"}}
}
func TestRefreshAuthentication(t *testing.T) {
	b, err := NewBackend("http://localhost", "secret")
	if err != nil {
		t.Fatal(err)
	}
	s := NewSupervisor(b, "", time.Second, nil)
	for _, v := range []struct {
		token, origin string
		want          int
	}{{"", "", 401}, {"Bearer bad", "", 401}, {"Bearer secret", "https://evil", 401}, {"Bearer secret", "", 202}} {
		r := httptest.NewRequest("POST", "/api/internal/v1/channel-bindings/refresh", strings.NewReader(`{"revision":2}`))
		r.Header.Set("Authorization", v.token)
		r.Header.Set("Origin", v.origin)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != v.want {
			t.Fatalf("got %d want %d", w.Code, v.want)
		}
	}
}
func TestSnapshotReconcileCancelAndStale(t *testing.T) {
	var rev atomic.Int64
	rev.Store(2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing auth")
		}
		if r.Method == "GET" {
			if err := json.NewEncoder(w).Encode(Snapshot{Revision: rev.Load(), Items: []Binding{fixtureBinding()}}); err != nil {
				t.Error(err)
			}
		}
	}))
	defer srv.Close()
	b, err := NewBackend(srv.URL, "secret")
	if err != nil {
		t.Fatal(err)
	}
	s := NewSupervisor(b, "", time.Second, nil)
	entered := make(chan struct{}, 2)
	exited := make(chan struct{}, 2)
	s.Connect = func(ctx context.Context, _ Binding, _ *Backend) error {
		entered <- struct{}{}
		<-ctx.Done()
		exited <- struct{}{}
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if e := s.Recover(ctx, nil); e != nil {
		t.Fatal(e)
	}
	s.reconcile(ctx)
	<-entered
	rev.Store(1)
	if e := s.fetch(ctx); e == nil {
		t.Fatal("stale config accepted")
	}
	if got, ok := s.Current("b"); !ok || got.Generation != 1 {
		t.Fatal("lost valid snapshot")
	}
	s.mu.Lock()
	s.snapshot = Snapshot{Revision: 3}
	s.mu.Unlock()
	s.reconcile(ctx)
	select {
	case <-exited:
	default:
		t.Fatal("removed binding not joined")
	}
	if _, ok := s.Current("b"); ok {
		t.Fatal("removed binding retained")
	}
	s.stop()
}
func TestBackendFailureIsNotAcceptance(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer srv.Close()
	b, err := NewBackend(srv.URL, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if e := b.Inbound(context.Background(), Inbound{}); e == nil {
		t.Fatal("failed persistence accepted")
	}
	s := NewSupervisor(b, "", time.Second, nil)
	if e := s.Recover(context.Background(), nil); e == nil {
		t.Fatal("first fetch failure accepted")
	}
	if len(s.active) != 0 {
		t.Fatal("connection started without config")
	}
}

func TestDurableReceiptRequired(t *testing.T) {
	for _, body := range []string{`{}`, `{"id":"not-id","status":"accepted"}`, `{"id":"11111111-1111-4111-8111-111111111111","status":"queued"}`, `{"id":"11111111-1111-4111-8111-111111111111","status":"accepted"}`} {
		t.Run(body, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if _, e := w.Write([]byte(body)); e != nil {
					t.Error(e)
				}
			}))
			defer srv.Close()
			b, e := NewBackend(srv.URL, "secret")
			if e != nil {
				t.Fatal(e)
			}
			e = b.Inbound(context.Background(), Inbound{})
			want := strings.Contains(body, `111111111111","status":"accepted`)
			if (e == nil) != want {
				t.Fatalf("receipt %s error=%v", body, e)
			}
		})
	}
}
