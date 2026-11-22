package service

import (
	"context"
	"time"
)

type SkillFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type SkillManifest struct {
	FormatVersion int         `json:"format_version"`
	Files         []SkillFile `json:"files"`
	Digest        string      `json:"digest"`
}
type SkillSource struct {
	TemplateVersionID  string `json:"-"`
	ResourceKey        string `json:"-"`
	CandidateID        string `json:"-"`
	CandidateVersionID string `json:"-"`
	CandidatePrefix    string `json:"-"`
	Type               string `json:"type"`
	RepositoryURL      string `json:"repository_url,omitempty"`
	Ref                string `json:"ref,omitempty"`
	Commit             string `json:"commit,omitempty"`
	Subpath            string `json:"subpath,omitempty"`
}
type SkillImportRequest struct {
	Source        string `json:"source"`
	ZIPBase64     string `json:"zip_base64,omitempty"`
	Markdown      string `json:"markdown,omitempty"`
	RepositoryURL string `json:"repository_url,omitempty"`
	Ref           string `json:"ref,omitempty"`
	Subpath       string `json:"subpath,omitempty"`
	Name          string `json:"name,omitempty"`
	Description   string `json:"description,omitempty"`
	Body          string `json:"body,omitempty"`
}
type SkillBundle struct {
	TechnicalName string
	Description   string
	Manifest      SkillManifest
	Source        SkillSource
	Files         map[string][]byte
}
type SkillImport struct {
	ID                 string        `json:"id"`
	TechnicalName      string        `json:"technical_name"`
	Description        string        `json:"description"`
	Manifest           SkillManifest `json:"manifest"`
	Source             SkillSource   `json:"source"`
	ExpiresAt          time.Time     `json:"expires_at"`
	ActorID            string        `json:"-"`
	StorageIdentity    string        `json:"-"`
	KeyPrefix          string        `json:"-"`
	PublishedSkillID   string        `json:"-"`
	PublishedVersionID string        `json:"-"`
	PublishFingerprint string        `json:"-"`
	Ready              bool          `json:"-"`
}
type SkillVersion struct {
	ID              string        `json:"id"`
	Sequence        int64         `json:"sequence"`
	VersionLabel    string        `json:"version_label"`
	TechnicalName   string        `json:"technical_name"`
	Description     string        `json:"description"`
	Manifest        SkillManifest `json:"manifest"`
	Source          SkillSource   `json:"source"`
	CreatedAt       time.Time     `json:"created_at"`
	StorageIdentity string        `json:"-"`
	KeyPrefix       string        `json:"-"`
}
type Skill struct {
	Card               *ResourceCard `json:"card,omitempty"`
	Scope              string        `json:"scope"`
	Listed             bool          `json:"listed"`
	AssignedAgentCount int           `json:"assigned_agent_count"`
	ID                 string        `json:"id"`
	Name               string        `json:"name"`
	Description        string        `json:"description"`
	Enabled            bool          `json:"enabled"`
	Version            int64         `json:"version"`
	CurrentVersionID   string        `json:"current_version_id"`
	CurrentVersion     SkillVersion  `json:"current_version"`
	CreatedAt          time.Time     `json:"created_at"`
	UpdatedAt          time.Time     `json:"updated_at"`
}
type SkillPublish struct {
	ImportID     string `json:"import_id"`
	Name         string `json:"name,omitempty"`
	Version      int64  `json:"version,omitempty"`
	VersionLabel string `json:"version_label,omitempty"`
}
type SkillPatch struct {
	Version int64   `json:"version"`
	Name    *string `json:"name,omitempty"`
	Enabled *bool   `json:"enabled,omitempty"`
}
type SkillPage struct {
	Items    []Skill `json:"items"`
	Total    int     `json:"total"`
	Page     int     `json:"page"`
	PageSize int     `json:"page_size"`
}
type SkillFileContent struct {
	Path          string `json:"path"`
	Size          int64  `json:"size"`
	SHA256        string `json:"sha256"`
	ContentBase64 string `json:"content_base64"`
}

// SkillAssets owns file I/O, not authorization. Only the service supplies trusted prefixes.
type SkillAssets interface {
	Cleanup(context.Context, string, SkillManifest) error
	Identity() string
	Prepare(context.Context, SkillImportRequest) (SkillBundle, error)
	Write(context.Context, string, SkillBundle) error
	Copy(context.Context, string, string, SkillManifest) error
	Read(context.Context, string, SkillFile) ([]byte, error)
}
type SkillTx interface {
	Member(context.Context, string) (Member, error)
	Get(context.Context, string) (Skill, error)
	List(context.Context, int, int, ResourceListFilter) ([]Skill, int, error)
	Versions(context.Context, string, int, int) ([]SkillVersion, int, error)
	GetVersion(context.Context, string, string) (SkillVersion, error)
	Import(context.Context, string) (SkillImport, error)
	SaveImport(context.Context, SkillImport) error
	MarkReady(context.Context, string) error
	Save(context.Context, Skill, SkillVersion, bool) error
	MarkPublished(context.Context, string, string, string, string) error
	Patch(context.Context, Skill) error
}
type SkillStore interface {
	CleanupCandidates(context.Context, string) ([]SkillImport, error)
	MarkCleaned(context.Context, string) error
	WithWorkspace(context.Context, string, func(SkillTx) error) error
}
type SkillService struct {
	Store  SkillStore
	Assets SkillAssets
}
