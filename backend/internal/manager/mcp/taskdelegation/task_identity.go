package taskdelegation

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	manager "github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

// Old in-flight tool calls may still submit a session_id from model history.
// Translate only the well-known task selectors and only after the original
// chat's authority has resolved it. The catalog and all replies stay task-only.
func translateLegacyTaskSelector(r *http.Request, d *manager.Delegation, a toolAccess) error {
	if r.Method != http.MethodPost || a.report || a.schemaOnly {
		return nil
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	body := raw
	defer func() {
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
	}()
	if !bytes.Contains(raw, []byte(`"session_id"`)) {
		return nil
	}
	var req map[string]json.RawMessage
	var params map[string]json.RawMessage
	var args map[string]json.RawMessage
	if json.Unmarshal(raw, &req) != nil || string(req["method"]) != `"tools/call"` ||
		json.Unmarshal(req["params"], &params) != nil || json.Unmarshal(params["arguments"], &args) != nil || args["task_id"] != nil {
		return nil
	}
	var name, session string
	if json.Unmarshal(params["name"], &name) != nil || !legacyTaskSelector(name) ||
		json.Unmarshal(args["session_id"], &session) != nil || !manager.ValidID(session) {
		return nil
	}
	ctx := r.Context()
	if a.scope.WebGroup != nil {
		ctx = manager.WithWebGroupExecution(ctx, *a.scope.WebGroup)
	}
	if a.scope.Channel != nil {
		ctx = manager.WithChannelActor(ctx, *a.scope.Channel)
	}
	v, err := d.Store.DelegatedSession(ctx, a.scope, session)
	if err != nil || v.TaskID == nil || !manager.ValidID(*v.TaskID) {
		// The regular strict schema still rejects the unresolvable alias. Don't
		// reflect a guessed or unauthorized identifier in a recovery hint.
		return nil
	}
	args["task_id"], err = json.Marshal(*v.TaskID)
	if err != nil {
		return err
	}
	delete(args, "session_id")
	params["arguments"], err = json.Marshal(args)
	if err != nil {
		return err
	}
	req["params"], err = json.Marshal(params)
	if err != nil {
		return err
	}
	body, err = json.Marshal(req)
	return err
}

func legacyTaskSelector(name string) bool {
	selector, ok := strings.CutPrefix(name, "task_delegation_")
	if !ok {
		return false
	}
	switch selector {
	case "get_session", "send_message", "stop_session", "start_todo", "cancel_todo", "supplement_todo":
		return true
	default:
		return false
	}
}

// Projection only at the chat tool boundary: the store, runner and Web
// contracts still use Session and its native IDs internally.
func modelTaskResult(out any, taskID string) (any, error) {
	raw, err := json.Marshal(chatResult(out))
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value any
	if err = dec.Decode(&value); err != nil {
		return nil, err
	}
	hideSessionIdentity(value)
	if fields, ok := value.(map[string]any); ok && taskID != "" {
		fields["task_id"] = taskID
	}
	return value, nil
}

func hideSessionIdentity(value any) {
	switch v := value.(type) {
	case map[string]any:
		for k, child := range v {
			if k == "session_id" || strings.HasSuffix(k, "_session_id") {
				delete(v, k)
			} else {
				hideSessionIdentity(child)
			}
		}
		// Policy list and stop returned the native Session record; its generic
		// id is also a session identity. Keep id on Inputs/Turns/events.
		if v["mode"] == "task" && v["task_id"] != nil && v["execution"] != nil {
			delete(v, "id")
		}
	case []any:
		for _, child := range v {
			hideSessionIdentity(child)
		}
	}
}
