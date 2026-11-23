package service

import "context"

type Artifact struct {
	ID        string `json:"artifact_id"`
	Version   string `json:"version"`
	Name      string `json:"name"`
	MediaType string `json:"media_type"`
	Size      int64  `json:"size_bytes"`
}

// ArtifactSelectionCandidate is the safe retry projection for the current reply.
type ArtifactSelectionCandidate struct {
	ID   string `json:"artifact_id"`
	Name string `json:"name"`
}

type ArtifactSelectionError struct {
	Err        error
	Candidates []ArtifactSelectionCandidate
}

func (e *ArtifactSelectionError) Error() string { return e.Err.Error() }
func (e *ArtifactSelectionError) Unwrap() error { return e.Err }

type ArtifactStore interface {
	SelectArtifacts(context.Context, DelegationScope, []string) (any, error)
	OfferArtifacts(context.Context, DelegationScope, string) ([]Artifact, error)
}
