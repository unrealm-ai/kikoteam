package repository

import (
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/unrealm-ai/kikoteam/backend/internal/manager/service"
)

type delegatedRef struct {
	ID   string `json:"id,omitempty"`
	Path string `json:"path,omitempty"`
}

type delegatedInputAdmission struct {
	destination string // InsertSession changes target.Session during a create.
	selected    []delegatedRef
	required    []delegatedRef
	checked     bool
	retrying    bool
}

// The conversation lock serializes retries with admission. A known rejection
// rolls back every task, input, receipt and event before keeping its file selection.
func (t *sessionTx) commitDelegatedInput(fn func(service.SessionTx) error) error {
	files := t.target.DelegatedFiles
	a := &delegatedInputAdmission{destination: t.target.Session}
	for _, id := range files.IDs {
		a.selected = append(a.selected, delegatedRef{ID: id})
	}
	for _, p := range files.Paths {
		a.selected = append(a.selected, delegatedRef{Path: p})
	}
	t.delegationInput = a
	if len(a.selected) != 0 {
		if _, err := t.tx.ExecContext(t.ctx, "SAVEPOINT delegate_input"); err != nil {
			return err
		}
	}
	err := fn(t)
	if err != nil {
		var problem *service.Error
		if !a.checked || len(a.selected) == 0 || !errors.As(err, &problem) {
			return err
		}
		if _, rollbackErr := t.tx.ExecContext(t.ctx, "ROLLBACK TO SAVEPOINT delegate_input"); rollbackErr != nil {
			return rollbackErr
		}
		if saveErr := t.rememberDelegatedFiles(); saveErr != nil {
			return saveErr
		}
	} else if a.retrying && a.checked {
		if _, clearErr := t.tx.ExecContext(t.ctx, `DELETE FROM manager_delegation_attachment_retries WHERE source_input_id=$1 AND destination=$2`, t.target.SourceInputID, a.destination); clearErr != nil {
			return clearErr
		}
	}
	if commitErr := t.tx.Commit(); commitErr != nil {
		return commitErr
	}
	return err
}

// Called after deduplication. A replay of an accepted input reads its original
// receipt and cannot clear the requirements for another, still-pending operation.
func (t *sessionTx) CheckDelegatedInput() error {
	a := t.delegationInput
	if a == nil {
		return nil
	}
	channel, isChannel := service.ChannelActorFrom(t.ctx)
	var hasFiles bool
	var pending []byte
	err := t.tx.QueryRowContext(t.ctx, `SELECT i.attachments <> '[]'::jsonb,r.required_refs
 FROM manager_inputs i LEFT JOIN manager_delegation_attachment_retries r ON r.source_input_id=i.id AND r.destination=$4
 WHERE i.id=$1 AND i.session_id=$2 AND i.workspace_id=$3 AND i.source_kind='user' AND i.trigger_agent_loop AND manager_input_actor_allowed(i.id)
 AND ((NOT $6 AND i.submitted_by=$5 AND i.channel_principal_id IS NULL)
 OR ($6 AND i.channel_principal_id=$5 AND i.channel_generation=$7))`,
		t.target.SourceInputID, t.target.Source.SessionID, t.target.Workspace, a.destination, t.target.Actor, isChannel, channel.Generation).Scan(&hasFiles, &pending)
	if errors.Is(err, sql.ErrNoRows) {
		return service.Fail("delegation_input_changed", 409, "无法核实本次委派的用户消息")
	}
	if err != nil {
		return err
	}
	a.retrying = pending != nil
	if a.retrying {
		if err := json.Unmarshal(pending, &a.required); err != nil {
			return err
		}
		var missing []string
		for _, ref := range a.required {
			if !slices.Contains(a.selected, ref) {
				field := "attachment_ids: " + ref.ID
				if ref.Path != "" {
					field = "attachment_paths: " + ref.Path
				}
				missing = append(missing, field)
			}
		}
		if len(missing) != 0 {
			return service.Fail("attachment_retry_required", 409, "重试须保留已选文件（"+strings.Join(missing, "，")+"）")
		}
		if len(a.selected) == 0 {
			return service.Fail("attachment_retry_required", 409, "上次文件委派未成功，请纠正文件引用或让用户确认，不能改为空附件")
		}
	}
	unused := t.target.DelegatedFiles.SourceFilesUnused
	if unused && len(a.selected) != 0 {
		return service.Fail("invalid_argument", 400, "已选择文件时不能声明无需附件")
	}
	if hasFiles && len(a.selected) == 0 && !unused {
		return service.Fail("attachment_selection_required", 409, "来源消息带有附件，请选择本任务所需的引用；仅在任务与附件无关时声明 source_files_unused")
	}
	var denied error
	for _, ref := range a.selected {
		err := t.checkAttachmentSource([]service.Attachment{{ID: ref.ID, Path: ref.Path}})
		var problem *service.Error
		switch {
		case err == nil:
			if !slices.Contains(a.required, ref) {
				a.required = append(a.required, ref)
			}
		case errors.As(err, &problem) && problem.Code == "permission_denied":
			// Keep checking: valid siblings after this selector are required too.
			if denied == nil {
				denied = err
			}
		default:
			return err
		}
	}
	a.checked = true
	return denied
}

func (t *sessionTx) rememberDelegatedFiles() error {
	a := t.delegationInput
	if a.required == nil {
		a.required = []delegatedRef{}
	}
	raw, err := json.Marshal(a.required)
	if err != nil {
		return err
	}
	_, err = t.tx.ExecContext(t.ctx, `INSERT INTO manager_delegation_attachment_retries(source_input_id,destination,required_refs) VALUES($1,$2,$3)
 ON CONFLICT (source_input_id,destination) DO UPDATE SET required_refs=excluded.required_refs`, t.target.SourceInputID, a.destination, raw)
	return err
}
