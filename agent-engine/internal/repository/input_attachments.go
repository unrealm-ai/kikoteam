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
