package service

import (
	"context"
	"time"

	proxy "github.com/unrealm-ai/kikoteam/backend/internal/platform/modelproxy"
)

type ProbeInput struct{ Protocol, Endpoint, ModelID, APIKey string }
type ProbeCase struct {
	Status     string `json:"status"`
	Output     string `json:"output"`
	Issue      string `json:"issue"`
	DurationMS int64  `json:"duration_ms"`
	HTTPStatus int    `json:"http_status"`
}
type ProbeResult struct {
	Text  ProbeCase `json:"text"`
	Image ProbeCase `json:"image"`
}
type ModelProbe interface {
	Probe(context.Context, ProbeInput) (ProbeResult, error)
}
type ModelConfig struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Protocol    string  `json:"protocol"`
	BaseURL     string  `json:"base_url"`
	ModelID     string  `json:"model_id"`
	APIKey      *string `json:"api_key,omitempty"`
}
type ModelTestRequest struct {
	ModelConfig
	ProfileID string `json:"model_profile_id,omitempty"`
	Version   int64  `json:"version,omitempty"`
}
type ModelSaveRequest struct {
	ModelConfig
	Version     int64  `json:"version,omitempty"`
	TestReceipt string `json:"test_receipt"`
}
type ModelProfile struct {
	Source             string       `json:"source,omitempty"`
	Prices             *proxy.Price `json:"prices,omitempty"`
	Replayed           bool         `json:"-"`
	ID                 string       `json:"id"`
	Name               string       `json:"name"`
	Description        string       `json:"description"`
	Protocol           string       `json:"protocol"`
	BaseURL            string       `json:"base_url"`
	ModelID            string       `json:"model_id"`
	Version            int64        `json:"version"`
	Validated          bool         `json:"validated"`
	HasKey             bool         `json:"has_key"`
	VisionStatus       string       `json:"vision_status"`
	Enabled            bool         `json:"enabled"`
	IsDefault          bool         `json:"is_default"`
	TestedAt           time.Time    `json:"tested_at"`
	Credential         []byte       `json:"-"`
	CredentialRevision int64        `json:"-"`
}
type ModelPage struct {
	Items    []ModelProfile `json:"items"`
	Total    int            `json:"total"`
	Page     int            `json:"page"`
	PageSize int            `json:"page_size"`
}
type ModelTestReceipt struct {
	ProbeResult
	Receipt   string    `json:"receipt"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ModelReceiptRecord contains only a keyed configuration digest, never a plaintext credential.
type ModelReceiptRecord struct {
	Hash, Actor, ProfileID, Fingerprint string
	Version                             int64
	ExpiresAt                           time.Time
	TestedAt                            time.Time
	Result                              ProbeResult
	UsedProfileID                       string
}

// ModelTx holds the workspace lock, also taken by membership changes. No external I/O occurs inside it.
type ModelTx interface {
	ClaimProbe(context.Context, string, string, time.Duration) error
	ReleaseProbe(context.Context, string, string) error
	Member(context.Context, string) (Member, error)
	Get(context.Context, string) (ModelProfile, error)
	List(context.Context, int, int) ([]ModelProfile, int, error)
	Save(context.Context, ModelProfile, bool) error
	InsertReceipt(context.Context, ModelReceiptRecord) error
	Receipt(context.Context, string) (ModelReceiptRecord, error)
	ConsumeReceipt(context.Context, string, string) error
	ClearDefault(context.Context) error
}
type ModelStore interface {
	AllowProbe(context.Context, string, string) error
	WithWorkspace(context.Context, string, func(ModelTx) error) error
}
type ModelService struct {
	OnReleaseError    func()
	AllowLoopbackHTTP bool // Test-only construction; never supplied by API input.
	Store             ModelStore
	Prober            ModelProbe
	NormalizeEndpoint func(string, string) (string, error)
	Secret            []byte
	Now               func() time.Time
}
