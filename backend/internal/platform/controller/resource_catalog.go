package controller

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/unrealm-ai/kikoteam/backend/internal/platform/service"
)

func resourcePage(r *http.Request) (int, int, service.ResourceListFilter, error) {
	q := r.URL.Query()
	filter := service.ResourceListFilter{Scope: q.Get("scope"), Query: strings.TrimSpace(q.Get("q"))}
	if len(q["q"]) > 1 || utf8.RuneCountInString(filter.Query) > 200 {
		return 0, 0, filter, service.Fail("invalid_query", 400, "搜索词过长或重复")
	}
	q.Del("q")
	if len(q["scope"]) > 1 {
		return 0, 0, filter, service.Fail("invalid_scope", 400, "资源范围重复")
	}
	q.Del("scope")
	p, n, e := pagination(q, 50)
	return p, n, filter, e
}

func (s *Server) catalogRoutes(m *http.ServeMux) {
	if s.Catalog == nil {
		return
	}
	for _, kind := range []string{"skill", "plugin"} {
		global := "/api/v1/official-" + kind + "s"
		local := "/api/v1/workspaces/{workspace_id}/" + kind + "s/{" + kind + "_id}"
		for pattern, action := range map[string]string{
			"GET " + global: "list", "POST " + global: "publish", "GET " + global + "/{" + kind + "_id}": "get", "PATCH " + global + "/{" + kind + "_id}": "state",
			"GET " + global + "/{" + kind + "_id}/versions": "versions", "POST " + global + "/{" + kind + "_id}/versions": "publish",
			"POST " + local + "/copy": "copy", "POST " + local + "/assign": "assign", "GET " + local + "/agents": "uses", "DELETE " + local: "archive",
		} {
			m.HandleFunc(pattern, s.wrap(pattern, func(_ http.ResponseWriter, r *http.Request) (any, int, error) {
				return s.catalogAction(r, kind, action)
			}))
		}
	}
}
func (s *Server) catalogAction(r *http.Request, kind, action string) (any, int, error) {
	a, e := s.actor(r)
	if e != nil {
		return nil, 0, e
	}
	ctx := r.Context()
	w := r.PathValue("workspace_id")
	id := r.PathValue(kind + "_id")
	switch action {
	case "list":
		p, n, filter, err := resourcePage(r)
		if err != nil {
			return nil, 0, err
		}
		if filter.Scope != "" {
			return nil, 0, service.Fail("invalid_scope", 400, "官方目录不支持此范围")
		}
		v, err := s.Catalog.Official(ctx, a.ID, kind, id, action, p, n, filter.Query)
		return v, 200, err
	case "get", "versions", "uses":
		p, n, e := page(r)
		if e != nil {
			return nil, 0, e
		}
		if action == "uses" {
			v, e := s.Catalog.Uses(ctx, w, a.ID, kind, id, p, n)
			return v, 200, e
		}
		v, e := s.Catalog.Official(ctx, a.ID, kind, id, action, p, n, "")
		return v, 200, e
	case "publish", "copy":
		var body service.ResourcePublication
		if action == "copy" {
			var copy struct {
				VersionID string `json:"version_id"`
			}
			if e = decode(r, &copy); e != nil {
				return nil, 0, e
			}
			body.VersionID = copy.VersionID
			body.SourceWorkspaceID = w
			body.ResourceID = id
			id = ""
		} else if e = decode(r, &body); e != nil {
			return nil, 0, e
		}
		if e = body.Validate(); e != nil {
			return nil, 0, e
		}
		v, e := s.Catalog.Publish(ctx, a.ID, kind, w, id, body)
		return v, 201, e
	case "state":
		var body service.ResourceStateChange
		if e = decode(r, &body); e != nil {
			return nil, 0, e
		}
		v, e := s.Catalog.Change(ctx, a.ID, kind, id, body)
		return v, 200, e
	case "archive":
		var body struct {
			Version int64 `json:"version"`
		}
		if e = decode(r, &body); e != nil {
			return nil, 0, e
		}
		if body.Version < 1 {
			return nil, 0, service.Fail("version_required", 400, "请提供当前资源版本")
		}
		return nil, 204, s.Catalog.Archive(ctx, w, a.ID, kind, id, body.Version)
	default:
		var body service.ResourceAssignment
		if e = decode(r, &body); e != nil {
			return nil, 0, e
		}
		result, e := s.Packages.Assign(ctx, w, a.ID, kind, id, body)
		return map[string]any{"items": result}, 200, e
	}
}
