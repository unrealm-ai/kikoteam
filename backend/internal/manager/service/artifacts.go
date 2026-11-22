package service

import "context"

type Artifact struct {
	ID        string `json:"artifact_id"`
	Version   string `json:"version"`
	Name      string `json:"name"`
	MediaType string `json:"media_type"`
	Size      int64  `json:"size_bytes"`
}
type ArtifactStore interface {
	SelectArtifacts(context.Context, DelegationScope, []string) (any, error)
	OfferArtifacts(context.Context, DelegationScope, string) ([]Artifact, error)
}
