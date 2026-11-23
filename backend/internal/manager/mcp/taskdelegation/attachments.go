package taskdelegation

import (
	"maps"

	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

// Chat tool results, like chat prompts, don't expose transfer status. Keep the
// stored metadata and public Web responses unchanged; the task can query live.
func chatResult(out any) any {
	switch v := out.(type) {
	case manager.Input:
		return struct {
			manager.Input
			Attachments []chatAttachment `json:"attachments,omitempty"`
		}{v, chatAttachments(v.Attachments)}
	case manager.PlannedTask:
		return struct {
			manager.PlannedTask
			Attachments []chatAttachment `json:"attachments,omitempty"`
		}{v, chatAttachments(v.Attachments)}
	case map[string]any:
		if input, ok := v["input"].(*manager.Input); ok && input != nil {
			result := maps.Clone(v)
			result["input"] = chatResult(*input)
			return result
		}
		return v
	default:
		return out
	}
}

type chatAttachment struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
	Path string `json:"path,omitempty"`
}

func chatAttachments(items []manager.Attachment) []chatAttachment {
	if len(items) == 0 {
		return nil
	}
	out := make([]chatAttachment, len(items))
	for i, a := range items {
		out[i] = chatAttachment{ID: a.ID, Name: a.Name}
		if a.ID == "" {
			out[i].Path = a.Path // Retain existing project-file references.
		}
	}
	return out
}
