package channel

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/unrealm-ai/kikoteam/event-hub/internal/channelhistory"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHistoryAuthorizationAndFallback(t *testing.T) {
	for _, scenario := range []string{"ok", "fetch_failed", "revoked", "mutated"} {
		t.Run(scenario, func(t *testing.T) {
			j := channelhistory.Job{TenantID: "tenant", ID: "job", LeaseToken: "lease", BindingID: "b", Generation: 1, Revision: 1, RulesVersion: 1, ChatID: "chat", TriggerID: "trigger", TriggerTimeMS: 1000, Limit: 10, Mode: "since_last_trigger"}
			fetched, completed := false, false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/authorize") {
					if scenario == "revoked" {
						w.WriteHeader(409)
						return
					}
					a := j
					if scenario == "mutated" {
						a.ChatID = "other"
					}
					if e := json.NewEncoder(w).Encode(struct {
						channelhistory.Job
						Allowed bool `json:"allowed"`
					}{a, true}); e != nil {
						t.Error(e)
					}
					return
				}
				if !strings.HasSuffix(r.URL.Path, "/complete") {
					t.Error("unexpected path")
					return
				}
				completed = true
				var v struct {
					LeaseToken string        `json:"lease_token"`
					Snapshot   historyResult `json:"snapshot"`
				}
				if e := json.NewDecoder(r.Body).Decode(&v); e != nil {
					t.Error(e)
				}
				want := "complete"
				if scenario == "fetch_failed" {
					want = "unavailable"
				}
				if v.LeaseToken != "lease" || v.Snapshot.Status != want {
					t.Errorf("bad completion %+v", v)
				}
			}))
			defer srv.Close()
			b, e := NewBackend(srv.URL, "secret")
			if e != nil {
				t.Fatal(e)
			}
			lookup := func(string) (Binding, bool) {
				b := fixtureBinding()
				b.History.Enabled = true
				b.TenantID = "tenant"
				return b, true
			}
			fetch := func(ctx context.Context, _ Binding, j channelhistory.Job, gate channelhistory.Gate) (channelhistory.Snapshot, error) {
				fetched = true
				if e := gate(ctx, j); e != nil {
					return channelhistory.Snapshot{}, e
				}
				if scenario == "fetch_failed" {
					return channelhistory.Snapshot{}, errors.New("provider unavailable")
				}
				return channelhistory.Snapshot{JobID: j.ID, TriggerID: j.TriggerID, Messages: []channelhistory.Message{}, Digest: "digest"}, nil
			}
			e = processHistory(context.Background(), b, lookup, fetch, j)
			if scenario == "mutated" {
				if e == nil || fetched || completed {
					t.Fatal("changed authority accepted")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if scenario == "revoked" {
				if fetched || completed {
					t.Fatal("revoked job fetched")
				}
			} else if !fetched || !completed {
				t.Fatal("job not completed")
			}
		})
	}
}
