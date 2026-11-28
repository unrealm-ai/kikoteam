package controller

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/imagegen"
	mp "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/controller"
	repo "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/repository"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func (s *Server) imageRoutes(m *http.ServeMux) {
	base := "/api/v1/platform/default-plugins"
	route := func(pattern, name string, op mp.Operation) { m.HandleFunc(pattern, s.wrapModelProxy(name, op)) }
	route("PUT /api/v1/workspaces/{workspace_id}/agents/{agent_id}/payer-binding", "bindAgentPayer", s.bindPayer)
	if s.Images == nil {
		return
	}
	route("GET "+base, "listPlatformDefaultPlugins", func(r *http.Request, a string) (any, int, error) {
		v, e := s.Images.Directory(r.Context(), a)
		return map[string]any{"items": v}, 200, e
	})
	route("GET "+base+"/image-generation", "getPlatformImageGeneration", func(r *http.Request, a string) (any, int, error) {
		v, e := s.Images.Settings(r.Context(), a)
		return v, 200, e
	})
	route("POST "+base+"/image-generation/test", "testPlatformImageGeneration", func(r *http.Request, a string) (any, int, error) {
		var p imagegen.Patch
		if e := decode(r, &p); e != nil {
			return nil, 400, e
		}
		v, e := s.Images.Test(r.Context(), a, p)
		return v, 200, e
	})
	route("PATCH "+base+"/image-generation", "updatePlatformImageGeneration", func(r *http.Request, a string) (any, int, error) {
		var p imagegen.Patch
		if e := decode(r, &p); e != nil {
			return nil, 400, e
		}
		v, e := s.Images.Save(r.Context(), a, p)
		return v, 200, e
	})
	route("GET /api/v1/image-generations", "listImageGenerations", s.imageUsage)
	route("POST /api/v1/image-generations/{generation_id}/resolve", "resolveImageGeneration", func(r *http.Request, a string) (any, int, error) {
		var p struct {
			Action string `json:"action"`
			Reason string `json:"reason"`
		}
		if e := decode(r, &p); e != nil {
			return nil, 400, e
		}
		p.Reason = strings.TrimSpace(p.Reason)
		id := r.PathValue("generation_id")
		if !repo.UUID(id) || !slices.Contains([]string{"reconcile", "close_no_charge"}, p.Action) || len(p.Reason) == 0 || len(p.Reason) > 1024 {
			return nil, 400, service.Fail("invalid_argument", 400, "生成标识、核对动作或原因无效")
		}
		v, e := s.Images.Resolve(r.Context(), a, id, p.Action, p.Reason)
		return v, 200, e
	})
}
func (s *Server) bindPayer(r *http.Request, a string) (any, int, error) {
	var p struct {
		Confirm bool  `json:"confirm_self"`
		Version int64 `json:"version"`
	}
	if e := decode(r, &p); e != nil {
		return nil, 400, e
	}
	if !p.Confirm || p.Version < 1 {
		return nil, 400, service.Fail("invalid_argument", 400, "请确认由本人支付并提供员工版本")
	}
	v, e := s.Agents.BindPayer(r.Context(), r.PathValue("workspace_id"), a, r.PathValue("agent_id"), p.Version)
	return v, 200, e
}
func (s *Server) imageUsage(r *http.Request, a string) (any, int, error) {
	q := r.URL.Query()
	invalid := service.Fail("invalid_query", 400, "出图记录查询参数无效")
	for k, vs := range q {
		if len(vs) != 1 || !slices.Contains([]string{"account_id", "agent_id", "status", "billing_status", "from", "to", "cursor", "limit", "scope"}, k) {
			return nil, 400, invalid
		}
	}
	f := imagegen.Filter{Account: q.Get("account_id"), Agent: q.Get("agent_id"), Status: q.Get("status"), Billing: q.Get("billing_status")}
	if f.Account != "" && !repo.UUID(f.Account) || f.Agent != "" && !repo.UUID(f.Agent) || !slices.Contains([]string{"", "admitted", "dispatched", "pending_review", "ready", "failed"}, f.Status) || !slices.Contains([]string{"", "pending", "settled", "no_charge"}, f.Billing) {
		return nil, 400, invalid
	}
	if q.Get("scope") == "all" {
		if f.Account != "" {
			return nil, 400, invalid
		}
	} else if q.Get("scope") != "" {
		return nil, 400, invalid
	} else if f.Account == "" {
		f.Account = a
	}
	for k, dest := range map[string]**time.Time{"from": &f.From, "to": &f.To} {
		if q.Has(k) {
			t, e := time.Parse(time.RFC3339, q.Get(k))
			if e != nil {
				return nil, 400, invalid
			}
			*dest = &t
		}
	}
	n := 30
	if q.Has("limit") {
		v, e := strconv.Atoi(q.Get("limit"))
		if e != nil || v < 1 || v > 100 {
			return nil, 400, invalid
		}
		n = v
	}
	if f.From != nil && f.To != nil && !f.From.Before(*f.To) {
		return nil, 400, invalid
	}
	v, e := s.Images.List(r.Context(), a, f, q.Get("cursor"), n)
	return v, 200, e
}
