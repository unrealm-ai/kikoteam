package repository

import (
	"encoding/json"
	"errors"
	"strings"
)

type inputAttachmentRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// The source message explains the user's request. Only the actual task input
// supplies file grants. In particular an unrelated project path stays private.
func renderDelegatedInput(instruction string, source, attachments []byte) (string, error) {
	type file struct {
		ID        string `json:"id,omitempty"`
		Path      string `json:"path,omitempty"`
		Name      string `json:"name,omitempty"`
		Forwarded bool   `json:"forwarded"`
	}
	var original struct {
		MessageID   string `json:"message_id"`
		Text        string `json:"text"`
		Attachments []file `json:"attachments"`
	}
	var granted []file
	if err := json.Unmarshal(source, &original); err != nil {
		return "", err
	}
	if err := json.Unmarshal(attachments, &granted); err != nil {
		return "", err
	}
	for i := range original.Attachments {
		f := &original.Attachments[i]
		if f.ID != "" {
			f.Path = ""
		}
		f.Forwarded = false
		for _, g := range granted {
			if (f.ID != "" && f.ID == g.ID) || (f.ID == "" && g.ID == "" && f.Path != "" && f.Path == g.Path) {
				f.Forwarded = true
				break
			}
		}
		if !f.Forwarded {
			f.ID, f.Path = "", ""
		}
	}
	raw, err := json.Marshal(original)
	if err != nil {
		return "", err
	}
	return "来源用户消息（原文及随附文件顺序，forwarded 表示本次转交）：\n" + string(raw) +
		"\n\n本任务委派说明与已转交文件（仅执行本次分工；若所需文件缺失或说明与原文冲突，请报告原因）：\n" + instruction, nil
}

// An inbound attachment is an opaque reference, not a project path. A typed
// projection prevents internal locators, credentials and staging paths from
// reaching Pi even if the stored record acquires additional fields.
func renderInputAttachments(text string, data []byte) (string, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(data, &items); err != nil {
		return "", errors.New("invalid stored input attachments")
	}
	var refs []inputAttachmentRef
	var paths []string
	for _, raw := range items {
		var item struct {
			inputAttachmentRef
			Path string `json:"path"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}
		switch {
		case item.ID != "":
			refs = append(refs, item.inputAttachmentRef)
		case item.Path != "":
			paths = append(paths, item.Path)
		}
	}
	if len(refs) > 0 {
		var labels []string
		for _, ref := range refs {
			label := "[附件"
			if ref.Name != "" {
				name, err := json.Marshal(ref.Name)
				if err != nil {
					return "", err
				}
				label += " 名称=" + string(name)
			}
			id := ref.ID
			if strings.ContainsAny(id, " \t\r\n[]\"") {
				encoded, err := json.Marshal(id)
				if err != nil {
					return "", err
				}
				id = string(encoded)
			}
			labels = append(labels, label+" attachment_id="+id+"]")
		}
		if text != "" {
			text += "\n\n"
		}
		text += strings.Join(labels, "\n")
	}
	if len(paths) > 0 {
		data, err := json.Marshal(paths)
		if err != nil {
			return "", err
		}
		text += "\n\n本条消息引用的项目文件（相对路径，按顺序）：\n" + string(data)
	}
	return text, nil
}
