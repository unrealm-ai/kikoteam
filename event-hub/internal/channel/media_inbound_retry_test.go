package channel

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestInboundMediaStoreAndDownloadReportAgainstSameLease(t *testing.T) {
	for _, tc := range []struct {
		name         string
		downloadErr  error
		storeStatus  int
		dropResponse bool
		want         []string
	}{
		{"download failure", errors.New("fixture download failure"), 0, false, []string{"POST /api/internal/v1/channel-media/inbox/1/failed"}},
		{"storage failure", nil, 503, false, []string{"PUT /api/internal/v1/channel-media/inbox/1", "POST /api/internal/v1/channel-media/inbox/1/failed"}},
		{"store response lost", nil, 200, true, []string{"PUT /api/internal/v1/channel-media/inbox/1", "POST /api/internal/v1/channel-media/inbox/1/failed"}},
		{"success", nil, 200, false, []string{"PUT /api/internal/v1/channel-media/inbox/1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Kiko-Media-Lease") != "original" {
					t.Error("settlement changed lease")
				}
				got = append(got, r.Method+" "+r.URL.Path)
				data, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				if r.Method == http.MethodPut {
					if string(data) != "fixtures" {
						t.Error("missing fetched bytes")
					}
					if tc.dropResponse {
						conn, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						if err := conn.Close(); err != nil {
							t.Error(err)
						}
						return
					}
					w.WriteHeader(tc.storeStatus)
					return
				}
				if len(data) != 0 {
					t.Error("failure sent bytes again")
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer h.Close()
			client, err := NewBackend(h.URL, "hub-token")
			if err != nil {
				t.Fatal(err)
			}
			j := MediaJob{InboxID: "inbox", Ordinal: 1, Lease: "original"}
			err = client.completeMedia(context.Background(), j, []byte("fixtures"), tc.downloadErr)
			if tc.storeStatus == 503 || tc.dropResponse {
				if err == nil {
					t.Fatal("storage failure was not observable")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatal(strings.Join(got, "; "), tc.want)
			}
		})
	}
}
