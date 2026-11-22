package integration

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/unrealm-ai/kikoteam/agent-engine/internal/repository"
)

type SkillFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type Skill struct {
	SkillID     string `json:"skill_id"`
	VersionID   string `json:"version_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Manifest    struct {
		FormatVersion int         `json:"format_version"`
		Digest        string      `json:"digest"`
		Files         []SkillFile `json:"files"`
	} `json:"manifest"`
}
type PluginTool struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"input_schema"`
	SchemaDigest string          `json:"schema_digest"`
}
type Plugin struct {
	PluginID       string            `json:"plugin_id"`
	VersionID      string            `json:"version_id"`
	AccountID      string            `json:"account_id"`
	AccountVersion int64             `json:"account_version"`
	Endpoint       string            `json:"endpoint"`
	Headers        map[string]string `json:"headers"`
	Tools          []PluginTool      `json:"tools"`
}
type Resources struct {
	Skills   []Skill  `json:"skills"`
	Plugins  []Plugin `json:"plugins"`
	Problems []string `json:"problems"`
}

func (b *Backend) SkillFile(ctx context.Context, w repository.Work, input string, skill Skill, file SkillFile) (string, error) {
	q := url.Values{"engine_id": {b.EngineID}, "assignment_id": {w.AssignmentID}, "generation": {strconv.FormatInt(w.Generation, 10)}, "input_id": {input}, "skill_id": {skill.SkillID}, "version_id": {skill.VersionID}, "path": {file.Path}}
	var out struct {
		Content string `json:"content_base64"`
	}
	err := b.Call(ctx, "GET", "/sessions/"+w.SessionID+"/skill-files?"+q.Encode(), nil, &out)
	return out.Content, err
}
