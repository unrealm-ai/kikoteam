package controller

import (
	"net/http"

	mp "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy/controller"
	"github.com/unrealm-ai/kikoteam/backend/internal/platform/videogen"
)

func (s *Server) videoRoutes(m *http.ServeMux) {
	if s.Videos == nil {
		return
	}
	const base = "/api/v1/platform/default-plugins/video-generation"
	route := func(pattern, name string, op mp.Operation) { m.HandleFunc(pattern, s.wrapModelProxy(name, op)) }
	route("GET "+base, "getPlatformVideoGeneration", func(r *http.Request, a string) (any, int, error) {
		v, e := s.Videos.Settings(r.Context(), a)
		return v, 200, e
	})
	route("PATCH "+base, "updatePlatformVideoGeneration", func(r *http.Request, a string) (any, int, error) {
		var p videogen.Patch
		if e := decode(r, &p); e != nil {
			return nil, 400, e
		}
		v, e := s.Videos.Save(r.Context(), a, p)
		return v, 200, e
	})
	route("POST "+base+"/test", "testPlatformVideoGeneration", func(r *http.Request, a string) (any, int, error) {
		var p videogen.Patch
		if e := decode(r, &p); e != nil {
			return nil, 400, e
		}
		v, e := s.Videos.Test(r.Context(), a, p)
		return v, 200, e
	})
	route("GET "+base+"/reviews", "listVideoGenerationReviews", func(r *http.Request, a string) (any, int, error) {
		v, e := s.Videos.Reviews(r.Context(), a)
		return map[string]any{"items": v}, 200, e
	})
	route("POST "+base+"/reviews/{generation_id}", "resolveVideoGeneration", func(r *http.Request, a string) (any, int, error) {
		var p videogen.Resolution
		if e := decode(r, &p); e != nil {
			return nil, 400, e
		}
		e := s.Videos.Resolve(r.Context(), a, r.PathValue("generation_id"), p)
		return nil, 204, e
	})
}
