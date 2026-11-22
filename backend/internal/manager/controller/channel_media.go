package controller

import (
	"context"
	"io"
	"net/http"
	"strconv"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

type channelMediaStore interface {
	ClaimMedia(context.Context, *service.Service) (*service.MediaJob, error)
	FinishMedia(context.Context, *service.Service, string, int, string, []byte, bool) error
	ReadDeliveryArtifact(context.Context, string, string, bool) (service.Artifact, []byte, error)
}

func (s *ChannelServer) mediaRoutes(mux *http.ServeMux) {
	store, ok := s.Store.(channelMediaStore)
	if !ok {
		return
	}
	mux.HandleFunc("POST /api/internal/v1/channel-media/claim", s.wrap(func(r *http.Request) (any, error) {
		j, e := store.ClaimMedia(r.Context(), s.Manager)
		return map[string]any{"job": j}, e
	}))
	finish := func(r *http.Request) (any, error) {
		index, e := strconv.Atoi(r.PathValue("ordinal"))
		if e != nil {
			return nil, service.Fail("invalid_argument", 400, "附件序号无效")
		}
		var data []byte
		failed := r.Method == "POST"
		if !failed {
			data, e = io.ReadAll(r.Body)
			if e != nil || len(data) > service.ProjectUploadLimit {
				return nil, service.Fail("file_too_large", 413, "附件过大")
			}
		}
		e = store.FinishMedia(r.Context(), s.Manager, r.PathValue("id"), index, r.Header.Get("X-Kiko-Media-Lease"), data, failed)
		return map[string]bool{"accepted": e == nil}, e
	}
	mux.HandleFunc("PUT /api/internal/v1/channel-media/{id}/{ordinal}", s.wrap(finish))
	mux.HandleFunc("POST /api/internal/v1/channel-media/{id}/{ordinal}/failed", s.wrap(finish))
	for _, suffix := range []string{"content", "dispatch-check"} {
		mux.HandleFunc("POST /api/internal/v1/channel-deliveries/{id}/"+suffix, s.wrap(func(r *http.Request) (any, error) {
			var q struct {
				Lease string `json:"lease_token"`
			}
			if e := decode(r, &q); e != nil {
				return nil, e
			}
			a, data, e := store.ReadDeliveryArtifact(r.Context(), r.PathValue("id"), q.Lease, suffix == "content")
			if e != nil {
				return nil, e
			}
			return map[string]any{"artifact": a, "bytes": data}, nil
		}))
	}
}
