package integration

import "encoding/json"

type Skill struct {
	SkillID     string `json:"skill_id"`
	VersionID   string `json:"version_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Scope       string `json:"scope"`
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
