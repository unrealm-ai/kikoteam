package controller

import (
	"context"
	"net/http"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

type channelReactionStore interface {
	ClaimChannelReaction(context.Context) (*service.ChannelReactionClaim, error)
	AuthorizeChannelReaction(context.Context, string, string) (*service.ChannelReactionTarget, error)
}

func (s *ChannelServer) reactionRoutes(mux *http.ServeMux) {
	store, ok := s.Store.(channelReactionStore)
	if !ok {
		return
	}
	mux.HandleFunc("POST /api/internal/v1/channel-reactions/claim", s.wrap(func(r *http.Request) (any, error) {
		claim, err := store.ClaimChannelReaction(r.Context())
		return map[string]any{"claim": claim}, err
	}))
	mux.HandleFunc("POST /api/internal/v1/channel-reactions/{id}/authorize", s.wrap(func(r *http.Request) (any, error) {
		var claim struct {
			Token string `json:"claim_token"`
		}
		if err := decode(r, &claim); err != nil {
			return nil, err
		}
		target, err := store.AuthorizeChannelReaction(r.Context(), r.PathValue("id"), claim.Token)
		return map[string]any{"target": target}, err
	}))
}
