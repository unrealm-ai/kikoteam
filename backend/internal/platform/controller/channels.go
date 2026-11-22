package controller

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func (s *Server) channelRoutes(m *http.ServeMux) {
	if s.Channels == nil {
		return
	}
	s.channelDirectoryRoutes(m)
	s.channelGroupDiscoveryRoutes(m)
	s.wecomGroupRefreshRoutes(m)
	s.channelObservedChatRoutes(m)
	p := "/api/v1/workspaces/{workspace_id}/agents/{agent_id}/channels/{provider}"
	m.HandleFunc("POST "+p+"/test-connection", s.wrap("POST "+p+"/test-connection", s.channelTestCredentials))
	m.HandleFunc("GET "+p, s.wrap("GET "+p, s.channelGet))
	m.HandleFunc("PUT "+p, s.wrap("PUT "+p, s.channelPut))
}
func (s *Server) channelGet(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	if values, exists := r.URL.Query()["include_rebind_impact"]; exists {
		if len(values) != 1 || (values[0] != "true" && values[0] != "false") {
			return nil, 0, service.Fail("invalid_request", 400, "换绑影响查询参数无效")
		}
		if values[0] == "true" {
			v, e := s.Channels.ProviderWithRebindImpact(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("agent_id"), r.PathValue("provider"))
			return v, 200, e
		}
	}
	v, e := s.Channels.ObservedProvider(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("agent_id"), r.PathValue("provider"))
	return v, 200, e
}
func (s *Server) channelPut(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	var q struct {
		TestReceipt     string  `json:"test_receipt,omitempty"`
		ConfirmedImpact bool    `json:"confirmed_impact,omitempty"`
		Version         int64   `json:"version"`
		AppID           string  `json:"app_id"`
		AppSecret       *string `json:"app_secret,omitempty"`
		Enabled         bool    `json:"enabled"`
	}
	if e = decode(r, &q); e != nil {
		return nil, 0, e
	}
	v, e := s.Channels.PutManual(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("agent_id"), r.PathValue("provider"), service.ChannelPut{Version: q.Version, AppID: q.AppID, AppSecret: q.AppSecret, Enabled: q.Enabled, History: service.ChannelHistory{Limit: 20}, Access: service.ChannelAccess{Mode: "allowlist"}}, q.TestReceipt, q.ConfirmedImpact)
	return v, 200, e
}

// ChannelInternalRoutes uses a dedicated Hub credential, never browser sessions or Engine tokens.
func ChannelInternalRoutes(m *http.ServeMux, s *service.ChannelService, token string) {
	handler := func(snapshot bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			if len(token) < 32 || r.Header.Get("Cookie") != "" || r.Header.Get("Origin") != "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
				w.WriteHeader(401)
				return
			}
			var value any
			var e error
			if snapshot {
				value, e = s.Snapshot(r.Context())
			} else {
				r.Body = http.MaxBytesReader(w, r.Body, 8192)
				var q service.ChannelApplied
				d := json.NewDecoder(r.Body)
				d.DisallowUnknownFields()
				if err := d.Decode(&q); err != nil {
					e = service.Fail("invalid_request", 400, "请求无效")
				} else {
					var extra any
					if d.Decode(&extra) != io.EOF {
						e = service.Fail("invalid_request", 400, "请求无效")
					} else {
						e = s.Apply(r.Context(), r.PathValue("id"), q)
					}
				}

			}
			if e != nil {
				status, code := 503, "channel_unavailable"
				var se *service.Error
				if errors.As(e, &se) {
					status, code = se.Status, se.Code
				}
				w.WriteHeader(status)
				if err := json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code}}); err != nil {
					return
				}
				return
			}
			if !snapshot {
				w.WriteHeader(204)
				return
			}
			if err := json.NewEncoder(w).Encode(value); err != nil {
				return
			}
		}
	}
	m.HandleFunc("GET /api/internal/v1/channel-bindings", handler(true))
	m.HandleFunc("POST /api/internal/v1/channel-bindings/{id}/applied", handler(false))
}

func (s *Server) channelTestCredentials(_ http.ResponseWriter, r *http.Request) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	var q service.ChannelCredentialTest
	if e = decode(r, &q); e != nil {
		return nil, 0, e
	}
	v, e := s.Channels.TestCredentials(r.Context(), r.PathValue("workspace_id"), a.ID, r.PathValue("agent_id"), r.PathValue("provider"), q)
	return v, 200, e
}
